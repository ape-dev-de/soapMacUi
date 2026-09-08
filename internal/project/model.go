package project

import (
	"time"

	"github.com/apeters/soapmacui/internal/auth"
	"github.com/apeters/soapmacui/internal/httpx"
	"github.com/apeters/soapmacui/internal/soap"
)

// SchemaVersion ist die Version des Projektformats. Bei Änderungen wird
// migriert, nie stillschweigend anders interpretiert.
const SchemaVersion = 1

// KV ist ein benutzerdefiniertes Kopffeld.
type KV struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	Enabled bool   `json:"enabled"`
}

// Project ist ein Projekt.
type Project struct {
	SchemaVersion int       `json:"schemaVersion"`
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Created       time.Time `json:"created"`
	Modified      time.Time `json:"modified"`

	Interfaces []*Interface `json:"interfaces"`
	Endpoints  []*Endpoint  `json:"endpoints"`

	// Color ist ein Schlüssel aus der Palette (siehe Colors), kein Farbwert.
	// Projektdateien sind fremder Inhalt; ein freier Wert landete sonst
	// ungeprüft in einem style-Attribut der Oberfläche.
	Color string `json:"color"`

	// Variables gelten projektweit und werden von Endpoint-Werten überlagert.
	Variables map[string]string `json:"variables"`

	// Dir ist der Ordner auf der Platte und wird nicht serialisiert.
	Dir string `json:"-"`
}

// Endpoint ist ein Zielsystem samt allem, was den Aufruf dorthin bestimmt.
type Endpoint struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`

	Auth    auth.Config       `json:"auth"`
	Wire    soap.WireOptions  `json:"wire"`
	MTOM    soap.MTOMOptions  `json:"mtom"`
	HTTP    httpx.Options     `json:"http"`
	Headers []KV              `json:"headers"`
	Vars    map[string]string `json:"variables"`
}

// NewEndpoint erzeugt einen Endpoint mit vollständigen Vorgaben.
// Alle Optionen werden explizit gesetzt, damit nichts implizit wandert.
func NewEndpoint(id, name, url string) *Endpoint {
	return &Endpoint{
		ID:   id,
		Name: name,
		URL:  url,
		Auth: auth.DefaultConfig(),
		Wire: soap.DefaultWireOptions(),
		MTOM: soap.DefaultMTOMOptions(),
		HTTP: httpx.DefaultOptions(),
		Vars: map[string]string{},
	}
}

// Interface ist eine aus einem WSDL abgeleitete Schnittstelle.
type Interface struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	WSDLURL  string    `json:"wsdlUrl"`
	LoadedAt time.Time `json:"loadedAt"`

	TargetNS   string   `json:"targetNamespace"`
	CacheFiles []string `json:"cacheFiles"` // relativ zum Projektordner

	Operations []*Operation `json:"operations"`
}

// Operation ist eine aufrufbare Operation.
type Operation struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	Service string `json:"service"`
	Port    string `json:"port"`
	Binding string `json:"binding"`

	SOAPAction  string `json:"soapAction"`
	Style       string `json:"style"`
	Use         string `json:"use"`
	Version     string `json:"soapVersion"`
	Doc         string `json:"documentation"`
	SuggestMTOM bool   `json:"suggestMtom"`

	// WSDLEndpoint ist die im WSDL gefundene Adresse.
	WSDLEndpoint string `json:"wsdlEndpoint"`

	// Retired markiert eine Operation, die beim letzten Reindex nicht mehr im
	// WSDL stand. Sie bleibt samt ihrer Requests erhalten — gelöscht wird nur
	// auf ausdrückliche Anweisung.
	Retired bool `json:"retired,omitempty"`

	Requests []*Request `json:"requests"`
}

// Request ist ein gespeicherter Aufruf.
//
// Overrides sind Zeiger: nil bedeutet "vom Endpoint erben". Beim Speichern
// werden gesetzte Werte immer vollständig geschrieben, auch wenn sie dem
// aktuellen Standard entsprechen — sonst verhielte sich ein altes Projekt
// nach einem Update anders.
type Request struct {
	ID   string `json:"id"`
	Name string `json:"name"`

	// BodyFile ist der relative Pfad der Body-Datei im Projektordner.
	BodyFile string `json:"bodyFile"`

	Headers     []KV              `json:"headers"`
	Attachments []soap.Attachment `json:"attachments"`
	Auth        *auth.Config      `json:"auth,omitempty"`
	Wire        *soap.WireOptions `json:"wire,omitempty"`
	MTOM        *soap.MTOMOptions `json:"mtom,omitempty"`
	HTTP        *httpx.Options    `json:"http,omitempty"`

	PreScript  string `json:"preScript"`  // relativer Pfad, leer = keiner
	PostScript string `json:"postScript"` // relativer Pfad, leer = keiner

	Modified time.Time `json:"modified"`

	// Body wird nicht serialisiert; er lebt in BodyFile.
	Body string `json:"-"`
}

// EffectiveWire liefert die Wire-Optionen unter Berücksichtigung des Endpoints.
func (r *Request) EffectiveWire(ep *Endpoint) soap.WireOptions {
	if r.Wire != nil {
		return *r.Wire
	}
	if ep != nil {
		return ep.Wire
	}
	return soap.DefaultWireOptions()
}

// EffectiveMTOM liefert die MTOM-Optionen unter Berücksichtigung des Endpoints.
func (r *Request) EffectiveMTOM(ep *Endpoint) soap.MTOMOptions {
	if r.MTOM != nil {
		return *r.MTOM
	}
	if ep != nil {
		return ep.MTOM
	}
	return soap.DefaultMTOMOptions()
}

// EffectiveAuth liefert die Auth-Konfiguration unter Berücksichtigung des Endpoints.
func (r *Request) EffectiveAuth(ep *Endpoint) auth.Config {
	if r.Auth != nil {
		return *r.Auth
	}
	if ep != nil {
		return ep.Auth
	}
	return auth.DefaultConfig()
}

// EffectiveHTTP liefert die Transportoptionen unter Berücksichtigung des Endpoints.
func (r *Request) EffectiveHTTP(ep *Endpoint) httpx.Options {
	if r.HTTP != nil {
		return *r.HTTP
	}
	if ep != nil {
		return ep.HTTP
	}
	return httpx.DefaultOptions()
}

// FindEndpoint sucht einen Endpoint anhand seiner ID.
func (p *Project) FindEndpoint(id string) *Endpoint {
	for _, e := range p.Endpoints {
		if e.ID == id {
			return e
		}
	}
	return nil
}

// FindRequest sucht einen Request samt zugehöriger Operation.
func (p *Project) FindRequest(id string) (*Request, *Operation, *Interface) {
	for _, itf := range p.Interfaces {
		for _, op := range itf.Operations {
			for _, r := range op.Requests {
				if r.ID == id {
					return r, op, itf
				}
			}
		}
	}
	return nil, nil, nil
}

// Colors ist die Palette für die Projektkennzeichnung. Gespeichert wird nur
// der Schlüssel; die Farbwerte selbst stehen im Stylesheet. Damit kann eine
// weitergereichte Projektdatei keinen freien Wert in die Oberfläche tragen.
var Colors = []string{"purple", "blue", "teal", "green", "amber", "orange", "red", "slate"}

// ValidColor prüft einen Palettenschlüssel. Leer heisst "keine Farbe gesetzt"
// und ist gültig — die Oberfläche zeigt dann die Vorgabe.
func ValidColor(c string) bool {
	if c == "" {
		return true
	}
	for _, v := range Colors {
		if v == c {
			return true
		}
	}
	return false
}
