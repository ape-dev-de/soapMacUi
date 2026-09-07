// Package wsdl liest WSDL 1.1 und macht daraus eine flache, UI-taugliche
// Operationsliste inklusive automatisch erkannter Endpoints.
package wsdl

import (
	"encoding/xml"

	"github.com/apeters/soapmacui/internal/xsd"
)

// Namespace-Konstanten der beteiligten Vokabulare.
const (
	NS       = "http://schemas.xmlsoap.org/wsdl/"
	NSSoap11 = "http://schemas.xmlsoap.org/wsdl/soap/"
	NSSoap12 = "http://schemas.xmlsoap.org/wsdl/soap12/"
	NSMime   = "http://schemas.xmlsoap.org/wsdl/mime/"
	NSHTTP   = "http://schemas.xmlsoap.org/wsdl/http/"

	EnvSoap11 = "http://schemas.xmlsoap.org/soap/envelope/"
	EnvSoap12 = "http://www.w3.org/2003/05/soap-envelope"
)

// SOAPVersion unterscheidet SOAP 1.1 und 1.2.
type SOAPVersion string

const (
	SOAP11 SOAPVersion = "1.1"
	SOAP12 SOAPVersion = "1.2"
)

// EnvelopeNS liefert den Envelope-Namespace zur Version.
func (v SOAPVersion) EnvelopeNS() string {
	if v == SOAP12 {
		return EnvSoap12
	}
	return EnvSoap11
}

// Part ist ein Message-Part, entweder element- oder typreferenziert.
type Part struct {
	Name    string
	Element xml.Name
	Type    xml.Name
}

// Message ist eine wsdl:message.
type Message struct {
	Name  xml.Name
	Parts []*Part
}

// OpMessage verweist aus einer Operation auf eine Message.
type OpMessage struct {
	Name    string
	Message xml.Name
}

// Operation ist eine Operation im abstrakten portType.
type Operation struct {
	Name   string
	Input  *OpMessage
	Output *OpMessage
	Faults []*OpMessage
	Doc    string
}

// PortType ist die abstrakte Schnittstelle.
type PortType struct {
	Name       xml.Name
	Operations []*Operation
}

// BodyBinding beschreibt soap:body.
type BodyBinding struct {
	Use       string // literal | encoded
	Parts     []string
	Namespace string
}

// HeaderBinding beschreibt soap:header.
type HeaderBinding struct {
	Message xml.Name
	Part    string
	Use     string
}

// BindingOperation ist die konkrete Bindung einer Operation.
type BindingOperation struct {
	Name       string
	SOAPAction string
	Style      string // document | rpc, sonst vom Binding geerbt
	Input      *BodyBinding
	Output     *BodyBinding
	InHeaders  []*HeaderBinding
	OutHeaders []*HeaderBinding
	// MTOM-Hinweise aus mime:multipartRelated
	InputAttachments []string
}

// Binding ist eine wsdl:binding.
type Binding struct {
	Name        xml.Name
	PortType    xml.Name
	Version     SOAPVersion
	Style       string
	Transport   string
	Operations  map[string]*BindingOperation
	IsSOAP      bool
	PolicyMTOM  bool // aus wsp:Policy / wsoma:OptimizedMimeSerialization
	PolicyNames []string
}

// Port ist ein konkreter Zugangspunkt.
type Port struct {
	Name    string
	Binding xml.Name
	Address string
	Version SOAPVersion
}

// Service ist eine wsdl:service.
type Service struct {
	Name  xml.Name
	Ports []*Port
	Doc   string
}

// Definitions ist das eingelesene WSDL samt aufgelöster Schemata.
type Definitions struct {
	TargetNS string
	Name     string
	Doc      string

	Messages  map[xml.Name]*Message
	PortTypes map[xml.Name]*PortType
	Bindings  map[xml.Name]*Binding
	Services  map[xml.Name]*Service

	Schemas *xsd.Set

	// Prefixes hält die im Dokument deklarierten Präfixe, damit erzeugte
	// Requests dieselben Präfixe verwenden wie das WSDL. Das erleichtert den
	// Abgleich mit Referenzbeispielen erheblich.
	Prefixes map[string]string

	// Sources listet alle geladenen Dokumente (WSDL + XSD) für den Cache.
	Sources []Source

	serviceOrder []xml.Name
}

// Source ist ein geladenes Quelldokument.
type Source struct {
	URL     string
	Kind    string // "wsdl" | "xsd"
	Content []byte
}

// OperationView ist die flache Sicht, die UI und Requestbau benutzen:
// ein Eintrag je Service/Port/Operation.
type OperationView struct {
	Service   string
	Port      string
	Binding   string
	Operation string

	Endpoint   string
	Version    SOAPVersion
	Style      string // document | rpc
	Use        string // literal | encoded
	SOAPAction string
	Doc        string

	// Auflösung für den Requestbau
	InputMessage  xml.Name
	OutputMessage xml.Name
	InputBody     *BodyBinding
	InHeaders     []*HeaderBinding

	SuggestMTOM bool
}

// Endpoints liefert alle im WSDL gefundenen Adressen, dedupliziert und
// in Dokumentreihenfolge. Das ist die automatische Endpoint-Erkennung.
func (d *Definitions) Endpoints() []string {
	seen := map[string]bool{}
	var out []string
	for _, svc := range d.orderedServices() {
		for _, p := range svc.Ports {
			if p.Address == "" || seen[p.Address] {
				continue
			}
			seen[p.Address] = true
			out = append(out, p.Address)
		}
	}
	return out
}
