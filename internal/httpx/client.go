// Package httpx sendet die fertigen SOAP-Nachrichten und schneidet dabei
// mit, was tatsächlich über die Leitung geht.
package httpx

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// Options steuert Transport, TLS und Proxy.
type Options struct {
	Timeout            time.Duration `json:"timeoutNanos"`
	InsecureSkipVerify bool          `json:"insecureSkipVerify"`
	FollowRedirects    bool          `json:"followRedirects"`
	Proxy              string        `json:"proxy"`
	ProxyUser          string        `json:"proxyUser"`
	ProxySecretRef     string        `json:"proxySecretRef"`

	// ClientCertPath ist ein PKCS#12-Keystore für mTLS.
	ClientCertPath      string `json:"clientCertPath"`
	ClientCertSecretRef string `json:"clientCertSecretRef"`

	// CACertPath erlaubt ein eigenes Wurzelzertifikat statt InsecureSkipVerify.
	CACertPath string `json:"caCertPath"`

	// MaxRawCapture begrenzt den Mitschnitt je Richtung. 0 => 256 KiB.
	MaxRawCapture int `json:"maxRawCapture"`
	// SpillThreshold: grössere Antworten landen auf der Platte statt im RAM.
	SpillThreshold int64 `json:"spillThreshold"`
	// SpillDir ist das Verzeichnis für ausgelagerte Antworten.
	SpillDir string `json:"-"`
}

// DefaultOptions liefert brauchbare Vorgaben.
func DefaultOptions() Options {
	return Options{
		Timeout:         60 * time.Second,
		FollowRedirects: false,
		MaxRawCapture:   256 << 10,
		SpillThreshold:  1 << 20,
	}
}

// Request ist eine sendefertige Nachricht.
type Request struct {
	Method string
	URL    string

	// Headers wird bewusst als geordnete Liste geführt, damit Schreibweise
	// und Reihenfolge erhalten bleiben. net/http würde "SOAPAction" sonst zu
	// "Soapaction" normalisieren, was manche Server ablehnen.
	Headers []Header

	Body      []byte
	BodyLen   int64
	BodyWrite func(io.Writer) (int64, error) // für gestreamte Multipart-Körper

	Chunked   bool
	Expect100 bool
}

// Header ist ein einzelnes Kopffeld in Originalschreibweise.
type Header struct {
	Name  string
	Value string
}

// Timing hält die Zeitmessung eines Aufrufs.
type Timing struct {
	DNSMillis     float64 `json:"dnsMs"`
	ConnectMillis float64 `json:"connectMs"`
	TLSMillis     float64 `json:"tlsMs"`
	TTFBMillis    float64 `json:"ttfbMs"`
	TotalMillis   float64 `json:"totalMs"`
}

// TLSInfo beschreibt die ausgehandelte Verbindung.
type TLSInfo struct {
	Version     string   `json:"version"`
	CipherSuite string   `json:"cipherSuite"`
	Subject     string   `json:"subject"`
	Issuer      string   `json:"issuer"`
	NotAfter    string   `json:"notAfter"`
	DNSNames    []string `json:"dnsNames"`
	Verified    bool     `json:"verified"`
}

// Response ist das Ergebnis eines Aufrufs.
type Response struct {
	Status     int         `json:"status"`
	StatusText string      `json:"statusText"`
	Headers    http.Header `json:"headers"`

	// Body hält kleine Antworten. Grössere liegen unter BodyPath.
	Body     []byte `json:"-"`
	BodyPath string `json:"bodyPath"`
	Size     int64  `json:"size"`

	RawRequest   []byte `json:"-"`
	RawResponse  []byte `json:"-"`
	RawTruncated bool   `json:"rawTruncated"`

	Timing Timing   `json:"timing"`
	TLS    *TLSInfo `json:"tls"`
}

// Client sendet Requests.
type Client struct {
	Opts Options
}

// New erzeugt einen Client.
func New(o Options) *Client {
	if o.MaxRawCapture == 0 {
		o.MaxRawCapture = 256 << 10
	}
	if o.SpillThreshold == 0 {
		o.SpillThreshold = 1 << 20
	}
	if o.Timeout == 0 {
		o.Timeout = 60 * time.Second
	}
	return &Client{Opts: o}
}

// Do sendet den Request und liefert die Antwort samt Mitschnitt.
func (c *Client) Do(ctx context.Context, r Request) (*Response, error) {
	cap := &capture{limit: c.Opts.MaxRawCapture}

	tlsConf, err := c.tlsConfig()
	if err != nil {
		return nil, err
	}

	tr := &http.Transport{
		DisableCompression:  true, // gzip steuern wir über den Accept-Encoding-Header
		MaxIdleConns:        4,
		IdleConnTimeout:     30 * time.Second,
		TLSHandshakeTimeout: 20 * time.Second,
		ForceAttemptHTTP2:   false, // SOAP-Stacks sprechen HTTP/1.1; das hält den Mitschnitt lesbar
	}
	if c.Opts.Proxy != "" {
		pu, err := url.Parse(c.Opts.Proxy)
		if err != nil {
			return nil, fmt.Errorf("proxy-URL %q: %w", c.Opts.Proxy, err)
		}
		if c.Opts.ProxyUser != "" {
			pu.User = url.UserPassword(c.Opts.ProxyUser, c.Opts.ProxySecretRef)
		}
		tr.Proxy = http.ProxyURL(pu)
	}

	dialer := &net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return cap.wrap(conn), nil
	}
	// Bei TLS erst nach dem Handshake mitschneiden — sonst hätten wir
	// verschlüsselten Datenmüll statt lesbarem HTTP.
	tr.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		raw, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		host, _, _ := net.SplitHostPort(addr)
		conf := tlsConf.Clone()
		conf.ServerName = host
		tc := tls.Client(raw, conf)
		if err := tc.HandshakeContext(ctx); err != nil {
			raw.Close()
			return nil, err
		}
		cap.setTLS(tc.ConnectionState(), !c.Opts.InsecureSkipVerify)
		return cap.wrap(tc), nil
	}

	client := &http.Client{Transport: tr, Timeout: c.Opts.Timeout}
	if !c.Opts.FollowRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	defer tr.CloseIdleConnections()

	var bodyReader io.Reader
	switch {
	case r.BodyWrite != nil:
		pr, pw := io.Pipe()
		go func() {
			_, err := r.BodyWrite(pw)
			pw.CloseWithError(err)
		}()
		bodyReader = pr
	case len(r.Body) > 0:
		bodyReader = bytes.NewReader(r.Body)
	}

	method := r.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, r.URL, bodyReader)
	if err != nil {
		return nil, err
	}

	// Header direkt in die Map schreiben, nicht über Set — nur so bleibt die
	// Schreibweise erhalten.
	for _, h := range r.Headers {
		switch strings.ToLower(h.Name) {
		case "host":
			req.Host = h.Value
		case "content-length":
			continue // von net/http verwaltet
		default:
			req.Header[h.Name] = append(req.Header[h.Name], h.Value)
		}
	}
	if r.Chunked {
		req.ContentLength = -1
		req.TransferEncoding = []string{"chunked"}
	} else if r.BodyLen > 0 {
		req.ContentLength = r.BodyLen
	} else if len(r.Body) > 0 {
		req.ContentLength = int64(len(r.Body))
	}
	if r.Expect100 {
		req.Header["Expect"] = []string{"100-continue"}
	}

	var t timings
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), t.trace()))

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		// Auch im Fehlerfall den Mitschnitt zurückgeben — bei einem
		// abweisenden Server ist genau das die Information, die zählt.
		return &Response{
			RawRequest:   cap.sentBytes(),
			RawResponse:  cap.recvBytes(),
			RawTruncated: cap.truncated(),
			Timing:       t.result(start),
			TLS:          cap.tlsInfo(),
		}, err
	}
	defer resp.Body.Close()

	out := &Response{
		Status:     resp.StatusCode,
		StatusText: resp.Status,
		Headers:    resp.Header,
		Timing:     t.result(start),
		TLS:        cap.tlsInfo(),
	}

	body, path, size, err := c.readBody(resp.Body)
	if err != nil {
		return out, fmt.Errorf("antwort lesen: %w", err)
	}
	out.Body, out.BodyPath, out.Size = body, path, size
	out.Timing.TotalMillis = float64(time.Since(start).Microseconds()) / 1000
	out.RawRequest, out.RawResponse, out.RawTruncated = cap.sentBytes(), cap.recvBytes(), cap.truncated()
	return out, nil
}

// readBody hält kleine Antworten im Speicher und lagert grosse auf die Platte
// aus — die Speicherregel aus dem Entwurf gilt auch hier.
func (c *Client) readBody(r io.Reader) ([]byte, string, int64, error) {
	head := make([]byte, c.Opts.SpillThreshold)
	n, err := io.ReadFull(r, head)
	switch err {
	case nil:
		// Es ist mehr da als die Schwelle: auf die Platte auslagern.
	case io.EOF, io.ErrUnexpectedEOF:
		return head[:n], "", int64(n), nil
	default:
		return nil, "", 0, err
	}

	dir := c.Opts.SpillDir
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.CreateTemp(dir, "response-*.bin")
	if err != nil {
		return nil, "", 0, err
	}
	defer f.Close()
	if _, err := f.Write(head[:n]); err != nil {
		return nil, "", 0, err
	}
	rest, err := io.Copy(f, r)
	if err != nil {
		return nil, "", 0, err
	}
	return nil, f.Name(), int64(n) + rest, nil
}

func (c *Client) tlsConfig() (*tls.Config, error) {
	conf := &tls.Config{
		InsecureSkipVerify: c.Opts.InsecureSkipVerify,
		MinVersion:         tls.VersionTLS10, // Altsysteme im Testnetz sprechen noch TLS 1.0
	}
	if c.Opts.CACertPath != "" {
		pem, err := os.ReadFile(c.Opts.CACertPath)
		if err != nil {
			return nil, fmt.Errorf("CA-Zertifikat lesen: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA-Zertifikat %s enthält kein gültiges PEM", c.Opts.CACertPath)
		}
		conf.RootCAs = pool
	}
	return conf, nil
}

// timings sammelt die Zwischenzeiten eines Aufrufs.
type timings struct {
	mu                         sync.Mutex
	dnsStart, connStart, tlsAt time.Time
	dns, conn, tlsDur, ttfb    time.Duration
	haveTTFB                   bool
	startedAt                  time.Time
}

func (t *timings) trace() *httptrace.ClientTrace {
	t.startedAt = time.Now()
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) {
			t.mu.Lock()
			t.dnsStart = time.Now()
			t.mu.Unlock()
		},
		DNSDone: func(httptrace.DNSDoneInfo) {
			t.mu.Lock()
			if !t.dnsStart.IsZero() {
				t.dns = time.Since(t.dnsStart)
			}
			t.mu.Unlock()
		},
		ConnectStart: func(string, string) {
			t.mu.Lock()
			t.connStart = time.Now()
			t.mu.Unlock()
		},
		ConnectDone: func(string, string, error) {
			t.mu.Lock()
			if !t.connStart.IsZero() {
				t.conn = time.Since(t.connStart)
			}
			t.mu.Unlock()
		},
		TLSHandshakeStart: func() {
			t.mu.Lock()
			t.tlsAt = time.Now()
			t.mu.Unlock()
		},
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			t.mu.Lock()
			if !t.tlsAt.IsZero() {
				t.tlsDur = time.Since(t.tlsAt)
			}
			t.mu.Unlock()
		},
		GotFirstResponseByte: func() {
			t.mu.Lock()
			t.ttfb, t.haveTTFB = time.Since(t.startedAt), true
			t.mu.Unlock()
		},
	}
}

func (t *timings) result(start time.Time) Timing {
	t.mu.Lock()
	defer t.mu.Unlock()
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
	out := Timing{
		DNSMillis:     ms(t.dns),
		ConnectMillis: ms(t.conn),
		TLSMillis:     ms(t.tlsDur),
		TotalMillis:   ms(time.Since(start)),
	}
	if t.haveTTFB {
		out.TTFBMillis = ms(t.ttfb)
	}
	return out
}
