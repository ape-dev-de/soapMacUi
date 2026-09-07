package wsdl

import "encoding/xml"

// Operations liefert die flache Liste aller aufrufbaren Operationen,
// je Service/Port/Operation ein Eintrag, in Dokumentreihenfolge.
func (d *Definitions) Operations() []OperationView {
	var out []OperationView
	for _, svc := range d.orderedServices() {
		for _, port := range svc.Ports {
			b := d.Bindings[port.Binding]
			if b == nil || !b.IsSOAP {
				continue
			}
			pt := d.PortTypes[b.PortType]
			if pt == nil {
				continue
			}
			version := b.Version
			if port.Version != "" {
				version = port.Version
			}
			for _, op := range pt.Operations {
				bo := b.Operations[op.Name]
				v := OperationView{
					Service:   svc.Name.Local,
					Port:      port.Name,
					Binding:   b.Name.Local,
					Operation: op.Name,
					Endpoint:  port.Address,
					Version:   version,
					Style:     b.Style,
					Use:       "literal",
					Doc:       op.Doc,
				}
				if op.Input != nil {
					v.InputMessage = op.Input.Message
				}
				if op.Output != nil {
					v.OutputMessage = op.Output.Message
				}
				if bo != nil {
					v.SOAPAction = bo.SOAPAction
					if bo.Style != "" {
						v.Style = bo.Style
					}
					v.InputBody = bo.Input
					v.InHeaders = bo.InHeaders
					if bo.Input != nil && bo.Input.Use != "" {
						v.Use = bo.Input.Use
					}
					v.SuggestMTOM = b.PolicyMTOM || len(bo.InputAttachments) > 0
				} else {
					v.SuggestMTOM = b.PolicyMTOM
				}
				out = append(out, v)
			}
		}
	}
	return out
}

// Message schlägt eine Message nach.
func (d *Definitions) Message(n xml.Name) *Message { return d.Messages[n] }

// InputParts liefert die für den Body relevanten Parts einer Operation.
// soap:body/@parts schränkt die Auswahl ein, wenn gesetzt.
func (d *Definitions) InputParts(v OperationView) []*Part {
	m := d.Messages[v.InputMessage]
	if m == nil {
		return nil
	}
	if v.InputBody == nil || len(v.InputBody.Parts) == 0 {
		return m.Parts
	}
	want := map[string]bool{}
	for _, p := range v.InputBody.Parts {
		want[p] = true
	}
	var out []*Part
	for _, p := range m.Parts {
		if want[p.Name] {
			out = append(out, p)
		}
	}
	return out
}
