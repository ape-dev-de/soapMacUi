// Package soap baut SOAP-Envelopes aus WSDL-Operationen und kümmert sich um
// die Verdrahtung mit MTOM, Headern und den Wire-Optionen.
package soap

import (
	"encoding/xml"
	"fmt"
	"sort"
	"strings"

	"github.com/apeters/soapmacui/internal/wsdl"
	"github.com/apeters/soapmacui/internal/xsd"
)

// Builder erzeugt Beispiel-Requests zu einer Operation.
type Builder struct {
	Defs *wsdl.Definitions
	Opts xsd.GenOptions

	// EnvPrefix ist das Präfix des Envelope-Namespace. SoapUI nutzt "soapenv",
	// und Referenzbeispiele beim Kunden sehen meist genauso aus.
	EnvPrefix string
}

// NewBuilder erzeugt einen Builder mit Vorgaben.
func NewBuilder(d *wsdl.Definitions, opts xsd.GenOptions) *Builder {
	return &Builder{Defs: d, Opts: opts, EnvPrefix: "soapenv"}
}

// BuildRequest erzeugt den vollständigen Envelope einer Operation.
func (b *Builder) BuildRequest(v wsdl.OperationView) (string, error) {
	g := xsd.NewGenerator(b.Defs.Schemas, b.Opts)
	b.seedPrefixes(g)

	envPre := b.EnvPrefix
	if envPre == "" {
		envPre = "soapenv"
	}
	envNS := v.Version.EnvelopeNS()
	g.Pfx.Prefer(envNS, envPre)

	header, err := b.buildHeader(g, v)
	if err != nil {
		return "", err
	}
	body, err := b.buildBody(g, v)
	if err != nil {
		return "", err
	}

	// Präfixe erst nach der Generierung einsammeln — vorher sind noch nicht
	// alle Namespaces bekannt.
	decls := g.Pfx.Declared()
	if g.NeedsXSI() {
		g.Pfx.Prefer(xsd.NSI, "xsi")
		decls = g.Pfx.Declared()
	}

	var sb strings.Builder
	sb.WriteString("<" + envPre + ":Envelope")
	for _, pre := range sortedKeys(decls) {
		sb.WriteString(fmt.Sprintf(" xmlns:%s=\"%s\"", pre, decls[pre]))
	}
	sb.WriteString(">\n")

	if strings.TrimSpace(header) == "" {
		sb.WriteString(b.Opts.IndentOr() + "<" + envPre + ":Header/>\n")
	} else {
		sb.WriteString(b.Opts.IndentOr() + "<" + envPre + ":Header>\n")
		sb.WriteString(header)
		if !strings.HasSuffix(header, "\n") {
			sb.WriteString("\n")
		}
		sb.WriteString(b.Opts.IndentOr() + "</" + envPre + ":Header>\n")
	}

	sb.WriteString(b.Opts.IndentOr() + "<" + envPre + ":Body>\n")
	if strings.TrimSpace(body) != "" {
		sb.WriteString(body)
		if !strings.HasSuffix(body, "\n") {
			sb.WriteString("\n")
		}
	}
	sb.WriteString(b.Opts.IndentOr() + "</" + envPre + ":Body>\n")
	sb.WriteString("</" + envPre + ":Envelope>")
	return sb.String(), nil
}

// buildBody erzeugt den Body je nach document/rpc-Stil.
func (b *Builder) buildBody(g *xsd.Generator, v wsdl.OperationView) (string, error) {
	parts := b.Defs.InputParts(v)
	if len(parts) == 0 {
		return "", nil
	}

	if strings.EqualFold(v.Style, "rpc") {
		// rpc/literal: ein Wrapper-Element mit dem Operationsnamen, darunter
		// die Parts als unqualifizierte Kinder mit ihrem Typ.
		ns := ""
		if v.InputBody != nil {
			ns = v.InputBody.Namespace
		}
		if ns == "" {
			ns = b.Defs.TargetNS
		}
		pre := g.Pfx.Prefix(ns)
		var sb strings.Builder
		ind := b.Opts.IndentOr()
		sb.WriteString(ind + ind + "<" + pre + ":" + v.Operation + ">\n")
		for _, p := range parts {
			frag, err := b.partFragment(g, p, 3)
			if err != nil {
				return "", err
			}
			sb.WriteString(frag + "\n")
		}
		sb.WriteString(ind + ind + "</" + pre + ":" + v.Operation + ">\n")
		return sb.String(), nil
	}

	// document/literal: die Parts referenzieren globale Elemente.
	var sb strings.Builder
	for _, p := range parts {
		frag, err := b.partFragment(g, p, 2)
		if err != nil {
			return "", err
		}
		sb.WriteString(frag + "\n")
	}
	return sb.String(), nil
}

// partFragment erzeugt das XML zu einem Message-Part.
func (b *Builder) partFragment(g *xsd.Generator, p *wsdl.Part, level int) (string, error) {
	switch {
	case p.Element.Local != "":
		return g.GenerateElement(p.Element, level)
	case p.Type.Local != "":
		// rpc-Part: der Elementname ist der Part-Name, unqualifiziert.
		return g.GenerateType(xml.Name{Local: p.Name}, p.Type, level)
	default:
		return "", fmt.Errorf("message-part %q hat weder element noch type", p.Name)
	}
}

// buildHeader erzeugt die im Binding deklarierten SOAP-Header.
func (b *Builder) buildHeader(g *xsd.Generator, v wsdl.OperationView) (string, error) {
	var sb strings.Builder
	for _, h := range v.InHeaders {
		m := b.Defs.Message(h.Message)
		if m == nil {
			continue
		}
		for _, p := range m.Parts {
			if h.Part != "" && p.Name != h.Part {
				continue
			}
			frag, err := b.partFragment(g, p, 2)
			if err != nil {
				// Ein unauflösbarer Header darf den ganzen Request nicht kippen.
				sb.WriteString(fmt.Sprintf("%s<!-- Header %q nicht auflösbar: %v -->\n",
					strings.Repeat(b.Opts.IndentOr(), 2), p.Name, err))
				continue
			}
			sb.WriteString(frag + "\n")
		}
	}
	return sb.String(), nil
}

// seedPrefixes übernimmt die Präfixe aus dem WSDL, damit erzeugte Requests
// dieselben Präfixe verwenden wie die Referenzbeispiele des Kunden.
func (b *Builder) seedPrefixes(g *xsd.Generator) {
	for _, pre := range sortedKeys(b.Defs.Prefixes) {
		uri := b.Defs.Prefixes[pre]
		switch uri {
		case wsdl.NS, wsdl.NSSoap11, wsdl.NSSoap12, wsdl.NSMime, wsdl.NSHTTP, xsd.NS:
			continue // WSDL-Vokabular gehört nicht in den Request
		}
		g.Pfx.Prefer(uri, pre)
	}
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
