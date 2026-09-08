package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/apeters/soapmacui/internal/auth"
	"github.com/apeters/soapmacui/internal/httpx"
	"github.com/apeters/soapmacui/internal/project"
	"github.com/apeters/soapmacui/internal/soap"
)

// UserAgent identifiziert die Anwendung. Über einen eigenen Header im Request
// oder Endpoint lässt er sich überschreiben.
const UserAgent = "SoapMacUi/0.1"

// SendResult ist das vollständige Ergebnis eines Aufrufs für die UI.
type SendResult struct {
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
	Status     int    `json:"status"`
	Statustext string `json:"statusText"`

	Envelope    string                    `json:"envelope"`
	Pretty      string                    `json:"pretty"`
	Headers     map[string][]string       `json:"headers"`
	Attachments []soap.ReceivedAttachment `json:"attachments"`
	Fault       *soap.Fault               `json:"fault"`

	RawRequest   string `json:"rawRequest"`
	RawResponse  string `json:"rawResponse"`
	RawTruncated bool   `json:"rawTruncated"`

	Timing httpx.Timing   `json:"timing"`
	TLS    *httpx.TLSInfo `json:"tls"`

	Size       int64  `json:"size"`
	BodyPath   string `json:"bodyPath"`
	SentAt     string `json:"sentAt"`
	EndpointID string `json:"endpointId"`
	Spilled    bool   `json:"spilled"`

	Warnings []string `json:"warnings"`
}

// send führt einen Aufruf vollständig aus.
func (a *App) send(ctx context.Context, p *project.Project, r *project.Request, op *project.Operation, ep *project.Endpoint) *SendResult {
	res := &SendResult{EndpointID: ep.ID, SentAt: time.Now().Format(time.RFC3339)}

	wire := r.EffectiveWire(ep)
	mtom := r.EffectiveMTOM(ep)
	ac := r.EffectiveAuth(ep)
	ho := r.EffectiveHTTP(ep)

	vars := mergeVars(p, ep)
	body := substitute(r.Body, vars)
	url := substitute(ep.URL, vars)

	// WS-Security greift in den Envelope ein — bewusst als letzter Schritt vor
	// dem Kodieren, damit alles davor byte-genau bleibt.
	if ac.Kind == auth.KindWSS {
		pw, err := a.secretFor(ac)
		if err != nil {
			res.Error = fmt.Sprintf("Passwort für WS-Security nicht verfügbar: %v", err)
			return res
		}
		injected, err := auth.InjectUsernameToken(body, ac, pw)
		if err != nil {
			res.Error = fmt.Sprintf("WS-Security-Header einfügen: %v", err)
			return res
		}
		body = injected
	}

	encoded, err := soap.EncodeBody(body, wire)
	if err != nil {
		res.Error = err.Error()
		return res
	}

	version := op.Version
	if version == "" {
		version = "1.1"
	}

	req := httpx.Request{
		Method:    http.MethodPost,
		URL:       url,
		Chunked:   wire.Chunked,
		Expect100: wire.Expect100,
	}

	contentType := soap.ContentType(version, wire)
	var previewBody string

	switch mtom.Mode {
	case soap.AttachMTOM, soap.AttachSwA:
		// Nur Anhänge mitsenden, auf die der Body auch verweist. Bei XOP muss
		// jeder Part über ein xop:Include erreichbar sein; unreferenzierte
		// Parts sind nach Spezifikation falsch und werden von strengen
		// Servern abgelehnt.
		atts := make([]soap.Attachment, 0, len(r.Attachments))
		var skipped, rejected []string
		for _, at := range r.Attachments {
			if !bytes.Contains(encoded, []byte("cid:"+at.ID)) {
				skipped = append(skipped, at.Name)
				continue
			}
			if err := attachmentSource(p, at); err != nil {
				rejected = append(rejected, fmt.Sprintf("%s (%v)", at.Name, err))
				continue
			}
			atts = append(atts, at)
		}
		if len(skipped) > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"Nicht mitgesendet, weil im Body kein Verweis darauf steht: %s",
				strings.Join(skipped, ", ")))
		}
		if len(rejected) > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"Nicht mitgesendet, weil die Quelle ausserhalb des Projekts liegt: %s",
				strings.Join(rejected, ", ")))
		}
		if len(atts) == 0 && len(r.Attachments) > 0 {
			res.Warnings = append(res.Warnings,
				"Kein einziger Anhang ist im Body referenziert — die Nachricht geht ohne Anhänge raus.")
		}
		mp, err := soap.BuildMultipart(encoded, atts, mtom, version)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		contentType = mp.ContentType
		req.BodyLen = mp.Length
		req.BodyWrite = func(w io.Writer) (int64, error) { return mp.WriteTo(w) }
		previewBody = mp.Preview(64 << 10)
	default:
		req.Body = encoded
		req.BodyLen = int64(len(encoded))
		previewBody = string(encoded)
	}
	_ = previewBody

	// Header in kontrollierter Reihenfolge und Originalschreibweise.
	req.Headers = append(req.Headers, httpx.Header{Name: "Content-Type", Value: contentType})
	if name, value, ok := soap.SOAPActionHeader(version, op.SOAPAction, wire); ok {
		req.Headers = append(req.Headers, httpx.Header{Name: name, Value: value})
	}
	if version == "1.2" && op.SOAPAction != "" && wire.SOAPAction != soap.ActionOmit {
		// Bei SOAP 1.2 gehört die Action als Content-Type-Parameter dazu.
		req.Headers[0].Value += fmt.Sprintf(`; action="%s"`, op.SOAPAction)
	}
	if wire.AcceptGzip {
		req.Headers = append(req.Headers, httpx.Header{Name: "Accept-Encoding", Value: "gzip, deflate"})
	}
	req.Headers = append(req.Headers, httpx.Header{Name: "Accept", Value: "*/*"})
	req.Headers = append(req.Headers, httpx.Header{Name: "User-Agent", Value: UserAgent})

	if ac.Kind == auth.KindBasic {
		pw, err := a.secretFor(ac)
		if err != nil {
			res.Error = fmt.Sprintf("Passwort für Basic Auth nicht verfügbar: %v", err)
			return res
		}
		h := http.Header{}
		auth.ApplyHTTP(h, ac, pw)
		req.Headers = append(req.Headers, httpx.Header{Name: "Authorization", Value: h.Get("Authorization")})
	}

	for _, kv := range ep.Headers {
		if kv.Enabled && kv.Name != "" {
			req.Headers = append(req.Headers, httpx.Header{Name: kv.Name, Value: substitute(kv.Value, vars)})
		}
	}
	for _, kv := range r.Headers {
		if kv.Enabled && kv.Name != "" {
			req.Headers = append(req.Headers, httpx.Header{Name: kv.Name, Value: substitute(kv.Value, vars)})
		}
	}

	ho.SpillDir = filepath.Join(a.paths.Cache, "responses")
	_ = os.MkdirAll(ho.SpillDir, 0o755)

	client := httpx.New(ho)
	resp, err := client.Do(ctx, req)
	if resp != nil {
		res.RawRequest = string(resp.RawRequest)
		res.RawResponse = string(resp.RawResponse)
		res.RawTruncated = resp.RawTruncated
		res.Timing, res.TLS = resp.Timing, resp.TLS
	}
	if err != nil {
		res.Error = err.Error()
		return res
	}

	res.Status, res.Statustext = resp.Status, resp.StatusText
	res.Headers, res.Size, res.BodyPath = resp.Headers, resp.Size, resp.BodyPath
	res.Spilled = resp.BodyPath != ""

	raw := resp.Body
	if res.Spilled {
		// Ausgelagerte Antwort: nur den Kopf einlesen, der Rest bleibt liegen.
		f, ferr := os.Open(resp.BodyPath)
		if ferr == nil {
			raw, _ = io.ReadAll(io.LimitReader(f, 1<<20))
			f.Close()
			res.Warnings = append(res.Warnings, fmt.Sprintf(
				"Antwort ist %s gross und liegt unter %s — angezeigt wird das erste MiB.",
				humanSize(resp.Size), resp.BodyPath))
		}
	}
	raw = maybeGunzip(raw, resp.Headers.Get("Content-Encoding"), res)

	parsed, perr := soap.ParseResponse(resp.Headers.Get("Content-Type"),
		raw, filepath.Join(a.paths.Cache, "attachments"))
	if perr != nil {
		res.Warnings = append(res.Warnings, "Antwort zerlegen: "+perr.Error())
		res.Envelope = string(raw)
	} else {
		res.Envelope = string(parsed.Envelope)
		res.Attachments = parsed.Attachments
		res.Fault = parsed.Fault
		res.Warnings = append(res.Warnings, parsed.Warnings...)
	}

	if pretty, perr := soap.Reindent(res.Envelope, "  "); perr == nil {
		res.Pretty = pretty
	} else {
		res.Pretty = res.Envelope
	}

	res.OK = resp.Status >= 200 && resp.Status < 300 && res.Fault == nil
	return res
}

// maybeGunzip packt gzip-Antworten aus. DisableCompression im Transport sorgt
// dafür, dass wir das selbst tun — nur so bleibt der Mitschnitt echt.
func maybeGunzip(body []byte, encoding string, res *SendResult) []byte {
	if !strings.Contains(strings.ToLower(encoding), "gzip") || len(body) == 0 {
		return body
	}
	zr, err := gzipReader(bytes.NewReader(body))
	if err != nil {
		res.Warnings = append(res.Warnings, "gzip-Antwort nicht entpackbar: "+err.Error())
		return body
	}
	defer zr.Close()
	out, err := io.ReadAll(zr)
	if err != nil {
		res.Warnings = append(res.Warnings, "gzip-Antwort unvollständig: "+err.Error())
		return body
	}
	return out
}

func (a *App) secretFor(c auth.Config) (string, error) {
	if c.SecretRef == "" {
		return "", fmt.Errorf("kein Passwort hinterlegt")
	}
	return a.secrets.Get(c.SecretRef)
}

// attachDirName ist der Anhang-Ordner innerhalb eines Projekts.
const attachDirName = "attachments"

// inside sagt, ob path innerhalb von root liegt. Über filepath.Rel statt über
// einen Präfixvergleich, damit ".." zuverlässig auffällt.
func inside(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

// attachmentSource stellt sicher, dass ein Anhang wirklich aus dem Anhang-
// Ordner des Projekts stammt.
//
// Attachment.Path steht absolut in der Projektdatei. Geprüft wurde das bisher
// nur in AttachExisting, also im Weg über die Oberfläche — ein Eintrag, der
// bereits in einer weitergereichten project.json steht, ging ungeprüft ans
// Netz. Angezeigt wird dabei Name, gelesen wird Path; die beiden dürfen
// auseinanderfallen, ein harmlos benanntes "Logo.png" also auf ~/.ssh/id_rsa
// zeigen.
func attachmentSource(p *project.Project, at soap.Attachment) error {
	if at.Path == "" {
		// Aus einem Script erzeugt: liegt im Speicher, nie auf der Platte.
		if len(at.Inline) == 0 {
			return fmt.Errorf("weder Datei noch Inhalt")
		}
		return nil
	}
	dir := filepath.Join(p.Dir, attachDirName)
	if !inside(dir, at.Path) {
		return fmt.Errorf("liegt nicht im Anhang-Ordner")
	}
	// Ein Symlink im Anhang-Ordner käme sonst an der Prüfung vorbei. Verglichen
	// wird gegen den ebenfalls aufgelösten Ordner — sonst scheitert die Prüfung
	// schon daran, dass macOS Pfade wie /tmp selbst über einen Symlink führt.
	real, err := filepath.EvalSymlinks(at.Path)
	if err != nil {
		return fmt.Errorf("nicht lesbar")
	}
	dirReal, err := filepath.EvalSymlinks(dir)
	if err != nil {
		dirReal = dir
	}
	if !inside(dirReal, real) {
		return fmt.Errorf("verweist aus dem Anhang-Ordner heraus")
	}
	return nil
}

// mergeVars legt Projekt- und Endpoint-Variablen übereinander.
func mergeVars(p *project.Project, ep *project.Endpoint) map[string]string {
	out := map[string]string{}
	for k, v := range p.Variables {
		out[k] = v
	}
	if ep != nil {
		for k, v := range ep.Vars {
			out[k] = v
		}
	}
	return out
}

// substitute ersetzt ${name} durch den Variablenwert. Unbekannte Namen bleiben
// unverändert stehen, damit man im Request sieht, was fehlt.
func substitute(s string, vars map[string]string) string {
	if len(vars) == 0 || !strings.Contains(s, "${") {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '{' {
			if end := strings.IndexByte(s[i+2:], '}'); end >= 0 {
				name := s[i+2 : i+2+end]
				if v, ok := vars[name]; ok {
					sb.WriteString(v)
					i += 2 + end + 1
					continue
				}
			}
		}
		sb.WriteByte(s[i])
		i++
	}
	return sb.String()
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func gzipReader(r io.Reader) (io.ReadCloser, error) { return gzip.NewReader(r) }
