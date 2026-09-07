package wsdl

import (
	"encoding/xml"
	"strings"

	"github.com/apeters/soapmacui/internal/xdom"
	"github.com/apeters/soapmacui/internal/xsd"
)

// newDefinitions legt eine leere Definitionsmenge an.
func newDefinitions() *Definitions {
	return &Definitions{
		Messages:  map[xml.Name]*Message{},
		PortTypes: map[xml.Name]*PortType{},
		Bindings:  map[xml.Name]*Binding{},
		Services:  map[xml.Name]*Service{},
		Schemas:   xsd.NewSet(),
		Prefixes:  map[string]string{},
	}
}

func (d *Definitions) orderedServices() []*Service {
	out := make([]*Service, 0, len(d.Services))
	for _, n := range d.serviceOrder {
		if s, ok := d.Services[n]; ok {
			out = append(out, s)
		}
	}
	return out
}

// parseDefinitions liest ein wsdl:definitions-Element in d ein.
// Mehrfachaufruf ist erlaubt (wsdl:import).
func (d *Definitions) parseDefinitions(root *xdom.Node, location string) {
	if d.TargetNS == "" {
		d.TargetNS = root.Attr("targetNamespace")
		d.Name = root.Attr("name")
		d.Doc = docOf(root)
	}
	for pre, uri := range root.Namespaces() {
		if pre == "" || uri == "" {
			continue
		}
		if _, exists := d.Prefixes[pre]; !exists {
			d.Prefixes[pre] = uri
		}
	}

	tns := root.Attr("targetNamespace")

	for _, c := range root.Children {
		if c.Name.Space != NS {
			continue
		}
		switch c.Name.Local {
		case "types":
			for _, sc := range c.Children {
				if sc.Name.Space == xsd.NS && sc.Name.Local == "schema" {
					d.Schemas.ParseSchema(sc, location)
				}
			}
		case "message":
			m := &Message{Name: xml.Name{Space: tns, Local: c.Attr("name")}}
			for _, p := range c.ChildrenNamed(NS, "part") {
				part := &Part{Name: p.Attr("name")}
				if e := p.Attr("element"); e != "" {
					part.Element = p.ResolveQName(e)
				}
				if t := p.Attr("type"); t != "" {
					part.Type = p.ResolveQName(t)
				}
				m.Parts = append(m.Parts, part)
			}
			d.Messages[m.Name] = m

		case "portType":
			pt := &PortType{Name: xml.Name{Space: tns, Local: c.Attr("name")}}
			for _, o := range c.ChildrenNamed(NS, "operation") {
				op := &Operation{Name: o.Attr("name"), Doc: docOf(o)}
				if in := o.Child(NS, "input"); in != nil {
					op.Input = &OpMessage{Name: in.Attr("name"), Message: in.ResolveQName(in.Attr("message"))}
				}
				if out := o.Child(NS, "output"); out != nil {
					op.Output = &OpMessage{Name: out.Attr("name"), Message: out.ResolveQName(out.Attr("message"))}
				}
				for _, f := range o.ChildrenNamed(NS, "fault") {
					op.Faults = append(op.Faults, &OpMessage{Name: f.Attr("name"), Message: f.ResolveQName(f.Attr("message"))})
				}
				pt.Operations = append(pt.Operations, op)
			}
			d.PortTypes[pt.Name] = pt

		case "binding":
			b := &Binding{
				Name:       xml.Name{Space: tns, Local: c.Attr("name")},
				PortType:   c.ResolveQName(c.Attr("type")),
				Operations: map[string]*BindingOperation{},
				Version:    SOAP11,
			}
			// soap:binding bestimmt Version, Style und Transport
			if sb := c.Child(NSSoap12, "binding"); sb != nil {
				b.IsSOAP, b.Version = true, SOAP12
				b.Style, b.Transport = sb.Attr("style"), sb.Attr("transport")
			} else if sb := c.Child(NSSoap11, "binding"); sb != nil {
				b.IsSOAP, b.Version = true, SOAP11
				b.Style, b.Transport = sb.Attr("style"), sb.Attr("transport")
			}
			if b.Style == "" {
				b.Style = "document"
			}
			b.PolicyMTOM = mentionsMTOM(c)

			soapNS := NSSoap11
			if b.Version == SOAP12 {
				soapNS = NSSoap12
			}
			for _, o := range c.ChildrenNamed(NS, "operation") {
				bo := &BindingOperation{Name: o.Attr("name")}
				if so := o.Child(soapNS, "operation"); so != nil {
					bo.SOAPAction = so.Attr("soapAction")
					bo.Style = so.Attr("style")
				}
				if in := o.Child(NS, "input"); in != nil {
					bo.Input = bodyBinding(in, soapNS)
					bo.InHeaders = headerBindings(in, soapNS)
					bo.InputAttachments = mimeParts(in)
				}
				if out := o.Child(NS, "output"); out != nil {
					bo.Output = bodyBinding(out, soapNS)
					bo.OutHeaders = headerBindings(out, soapNS)
				}
				b.Operations[bo.Name] = bo
			}
			d.Bindings[b.Name] = b

		case "service":
			svc := &Service{Name: xml.Name{Space: tns, Local: c.Attr("name")}, Doc: docOf(c)}
			for _, p := range c.ChildrenNamed(NS, "port") {
				port := &Port{Name: p.Attr("name"), Binding: p.ResolveQName(p.Attr("binding")), Version: SOAP11}
				if a := p.Child(NSSoap12, "address"); a != nil {
					port.Address, port.Version = a.Attr("location"), SOAP12
				} else if a := p.Child(NSSoap11, "address"); a != nil {
					port.Address, port.Version = a.Attr("location"), SOAP11
				} else if a := p.Child(NSHTTP, "address"); a != nil {
					port.Address = a.Attr("location")
				}
				svc.Ports = append(svc.Ports, port)
			}
			if _, dup := d.Services[svc.Name]; !dup {
				d.serviceOrder = append(d.serviceOrder, svc.Name)
			}
			d.Services[svc.Name] = svc
		}
	}
}

func bodyBinding(n *xdom.Node, soapNS string) *BodyBinding {
	b := n.Child(soapNS, "body")
	if b == nil {
		// Bei mime:multipartRelated steckt der Body im ersten mime:part.
		if mr := n.Child(NSMime, "multipartRelated"); mr != nil {
			for _, p := range mr.ChildrenNamed(NSMime, "part") {
				if bb := p.Child(soapNS, "body"); bb != nil {
					b = bb
					break
				}
			}
		}
	}
	if b == nil {
		return &BodyBinding{Use: "literal"}
	}
	out := &BodyBinding{Use: b.Attr("use"), Namespace: b.Attr("namespace")}
	if out.Use == "" {
		out.Use = "literal"
	}
	if parts := strings.Fields(b.Attr("parts")); len(parts) > 0 {
		out.Parts = parts
	}
	return out
}

func headerBindings(n *xdom.Node, soapNS string) []*HeaderBinding {
	var out []*HeaderBinding
	for _, h := range n.ChildrenNamed(soapNS, "header") {
		hb := &HeaderBinding{Part: h.Attr("part"), Use: h.Attr("use")}
		if m := h.Attr("message"); m != "" {
			hb.Message = h.ResolveQName(m)
		}
		out = append(out, hb)
	}
	return out
}

// mimeParts liest mime:content-Deklarationen, die auf Anhänge hindeuten.
func mimeParts(n *xdom.Node) []string {
	mr := n.Child(NSMime, "multipartRelated")
	if mr == nil {
		return nil
	}
	var out []string
	for _, p := range mr.ChildrenNamed(NSMime, "part") {
		for _, mc := range p.ChildrenNamed(NSMime, "content") {
			if part := mc.Attr("part"); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

// mentionsMTOM sucht nach WS-Policy-Hinweisen auf MTOM.
func mentionsMTOM(n *xdom.Node) bool {
	found := false
	var walk func(*xdom.Node)
	walk = func(x *xdom.Node) {
		if found {
			return
		}
		l := strings.ToLower(x.Name.Local)
		if strings.Contains(l, "optimizedmimeserialization") || strings.Contains(l, "mtom") {
			found = true
			return
		}
		for _, c := range x.Children {
			walk(c)
		}
	}
	walk(n)
	return found
}

func docOf(n *xdom.Node) string {
	d := n.Child(NS, "documentation")
	if d == nil {
		return ""
	}
	return strings.Join(strings.Fields(d.Text), " ")
}
