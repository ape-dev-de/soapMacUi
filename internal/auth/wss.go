package auth

import (
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// WS-Security-Namespaces (OASIS 1.0).
const (
	NSWSSE = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"
	NSWSU  = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"

	nsUTProfile = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0"
	nsMsgSec    = "http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0"

	typeDigest = nsUTProfile + "#PasswordDigest"
	typeText   = nsUTProfile + "#PasswordText"
	typeNonce  = nsMsgSec + "#Base64Binary"
)

// headerOpen findet das öffnende Header-Element eines Envelopes, mit oder ohne
// Präfix, selbstschliessend oder nicht.
var (
	headerSelfClosing = regexp.MustCompile(`<([A-Za-z_][\w.-]*:)?Header(\s[^>]*?)?/>`)
	headerOpen        = regexp.MustCompile(`<([A-Za-z_][\w.-]*:)?Header(\s[^>]*?)?>`)
	bodyOpen          = regexp.MustCompile(`<([A-Za-z_][\w.-]*:)?Body(\s[^>]*?)?[/>]`)
	envelopeOpen      = regexp.MustCompile(`<([A-Za-z_][\w.-]*:)?Envelope(\s[^>]*?)?>`)
)

// InjectUsernameToken fügt einen WS-Security-Header in den Envelope ein.
//
// Der Eingriff passiert bewusst als Textoperation direkt vor dem Senden und ist
// in der Raw-Wire-Ansicht sichtbar: der Rest des Bodys bleibt byte-genau so,
// wie er im Editor steht.
func InjectUsernameToken(envelope string, c Config, password string) (string, error) {
	if c.Kind != KindWSS {
		return envelope, nil
	}
	sec, err := buildSecurityHeader(c, password, envelopePrefix(envelope))
	if err != nil {
		return "", err
	}

	if loc := headerSelfClosing.FindStringSubmatchIndex(envelope); loc != nil {
		prefix := envelope[loc[2]:loc[3]]
		open := fmt.Sprintf("<%sHeader>", prefix)
		closeTag := fmt.Sprintf("</%sHeader>", prefix)
		return envelope[:loc[0]] + open + "\n" + sec + "\n  " + closeTag + envelope[loc[1]:], nil
	}
	if loc := headerOpen.FindStringIndex(envelope); loc != nil {
		return envelope[:loc[1]] + "\n" + sec + envelope[loc[1]:], nil
	}
	// Kein Header vorhanden: einen vor dem Body einziehen.
	if loc := bodyOpen.FindStringIndex(envelope); loc != nil {
		prefix := envelopePrefix(envelope)
		hdr := fmt.Sprintf("  <%sHeader>\n%s\n  </%sHeader>\n  ", prefix, sec, prefix)
		return envelope[:loc[0]] + hdr + envelope[loc[0]:], nil
	}
	return "", fmt.Errorf("kein SOAP-Header und kein Body gefunden — ist das ein gültiger Envelope?")
}

// envelopePrefix ermittelt das Präfix des Envelope-Elements, damit der
// eingefügte Header zum Dokument passt.
func envelopePrefix(envelope string) string {
	if m := envelopeOpen.FindStringSubmatch(envelope); m != nil {
		return m[1]
	}
	return "soapenv:"
}

func buildSecurityHeader(c Config, password, envPrefix string) (string, error) {
	created := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	pwType := c.PasswordType
	if pwType == "" {
		pwType = PasswordText
	}

	var nonceB64 string
	if c.AddNonce || pwType == PasswordDigest {
		var n [16]byte
		if _, err := rand.Read(n[:]); err != nil {
			return "", fmt.Errorf("nonce erzeugen: %w", err)
		}
		nonceB64 = base64.StdEncoding.EncodeToString(n[:])
	}

	pwValue := password
	pwTypeURI := typeText
	if pwType == PasswordDigest {
		// Digest = Base64(SHA1(nonce + created + password)), Nonce als Rohbytes.
		nonceRaw, err := base64.StdEncoding.DecodeString(nonceB64)
		if err != nil {
			return "", fmt.Errorf("nonce dekodieren: %w", err)
		}
		h := sha1.New()
		h.Write(nonceRaw)
		h.Write([]byte(created))
		h.Write([]byte(password))
		pwValue = base64.StdEncoding.EncodeToString(h.Sum(nil))
		pwTypeURI = typeDigest
	}

	var sb strings.Builder
	attrs := fmt.Sprintf(` xmlns:wsse=%q xmlns:wsu=%q`, NSWSSE, NSWSU)
	if c.MustUnderstand {
		attrs += fmt.Sprintf(` %smustUnderstand="1"`, envPrefix)
	}
	if c.ActorRole != "" {
		role := "actor"
		if envPrefix != "" && strings.Contains(envPrefix, "12") {
			role = "role"
		}
		attrs += fmt.Sprintf(` %s%s=%q`, envPrefix, role, c.ActorRole)
	}
	sb.WriteString("    <wsse:Security" + attrs + ">\n")

	if c.AddTimestamp {
		ttl := c.TimestampTTL
		if ttl <= 0 {
			ttl = 300
		}
		expires := time.Now().UTC().Add(time.Duration(ttl) * time.Second).Format("2006-01-02T15:04:05.000Z")
		sb.WriteString("      <wsu:Timestamp>\n")
		sb.WriteString("        <wsu:Created>" + created + "</wsu:Created>\n")
		sb.WriteString("        <wsu:Expires>" + expires + "</wsu:Expires>\n")
		sb.WriteString("      </wsu:Timestamp>\n")
	}

	sb.WriteString("      <wsse:UsernameToken>\n")
	sb.WriteString("        <wsse:Username>" + escapeXML(c.Username) + "</wsse:Username>\n")
	sb.WriteString(fmt.Sprintf("        <wsse:Password Type=%q>%s</wsse:Password>\n", pwTypeURI, escapeXML(pwValue)))
	if c.AddNonce {
		sb.WriteString(fmt.Sprintf("        <wsse:Nonce EncodingType=%q>%s</wsse:Nonce>\n", typeNonce, nonceB64))
	}
	if c.AddCreated {
		sb.WriteString("        <wsu:Created>" + created + "</wsu:Created>\n")
	}
	sb.WriteString("      </wsse:UsernameToken>\n")
	sb.WriteString("    </wsse:Security>")
	return sb.String(), nil
}

func escapeXML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}
