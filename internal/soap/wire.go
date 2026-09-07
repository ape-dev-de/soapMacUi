package soap

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"
)

// BodyMode bestimmt, wie der Body vor dem Senden behandelt wird.
type BodyMode string

const (
	// BodyExact sendet byte-genau das, was im Editor steht. Standard, weil
	// jede Umformatierung bei einem strengen Gegenüber ein Risiko ist.
	BodyExact BodyMode = "exact"
	// BodyStrip entfernt Whitespace zwischen Elementen.
	BodyStrip BodyMode = "strip"
	// BodyPretty formatiert neu ein.
	BodyPretty BodyMode = "pretty"
)

// SOAPActionMode steuert die Schreibweise des SOAPAction-Headers.
type SOAPActionMode string

const (
	ActionQuoted   SOAPActionMode = "quoted"
	ActionUnquoted SOAPActionMode = "unquoted"
	ActionOmit     SOAPActionMode = "omit"
)

// LineEnding bestimmt die Zeilenenden im gesendeten Body.
type LineEnding string

const (
	LineLF   LineEnding = "LF"
	LineCRLF LineEnding = "CRLF"
	LineKeep LineEnding = "keep"
)

// WireOptions bündelt alles, was die exakten Bytes auf der Leitung bestimmt.
// Jede Option wird explizit persistiert — auch wenn sie dem Default entspricht,
// damit ein gespeicherter Request nach einem Update identisch bleibt.
type WireOptions struct {
	Body       BodyMode   `json:"body"`
	LineEnding LineEnding `json:"lineEnding"`

	XMLDeclaration bool   `json:"xmlDeclaration"`
	Encoding       string `json:"encoding"` // UTF-8 | ISO-8859-1
	BOM            bool   `json:"bom"`

	SOAPAction     SOAPActionMode `json:"soapAction"`
	CharsetInCT    bool           `json:"charsetInContentType"`
	ContentTypeOvr string         `json:"contentTypeOverride"`

	Chunked    bool `json:"chunked"`
	Expect100  bool `json:"expect100Continue"`
	AcceptGzip bool `json:"acceptGzip"`

	// HeaderOrder legt die Reihenfolge der Header fest. Nicht genannte Header
	// folgen alphabetisch danach.
	HeaderOrder []string `json:"headerOrder"`
}

// DefaultWireOptions liefert die Vorgaben: nichts anfassen, was nicht sein muss.
func DefaultWireOptions() WireOptions {
	return WireOptions{
		Body:           BodyExact,
		LineEnding:     LineKeep,
		XMLDeclaration: true,
		Encoding:       "UTF-8",
		BOM:            false,
		SOAPAction:     ActionQuoted,
		CharsetInCT:    true,
		Chunked:        false,
		Expect100:      false,
		AcceptGzip:     true,
	}
}

var betweenTags = regexp.MustCompile(`>[ \t\r\n]+<`)

// EncodeBody wandelt den Editor-Text in die zu sendenden Bytes.
func EncodeBody(body string, o WireOptions) ([]byte, error) {
	switch o.Body {
	case BodyStrip:
		// Nur Whitespace *zwischen* Tags. Text innerhalb von Elementen bleibt
		// unangetastet — bei mixed content ist das trotzdem eine Änderung,
		// deshalb ist der Modus bewusst nicht Standard.
		body = betweenTags.ReplaceAllString(body, "><")
		body = strings.TrimSpace(body)
	case BodyPretty:
		p, err := Reindent(body, "  ")
		if err != nil {
			return nil, fmt.Errorf("neu formatieren: %w", err)
		}
		body = p
	}

	switch o.LineEnding {
	case LineLF:
		body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\r", "\n")
	case LineCRLF:
		body = strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	}

	enc := strings.ToUpper(strings.TrimSpace(o.Encoding))
	if enc == "" {
		enc = "UTF-8"
	}

	var out []byte
	if o.XMLDeclaration && !strings.HasPrefix(strings.TrimSpace(body), "<?xml") {
		nl := "\n"
		if o.LineEnding == LineCRLF {
			nl = "\r\n"
		}
		out = append(out, []byte(`<?xml version="1.0" encoding="`+enc+`"?>`+nl)...)
	}
	out = append(out, []byte(body)...)

	switch enc {
	case "UTF-8", "UTF8":
		if o.BOM {
			out = append([]byte{0xEF, 0xBB, 0xBF}, out...)
		}
	case "ISO-8859-1", "LATIN1", "ISO8859-1":
		conv, err := toLatin1(out)
		if err != nil {
			return nil, err
		}
		out = conv
	default:
		return nil, fmt.Errorf("nicht unterstütztes Encoding %q", o.Encoding)
	}
	return out, nil
}

// toLatin1 kodiert UTF-8 nach ISO-8859-1 und meldet, was nicht darstellbar ist,
// statt still ein Fragezeichen zu senden.
func toLatin1(in []byte) ([]byte, error) {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); {
		r, size := utf8.DecodeRune(in[i:])
		if r == utf8.RuneError && size == 1 {
			return nil, fmt.Errorf("ungültige UTF-8-Sequenz an Position %d", i)
		}
		if r > 0xFF {
			return nil, fmt.Errorf("zeichen %q lässt sich nicht in ISO-8859-1 darstellen (Position %d) — als XML-Entity schreiben oder UTF-8 verwenden", r, i)
		}
		out = append(out, byte(r))
		i += size
	}
	return out, nil
}

// ContentType baut den Content-Type-Header für einen nicht-multipart-Request.
func ContentType(version string, o WireOptions) string {
	if o.ContentTypeOvr != "" {
		return o.ContentTypeOvr
	}
	enc := o.Encoding
	if enc == "" {
		enc = "UTF-8"
	}
	base := "text/xml"
	if version == "1.2" {
		base = "application/soap+xml"
	}
	if o.CharsetInCT {
		return base + "; charset=" + enc
	}
	return base
}

// SOAPActionHeader liefert Name und Wert des Action-Headers, oder ok=false,
// wenn keiner gesetzt werden soll.
func SOAPActionHeader(version, action string, o WireOptions) (name, value string, ok bool) {
	if version == "1.2" {
		// Bei SOAP 1.2 gehört die Action als Parameter in den Content-Type,
		// nicht in einen eigenen Header.
		return "", "", false
	}
	switch o.SOAPAction {
	case ActionOmit:
		return "", "", false
	case ActionUnquoted:
		return "SOAPAction", action, true
	default:
		return "SOAPAction", `"` + action + `"`, true
	}
}

// Reindent formatiert XML neu ein. Bewusst getrennt von EncodeBody, damit der
// Editor formatieren kann, ohne dass das den Sendepfad verändert.
func Reindent(src, indent string) (string, error) {
	dec := xml.NewDecoder(strings.NewReader(src))
	dec.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }

	var out bytes.Buffer
	depth := 0
	lastWasStart := false

	writeIndent := func(d int) {
		out.WriteString("\n")
		for i := 0; i < d; i++ {
			out.WriteString(indent)
		}
	}

	for {
		tok, err := dec.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.ProcInst:
			out.WriteString("<?" + t.Target + " " + string(t.Inst) + "?>")
		case xml.Comment:
			if out.Len() > 0 {
				writeIndent(depth)
			}
			out.WriteString("<!--" + string(t) + "-->")
			lastWasStart = false
		case xml.StartElement:
			if out.Len() > 0 {
				writeIndent(depth)
			}
			out.WriteString("<" + rawName(t.Name))
			for _, a := range t.Attr {
				n := a.Name.Local
				if a.Name.Space != "" {
					n = a.Name.Space + ":" + a.Name.Local
				}
				out.WriteString(fmt.Sprintf(` %s=%q`, n, a.Value))
			}
			out.WriteString(">")
			depth++
			lastWasStart = true
		case xml.EndElement:
			depth--
			if !lastWasStart {
				writeIndent(depth)
			}
			out.WriteString("</" + rawName(t.Name) + ">")
			lastWasStart = false
		case xml.CharData:
			s := strings.TrimSpace(string(t))
			if s != "" {
				out.WriteString(escapeText(s))
			} else {
				lastWasStart = false
			}
		}
	}
	return strings.TrimLeft(out.String(), "\n"), nil
}

// rawName setzt einen Namen aus RawToken wieder zusammen. RawToken liefert im
// Feld Space das Präfix, nicht die URI — genau das wollen wir hier, weil
// Reindent die Präfixe des Originals erhalten soll.
func rawName(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

func escapeText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}
