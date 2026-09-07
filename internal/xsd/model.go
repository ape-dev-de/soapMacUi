// Package xsd modelliert XML-Schema und erzeugt daraus Beispielinstanzen.
//
// Der Umfang orientiert sich an dem, was SOAP-WSDLs in der Praxis benutzen,
// nicht an der Vollständigkeit der Spezifikation: Elemente, komplexe Typen mit
// sequence/choice/all, Ableitung per extension/restriction, benannte einfache
// Typen mit Restriktionen, Gruppen, Attribute und Substitutionsgruppen.
package xsd

import "encoding/xml"

// Namespace-Konstanten.
const (
	NS  = "http://www.w3.org/2001/XMLSchema"
	NSI = "http://www.w3.org/2001/XMLSchema-instance"
)

// Unbounded steht für maxOccurs="unbounded".
const Unbounded = -1

// Type ist die gemeinsame Schnittstelle einfacher und komplexer Typen.
type Type interface {
	TypeName() xml.Name
	isType()
}

// SimpleType ist ein einfacher Typ, ggf. mit Restriktionen.
type SimpleType struct {
	Name    xml.Name
	Base    xml.Name // Basis einer restriction
	Variety Variety

	Enumerations []string
	Pattern      string
	MinLength    *int
	MaxLength    *int
	Length       *int
	MinInclusive string
	MaxInclusive string
	MinExclusive string
	MaxExclusive string
	TotalDigits  *int
	FractionDig  *int

	ItemType    xml.Name   // list
	MemberTypes []xml.Name // union
	Members     []*SimpleType

	Doc string
}

func (t *SimpleType) TypeName() xml.Name { return t.Name }
func (t *SimpleType) isType()            {}

// Variety unterscheidet atomic, list und union.
type Variety uint8

const (
	VarietyAtomic Variety = iota
	VarietyList
	VarietyUnion
)

// ComplexType ist ein komplexer Typ.
type ComplexType struct {
	Name     xml.Name
	Abstract bool
	Mixed    bool

	// Ableitung: Base ist gesetzt, wenn complexContent/simpleContent
	// mit extension oder restriction verwendet wird.
	Base       xml.Name
	Derivation Derivation

	// SimpleContentBase ist bei simpleContent der Textinhaltstyp.
	SimpleContent bool

	Content    *Particle
	Attributes []*Attribute
	AnyAttr    bool

	Doc string
}

func (t *ComplexType) TypeName() xml.Name { return t.Name }
func (t *ComplexType) isType()            {}

// Derivation beschreibt die Art der Typableitung.
type Derivation uint8

const (
	DerivNone Derivation = iota
	DerivExtension
	DerivRestriction
)

// ParticleKind unterscheidet die Bestandteile eines Inhaltsmodells.
type ParticleKind uint8

const (
	KindSequence ParticleKind = iota
	KindChoice
	KindAll
	KindElement
	KindAny
	KindGroupRef
)

// Particle ist ein Knoten im Inhaltsmodell eines komplexen Typs.
type Particle struct {
	Kind      ParticleKind
	MinOccurs int
	MaxOccurs int // Unbounded für unbegrenzt

	Children []*Particle // sequence/choice/all
	Element  *Element    // KindElement
	Ref      xml.Name    // KindGroupRef
	AnyNS    string      // KindAny: namespace-Constraint
}

// Element ist eine Elementdeklaration, global oder lokal.
type Element struct {
	Name xml.Name
	Ref  xml.Name // Verweis auf eine globale Deklaration

	TypeRef    xml.Name // benannter Typ
	InlineType Type     // anonym deklarierter Typ

	MinOccurs int
	MaxOccurs int

	Nillable bool
	Abstract bool
	Default  string
	Fixed    string

	SubstitutionGroup xml.Name
	Qualified         bool // ergibt sich aus form / elementFormDefault

	Doc string
}

// Attribute ist eine Attributdeklaration.
type Attribute struct {
	Name xml.Name
	Ref  xml.Name

	TypeRef    xml.Name
	InlineType Type

	Use       string // optional | required | prohibited
	Default   string
	Fixed     string
	Qualified bool

	Doc string
}

// Group ist eine benannte Modellgruppe.
type Group struct {
	Name    xml.Name
	Content *Particle
	Doc     string
}

// AttributeGroup ist eine benannte Attributgruppe.
type AttributeGroup struct {
	Name       xml.Name
	Attributes []*Attribute
	Refs       []xml.Name
	AnyAttr    bool
}

// Schema ist ein einzelnes xs:schema-Dokument.
type Schema struct {
	TargetNS           string
	ElementFormDefault string
	AttrFormDefault    string
	Location           string // Herkunft, für Fehlermeldungen und Cache
}

// Set ist die Menge aller geladenen Schemata mit globalen Indexen.
// Namensauflösung läuft immer über den Set, nie über ein Einzelschema,
// weil WSDLs Typen quer über Namespaces hinweg referenzieren.
type Set struct {
	Schemas []*Schema

	Elements   map[xml.Name]*Element
	Types      map[xml.Name]Type
	Groups     map[xml.Name]*Group
	AttrGroups map[xml.Name]*AttributeGroup
	Attributes map[xml.Name]*Attribute

	// substitutions bildet Head-Element -> Ersetzer ab.
	substitutions map[xml.Name][]*Element
}

// NewSet legt einen leeren Set an.
func NewSet() *Set {
	return &Set{
		Elements:      map[xml.Name]*Element{},
		Types:         map[xml.Name]Type{},
		Groups:        map[xml.Name]*Group{},
		AttrGroups:    map[xml.Name]*AttributeGroup{},
		Attributes:    map[xml.Name]*Attribute{},
		substitutions: map[xml.Name][]*Element{},
	}
}

// ResolveType schlägt einen benannten Typ nach.
func (s *Set) ResolveType(n xml.Name) Type {
	if n.Local == "" {
		return nil
	}
	return s.Types[n]
}

// ResolveElement schlägt eine globale Elementdeklaration nach.
func (s *Set) ResolveElement(n xml.Name) *Element {
	if n.Local == "" {
		return nil
	}
	return s.Elements[n]
}

// Substitutes liefert die konkreten Ersetzer eines abstrakten Head-Elements.
func (s *Set) Substitutes(head xml.Name) []*Element { return s.substitutions[head] }

// IsBuiltin sagt, ob der Name ein eingebauter Schema-Datentyp ist.
func IsBuiltin(n xml.Name) bool { return n.Space == NS }
