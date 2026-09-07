// Package auth kapselt HTTP- und WS-Security-Authentifizierung.
//
// Passwörter stehen nie in der Konfiguration: gespeichert wird nur ein
// Schlüssel, unter dem der Wert im Schlüsselbund des Betriebssystems liegt.
package auth

import (
	"encoding/base64"
	"net/http"
	"strings"
)

// Kind unterscheidet die Verfahren.
type Kind string

const (
	KindNone  Kind = "none"
	KindBasic Kind = "basic"
	KindWSS   Kind = "wss"
)

// PasswordType für WS-Security UsernameToken.
const (
	PasswordText   = "PasswordText"
	PasswordDigest = "PasswordDigest"
)

// Config beschreibt die Authentifizierung eines Endpoints oder Requests.
// Alle Felder werden explizit persistiert, auch bei Default-Werten.
type Config struct {
	Kind     Kind   `json:"kind"`
	Username string `json:"username"`

	// SecretRef verweist auf den Eintrag im Schlüsselbund. Das Passwort
	// selbst wird niemals serialisiert.
	SecretRef string `json:"secretRef"`

	// WS-Security
	PasswordType   string `json:"passwordType"`
	AddNonce       bool   `json:"addNonce"`
	AddCreated     bool   `json:"addCreated"`
	AddTimestamp   bool   `json:"addTimestamp"`
	TimestampTTL   int    `json:"timestampTtlSeconds"`
	MustUnderstand bool   `json:"mustUnderstand"`
	// ActorRole setzt soapenv:actor bzw. role am Security-Header.
	ActorRole string `json:"actorRole"`
}

// DefaultConfig liefert eine leere Konfiguration mit sinnvollen WSS-Vorgaben.
func DefaultConfig() Config {
	return Config{
		Kind:           KindNone,
		PasswordType:   PasswordText,
		AddNonce:       true,
		AddCreated:     true,
		AddTimestamp:   false,
		TimestampTTL:   300,
		MustUnderstand: true,
	}
}

// ApplyHTTP setzt die HTTP-Header, die sich aus der Konfiguration ergeben.
func ApplyHTTP(h http.Header, c Config, password string) {
	if c.Kind != KindBasic {
		return
	}
	token := base64.StdEncoding.EncodeToString([]byte(c.Username + ":" + password))
	h.Set("Authorization", "Basic "+token)
}

// NeedsPassword sagt, ob für diese Konfiguration ein Geheimnis gebraucht wird.
func (c Config) NeedsPassword() bool {
	return c.Kind == KindBasic || c.Kind == KindWSS
}

// Describe liefert eine kurze Beschreibung für die UI.
func (c Config) Describe() string {
	switch c.Kind {
	case KindBasic:
		return "HTTP Basic (" + c.Username + ")"
	case KindWSS:
		t := c.PasswordType
		if t == "" {
			t = PasswordText
		}
		return "WS-Security " + strings.TrimPrefix(t, "Password") + " (" + c.Username + ")"
	default:
		return "keine"
	}
}
