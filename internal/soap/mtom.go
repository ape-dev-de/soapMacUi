package soap

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// AttachMode bestimmt, wie Anhänge transportiert werden.
// Server implementieren das unterschiedlich, deshalb alle drei Varianten.
type AttachMode string

const (
	// AttachNone: kein multipart, der Body geht allein raus.
	AttachNone AttachMode = "none"
	// AttachMTOM: XOP, multipart/related mit application/xop+xml als Root.
	AttachMTOM AttachMode = "mtom"
	// AttachSwA: klassisches SOAP with Attachments, Root ist text/xml.
	AttachSwA AttachMode = "swa"
	// AttachInline: kein multipart, Anhänge stehen base64 im Body.
	AttachInline AttachMode = "inline"
)

// TransferEncoding der einzelnen Parts.
const (
	TEBinary = "binary"
	TE8Bit   = "8bit"
	TEBase64 = "base64"
)

// Attachment ist ein Anhang. Der Inhalt bleibt als Dateiverweis liegen und
// wird beim Senden gestreamt — er landet nie komplett im Speicher und nie
// im Frontend.
type Attachment struct {
	ID          string `json:"id"`          // Content-ID ohne spitze Klammern
	Name        string `json:"name"`        // Dateiname
	ContentType string `json:"contentType"` // leer => aus Endung geraten
	Path        string `json:"path"`        // Quelle auf der Platte
	Size        int64  `json:"size"`

	// Inline erlaubt Anhänge ohne Datei, etwa aus einem Script erzeugt.
	Inline []byte `json:"-"`
}

// MTOMOptions steuert die Multipart-Erzeugung bis auf Header-Ebene.
type MTOMOptions struct {
	Mode             AttachMode `json:"mode"`
	Boundary         string     `json:"boundary"`         // leer => zufällig
	RootID           string     `json:"rootId"`           // leer => generiert
	TransferEncoding string     `json:"transferEncoding"` // Standard: binary bei MTOM
	StartInfo        bool       `json:"startInfo"`        // start-info-Parameter setzen
	RootCharset      bool       `json:"rootCharset"`      // charset am Root-Part
}

// DefaultMTOMOptions liefert das, was die meisten Stacks erwarten.
func DefaultMTOMOptions() MTOMOptions {
	return MTOMOptions{
		Mode:             AttachNone,
		TransferEncoding: TEBinary,
		StartInfo:        true,
		RootCharset:      true,
	}
}

// XOPNamespace ist der Namespace für xop:Include.
const XOPNamespace = "http://www.w3.org/2004/08/xop/include"

// CIDRef liefert die href-Form einer Content-ID für xop:Include.
func CIDRef(id string) string { return "cid:" + id }

// XOPInclude liefert das Element, das im Body an die Stelle des base64-Inhalts
// gehört. Genau das fügt die UI beim Anhängen einer Datei ein.
func (a Attachment) XOPInclude() string {
	return fmt.Sprintf(`<xop:Include xmlns:xop=%q href=%q/>`, XOPNamespace, CIDRef(a.ID))
}

// part ist ein vorbereiteter Multipart-Abschnitt.
type part struct {
	header []byte
	path   string
	inline []byte
	size   int64
	base64 bool
}

// Multipart ist ein fertig geplanter multipart/related-Körper.
// Die Anhänge sind noch nicht gelesen — erst WriteTo streamt sie.
type Multipart struct {
	ContentType string
	Length      int64

	boundary string
	parts    []part
}

// BuildMultipart plant den Multipart-Körper.
// soapVersion ist "1.1" oder "1.2" und bestimmt die Medientypen.
func BuildMultipart(root []byte, atts []Attachment, o MTOMOptions, soapVersion string) (*Multipart, error) {
	if o.Mode != AttachMTOM && o.Mode != AttachSwA {
		return nil, fmt.Errorf("BuildMultipart nur für mtom oder swa, nicht %q", o.Mode)
	}
	boundary := o.Boundary
	if boundary == "" {
		boundary = randomBoundary()
	}
	rootID := o.RootID
	if rootID == "" {
		rootID = "root.message@soapmacui"
	}
	te := o.TransferEncoding
	if te == "" {
		te = TEBinary
	}

	soapCT := "text/xml"
	if soapVersion == "1.2" {
		soapCT = "application/soap+xml"
	}

	var rootCT string
	if o.Mode == AttachMTOM {
		rootCT = "application/xop+xml"
		if o.RootCharset {
			rootCT += "; charset=UTF-8"
		}
		rootCT += fmt.Sprintf("; type=%q", soapCT)
	} else {
		rootCT = soapCT
		if o.RootCharset {
			rootCT += "; charset=UTF-8"
		}
	}

	m := &Multipart{boundary: boundary}

	// Root-Part
	rootHdr := fmt.Sprintf("--%s\r\nContent-Type: %s\r\nContent-Transfer-Encoding: %s\r\nContent-ID: <%s>\r\n\r\n",
		boundary, rootCT, te, rootID)
	m.parts = append(m.parts, part{header: []byte(rootHdr), inline: root, size: int64(len(root))})

	// Anhänge
	for _, a := range atts {
		size := a.Size
		if a.Inline != nil {
			size = int64(len(a.Inline))
		} else if a.Path != "" {
			st, err := os.Stat(a.Path)
			if err != nil {
				return nil, fmt.Errorf("anhang %q: %w", a.Name, err)
			}
			size = st.Size()
		}
		ct := a.ContentType
		if ct == "" {
			ct = guessContentType(a.Name)
		}
		pte := te
		if pte == TE8Bit {
			// 8bit ist für Binärdaten nicht zulässig; Anhänge gehen binary raus.
			pte = TEBinary
		}
		hdr := fmt.Sprintf("--%s\r\nContent-Type: %s\r\nContent-Transfer-Encoding: %s\r\nContent-ID: <%s>\r\n",
			boundary, ct, pte, a.ID)
		if a.Name != "" {
			hdr += fmt.Sprintf("Content-Disposition: attachment; name=%q; filename=%q\r\n", a.Name, a.Name)
		}
		hdr += "\r\n"

		p := part{header: []byte(hdr), path: a.Path, inline: a.Inline, size: size, base64: pte == TEBase64}
		if p.base64 {
			p.size = int64(base64.StdEncoding.EncodedLen(int(size)))
		}
		m.parts = append(m.parts, p)
	}

	// Länge exakt berechnen, damit Content-Length statt chunked möglich ist.
	var total int64
	for _, p := range m.parts {
		total += int64(len(p.header)) + p.size + 2 // CRLF nach dem Inhalt
	}
	total += int64(len("--" + boundary + "--\r\n"))
	m.Length = total

	ct := fmt.Sprintf("multipart/related; boundary=%q; type=%q; start=\"<%s>\"", boundary, rootCTType(o.Mode, soapCT), rootID)
	if o.Mode == AttachMTOM && o.StartInfo {
		ct += fmt.Sprintf("; start-info=%q", soapCT)
	}
	m.ContentType = ct
	return m, nil
}

func rootCTType(mode AttachMode, soapCT string) string {
	if mode == AttachMTOM {
		return "application/xop+xml"
	}
	return soapCT
}

// WriteTo streamt den Körper. Anhänge werden dabei von der Platte gelesen,
// nicht vorher in den Speicher geholt.
func (m *Multipart) WriteTo(w io.Writer) (int64, error) {
	var n int64
	write := func(b []byte) error {
		c, err := w.Write(b)
		n += int64(c)
		return err
	}
	for _, p := range m.parts {
		if err := write(p.header); err != nil {
			return n, err
		}
		var src io.Reader
		switch {
		case p.inline != nil:
			src = strings.NewReader(string(p.inline))
		case p.path != "":
			f, err := os.Open(p.path)
			if err != nil {
				return n, err
			}
			defer f.Close()
			src = f
		default:
			src = strings.NewReader("")
		}
		var dst io.Writer = w
		var closer io.Closer
		if p.base64 {
			enc := base64.NewEncoder(base64.StdEncoding, w)
			dst, closer = enc, enc
		}
		c, err := io.Copy(dst, src)
		n += c
		if closer != nil {
			if cerr := closer.Close(); err == nil {
				err = cerr
			}
		}
		if err != nil {
			return n, err
		}
		if err := write([]byte("\r\n")); err != nil {
			return n, err
		}
	}
	return n, write([]byte("--" + m.boundary + "--\r\n"))
}

// Preview liefert eine Textdarstellung für die Raw-Wire-Ansicht. Binärinhalte
// werden als Grössenangabe gezeigt, damit die Ansicht nicht den Speicher sprengt.
func (m *Multipart) Preview(maxInline int64) string {
	var sb strings.Builder
	for i, p := range m.parts {
		sb.Write(p.header)
		switch {
		case i == 0 && p.inline != nil:
			sb.Write(p.inline)
		case p.size <= maxInline && p.inline != nil:
			sb.Write(p.inline)
		default:
			fmt.Fprintf(&sb, "[%d Bytes Inhalt — in der Raw-Ansicht ausgelassen]", p.size)
		}
		sb.WriteString("\r\n")
	}
	sb.WriteString("--" + m.boundary + "--\r\n")
	return sb.String()
}

func randomBoundary() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "----=_Part_SoapMacUi_fallback"
	}
	return "----=_Part_" + hex.EncodeToString(b[:])
}

// NewContentID erzeugt eine eindeutige Content-ID für einen Anhang.
func NewContentID(name string) string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	base := strings.TrimSuffix(filepath.Base(name), filepath.Ext(name))
	base = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, base)
	if base == "" {
		base = "part"
	}
	return fmt.Sprintf("%s-%s@soapmacui", base, hex.EncodeToString(b[:4]))
}

func guessContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".xml":
		return "application/xml"
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".txt":
		return "text/plain"
	case ".json":
		return "application/json"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gz":
		return "application/gzip"
	case ".csv":
		return "text/csv"
	default:
		return "application/octet-stream"
	}
}
