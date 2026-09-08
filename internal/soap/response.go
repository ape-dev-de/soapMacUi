package soap

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/apeters/soapmacui/internal/wsdl"
	"github.com/apeters/soapmacui/internal/xdom"
)

// ReceivedAttachment ist ein empfangener Anhang.
// Grosse Anhänge landen auf der Platte und werden nur per Pfad referenziert.
type ReceivedAttachment struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"contentType"`
	Size        int64  `json:"size"`
	Path        string `json:"path"`
	IsXML       bool   `json:"isXml"`
}

// Fault ist ein SOAP-Fault in vereinheitlichter Form.
type Fault struct {
	Code   string `json:"code"`
	Reason string `json:"reason"`
	Actor  string `json:"actor"`
	Detail string `json:"detail"`
}

// ParsedResponse ist die aufbereitete Antwort.
type ParsedResponse struct {
	IsMultipart bool                 `json:"isMultipart"`
	RootType    string               `json:"rootType"`
	Envelope    []byte               `json:"-"`
	Attachments []ReceivedAttachment `json:"attachments"`
	Fault       *Fault               `json:"fault"`
	Warnings    []string             `json:"warnings"`
}

// ParseResponse zerlegt eine Antwort. Bei multipart/related wird der Root-Part
// als Envelope genommen und die übrigen Parts als Anhänge abgelegt.
// attachDir bestimmt, wohin Anhänge geschrieben werden.
func ParseResponse(contentType string, body []byte, attachDir string) (*ParsedResponse, error) {
	out := &ParsedResponse{Envelope: body}

	mt, params, err := mime.ParseMediaType(contentType)
	if err != nil && contentType != "" {
		out.Warnings = append(out.Warnings, fmt.Sprintf("Content-Type %q nicht lesbar: %v", contentType, err))
	}

	if strings.HasPrefix(mt, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return nil, fmt.Errorf("multipart-Antwort ohne boundary-Parameter")
		}
		out.IsMultipart = true
		if err := out.readParts(bytes.NewReader(body), boundary, params["start"], attachDir); err != nil {
			return nil, err
		}
	}

	out.Fault = detectFault(out.Envelope)
	return out, nil
}

func (p *ParsedResponse) readParts(r io.Reader, boundary, start, attachDir string) error {
	mr := multipart.NewReader(r, boundary)
	start = strings.Trim(start, "<>")
	first := true

	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("multipart lesen: %w", err)
		}
		cid := strings.Trim(part.Header.Get("Content-ID"), "<>")
		ct := part.Header.Get("Content-Type")

		isRoot := (start != "" && cid == start) || (start == "" && first)
		first = false

		if isRoot {
			data, err := io.ReadAll(part)
			if err != nil {
				return err
			}
			p.Envelope = data
			p.RootType = ct
			part.Close()
			continue
		}

		att, err := spillAttachment(part, cid, ct, attachDir)
		part.Close()
		if err != nil {
			return err
		}
		p.Attachments = append(p.Attachments, att)
	}
	return nil
}

// spillAttachment schreibt einen Anhang direkt auf die Platte, ohne ihn
// vorher komplett in den Speicher zu holen.
func spillAttachment(r io.Reader, cid, ct, dir string) (ReceivedAttachment, error) {
	if dir == "" {
		dir = os.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ReceivedAttachment{}, err
	}
	// Die Endung kommt aus dem Content-Type, nie aus der Content-ID. Sonst
	// bestimmt die Gegenstelle, als was die Datei auf der Platte landet: eine
	// Content-ID "bericht.terminal" ergäbe eine Datei, die beim Öffnen im
	// Finder von LaunchServices ausgeführt statt angezeigt wird.
	name := safeName(cid)
	name = strings.TrimSuffix(name, filepath.Ext(name))
	// Was jetzt noch aus Punkten und Trennern besteht, taugt nicht als
	// Dateiname — "..", "..." und "-" wären Pfadbestandteile statt Namen.
	if !hasAlnum(name) {
		name = "attachment"
	}
	ext := extForType(ct)
	if ext == "" {
		ext = ".bin"
	}
	name += ext
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return ReceivedAttachment{}, err
	}
	defer f.Close()
	n, err := io.Copy(f, r)
	if err != nil {
		return ReceivedAttachment{}, err
	}
	return ReceivedAttachment{
		ID:          cid,
		Name:        name,
		ContentType: ct,
		Size:        n,
		Path:        path,
		IsXML:       strings.Contains(ct, "xml"),
	}, nil
}

// hasAlnum sagt, ob wenigstens ein Buchstabe oder eine Ziffer enthalten ist.
func hasAlnum(s string) bool {
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}

func safeName(cid string) string {
	if cid == "" {
		cid = "attachment"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			return r
		default:
			return '-'
		}
	}, cid)
}

func extForType(ct string) string {
	mt, _, _ := mime.ParseMediaType(ct)
	switch mt {
	case "application/xml", "text/xml":
		return ".xml"
	case "application/pdf":
		return ".pdf"
	case "text/plain":
		return ".txt"
	case "application/json":
		return ".json"
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "application/zip":
		return ".zip"
	default:
		return ""
	}
}

// detectFault sucht einen SOAP-Fault in beiden Versionen.
func detectFault(envelope []byte) *Fault {
	root, err := xdom.Parse(bytes.NewReader(envelope))
	if err != nil {
		return nil
	}
	var body *xdom.Node
	for _, ns := range []string{wsdl.EnvSoap11, wsdl.EnvSoap12} {
		if b := root.Child(ns, "Body"); b != nil {
			body = b
			break
		}
	}
	if body == nil {
		return nil
	}

	// SOAP 1.1: faultcode/faultstring ohne Namespace.
	if f := body.Child(wsdl.EnvSoap11, "Fault"); f != nil {
		return &Fault{
			Code:   childText(f, "", "faultcode"),
			Reason: childText(f, "", "faultstring"),
			Actor:  childText(f, "", "faultactor"),
			Detail: innerOf(f, "", "detail"),
		}
	}
	// SOAP 1.2: Code/Value und Reason/Text.
	if f := body.Child(wsdl.EnvSoap12, "Fault"); f != nil {
		out := &Fault{Detail: innerOf(f, wsdl.EnvSoap12, "Detail")}
		if c := f.Child(wsdl.EnvSoap12, "Code"); c != nil {
			out.Code = childText(c, wsdl.EnvSoap12, "Value")
		}
		if r := f.Child(wsdl.EnvSoap12, "Reason"); r != nil {
			out.Reason = childText(r, wsdl.EnvSoap12, "Text")
		}
		if role := f.Child(wsdl.EnvSoap12, "Role"); role != nil {
			out.Actor = role.Trimmed()
		}
		return out
	}
	return nil
}

func childText(n *xdom.Node, ns, local string) string {
	if c := n.Child(ns, local); c != nil {
		return c.Trimmed()
	}
	return ""
}

// innerOf liefert eine kompakte Textdarstellung eines Detail-Knotens.
func innerOf(n *xdom.Node, ns, local string) string {
	d := n.Child(ns, local)
	if d == nil {
		return ""
	}
	var sb strings.Builder
	var walk func(*xdom.Node, int)
	walk = func(x *xdom.Node, depth int) {
		for _, c := range x.Children {
			if t := c.Trimmed(); t != "" {
				fmt.Fprintf(&sb, "%s%s: %s\n", strings.Repeat("  ", depth), c.Name.Local, t)
			} else {
				fmt.Fprintf(&sb, "%s%s\n", strings.Repeat("  ", depth), c.Name.Local)
			}
			walk(c, depth+1)
		}
	}
	walk(d, 0)
	if sb.Len() == 0 {
		return d.Trimmed()
	}
	return strings.TrimRight(sb.String(), "\n")
}

// envelopeStart findet das öffnende Envelope-Element, mit oder ohne Präfix.
var envelopeStart = regexp.MustCompile(`<([A-Za-z_][\w.-]*:)?Envelope\b[^>]*>`)
