package httpx

import (
	"bytes"
	"crypto/tls"
	"net"
	"sync"
	"time"
)

// capture schneidet die Bytes einer Verbindung mit.
//
// Der Mitschnitt sitzt bewusst unterhalb von net/http und oberhalb von TLS:
// so steht in der Raw-Ansicht genau das, was der Server zu sehen bekommt —
// inklusive der Kopfzeilen, die net/http selbst ergänzt.
type capture struct {
	mu    sync.Mutex
	limit int

	sent bytes.Buffer
	recv bytes.Buffer
	cut  bool

	state    *tls.ConnectionState
	verified bool
}

func (c *capture) wrap(conn net.Conn) net.Conn { return &capturedConn{Conn: conn, cap: c} }

func (c *capture) setTLS(st tls.ConnectionState, verified bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state, c.verified = &st, verified
}

func (c *capture) write(buf *bytes.Buffer, p []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	room := c.limit - buf.Len()
	if room <= 0 {
		c.cut = true
		return
	}
	if len(p) > room {
		buf.Write(p[:room])
		c.cut = true
		return
	}
	buf.Write(p)
}

func (c *capture) sentBytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.sent.Bytes()...)
}

func (c *capture) recvBytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]byte(nil), c.recv.Bytes()...)
}

func (c *capture) truncated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cut
}

func (c *capture) tlsInfo() *TLSInfo {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state == nil {
		return nil
	}
	info := &TLSInfo{
		Version:     tlsVersionName(c.state.Version),
		CipherSuite: tls.CipherSuiteName(c.state.CipherSuite),
		Verified:    c.verified,
	}
	if len(c.state.PeerCertificates) > 0 {
		leaf := c.state.PeerCertificates[0]
		info.Subject = leaf.Subject.String()
		info.Issuer = leaf.Issuer.String()
		info.NotAfter = leaf.NotAfter.Format(time.RFC3339)
		info.DNSNames = leaf.DNSNames
	}
	return info
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return "unbekannt"
	}
}

// capturedConn kopiert alles, was gelesen und geschrieben wird, in den Mitschnitt.
type capturedConn struct {
	net.Conn
	cap *capture
}

func (c *capturedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.cap.write(&c.cap.recv, p[:n])
	}
	return n, err
}

func (c *capturedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.cap.write(&c.cap.sent, p[:n])
	}
	return n, err
}
