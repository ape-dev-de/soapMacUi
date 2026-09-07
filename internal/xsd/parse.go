package xsd

import (
	"encoding/xml"
	"strconv"
	"strings"

	"github.com/apeters/soapmacui/internal/xdom"
)

// ParseSchema liest ein xs:schema-Element in den Set ein.
// Mehrfaches Aufrufen für dieselbe Zielnamespace ist erlaubt und üblich
// (xs:include, mehrere inline-Schemata in wsdl:types).
func (s *Set) ParseSchema(root *xdom.Node, location string) *Schema {
	sch := &Schema{
		TargetNS:           root.Attr("targetNamespace"),
		ElementFormDefault: root.Attr("elementFormDefault"),
		AttrFormDefault:    root.Attr("attributeFormDefault"),
		Location:           location,
	}
	s.Schemas = append(s.Schemas, sch)

	for _, c := range root.Children {
		if c.Name.Space != NS {
			continue
		}
		switch c.Name.Local {
		case "element":
			e := s.parseElement(c, sch, true)
			if e.Name.Local != "" {
				s.Elements[e.Name] = e
				if e.SubstitutionGroup.Local != "" {
					s.substitutions[e.SubstitutionGroup] = append(s.substitutions[e.SubstitutionGroup], e)
				}
			}
		case "complexType":
			t := s.parseComplexType(c, sch)
			if t.Name.Local != "" {
				s.Types[t.Name] = t
			}
		case "simpleType":
			t := s.parseSimpleType(c, sch)
			if t.Name.Local != "" {
				s.Types[t.Name] = t
			}
		case "group":
			g := &Group{
				Name: xml.Name{Space: sch.TargetNS, Local: c.Attr("name")},
				Doc:  documentation(c),
			}
			for _, gc := range c.Children {
				if gc.Name.Space == NS && isModelGroup(gc.Name.Local) {
					g.Content = s.parseParticle(gc, sch)
					break
				}
			}
			if g.Name.Local != "" {
				s.Groups[g.Name] = g
			}
		case "attributeGroup":
			ag := &AttributeGroup{Name: xml.Name{Space: sch.TargetNS, Local: c.Attr("name")}}
			s.collectAttributes(c, sch, &ag.Attributes, &ag.Refs, &ag.AnyAttr)
			if ag.Name.Local != "" {
				s.AttrGroups[ag.Name] = ag
			}
		case "attribute":
			a := s.parseAttribute(c, sch, true)
			if a.Name.Local != "" {
				s.Attributes[a.Name] = a
			}
		}
	}
	return sch
}

func isModelGroup(local string) bool {
	return local == "sequence" || local == "choice" || local == "all"
}

func (s *Set) parseElement(n *xdom.Node, sch *Schema, global bool) *Element {
	e := &Element{
		MinOccurs: 1,
		MaxOccurs: 1,
		Nillable:  n.Attr("nillable") == "true",
		Abstract:  n.Attr("abstract") == "true",
		Default:   n.Attr("default"),
		Fixed:     n.Attr("fixed"),
		Doc:       documentation(n),
	}

	if ref := n.Attr("ref"); ref != "" {
		e.Ref = n.ResolveQName(ref)
		e.Name = e.Ref
	} else if name := n.Attr("name"); name != "" {
		// Globale Elemente sind immer qualifiziert. Lokale nur, wenn
		// form="qualified" oder elementFormDefault="qualified".
		qualified := global || n.Attr("form") == "qualified" ||
			(n.Attr("form") == "" && sch.ElementFormDefault == "qualified")
		e.Qualified = qualified
		if qualified {
			e.Name = xml.Name{Space: sch.TargetNS, Local: name}
		} else {
			e.Name = xml.Name{Local: name}
		}
	}

	if t := n.Attr("type"); t != "" {
		e.TypeRef = n.ResolveQName(t)
	}
	if sg := n.Attr("substitutionGroup"); sg != "" {
		e.SubstitutionGroup = n.ResolveQName(sg)
	}

	if !global {
		e.MinOccurs = occurs(n, "minOccurs", 1)
		e.MaxOccurs = occurs(n, "maxOccurs", 1)
	}

	// Anonym deklarierter Typ
	for _, c := range n.Children {
		if c.Name.Space != NS {
			continue
		}
		switch c.Name.Local {
		case "complexType":
			e.InlineType = s.parseComplexType(c, sch)
		case "simpleType":
			e.InlineType = s.parseSimpleType(c, sch)
		}
	}
	return e
}

func (s *Set) parseComplexType(n *xdom.Node, sch *Schema) *ComplexType {
	t := &ComplexType{
		Abstract: n.Attr("abstract") == "true",
		Mixed:    n.Attr("mixed") == "true",
		Doc:      documentation(n),
	}
	if name := n.Attr("name"); name != "" {
		t.Name = xml.Name{Space: sch.TargetNS, Local: name}
	}

	for _, c := range n.Children {
		if c.Name.Space != NS {
			continue
		}
		switch c.Name.Local {
		case "complexContent", "simpleContent":
			t.SimpleContent = c.Name.Local == "simpleContent"
			for _, d := range c.Children {
				if d.Name.Space != NS {
					continue
				}
				switch d.Name.Local {
				case "extension":
					t.Derivation = DerivExtension
					t.Base = d.ResolveQName(d.Attr("base"))
					s.fillComplexBody(d, sch, t)
				case "restriction":
					t.Derivation = DerivRestriction
					t.Base = d.ResolveQName(d.Attr("base"))
					s.fillComplexBody(d, sch, t)
				}
			}
		case "sequence", "choice", "all":
			t.Content = s.parseParticle(c, sch)
		case "group":
			t.Content = s.parseParticle(c, sch)
		case "attribute", "attributeGroup", "anyAttribute":
			// unten gesammelt
		}
	}
	var refs []xml.Name
	s.collectAttributes(n, sch, &t.Attributes, &refs, &t.AnyAttr)
	s.expandAttrGroupRefs(refs, &t.Attributes)
	return t
}

// fillComplexBody übernimmt Inhaltsmodell und Attribute aus einem
// extension-/restriction-Knoten.
func (s *Set) fillComplexBody(n *xdom.Node, sch *Schema, t *ComplexType) {
	for _, c := range n.Children {
		if c.Name.Space == NS && (isModelGroup(c.Name.Local) || c.Name.Local == "group") {
			t.Content = s.parseParticle(c, sch)
			break
		}
	}
	var refs []xml.Name
	s.collectAttributes(n, sch, &t.Attributes, &refs, &t.AnyAttr)
	s.expandAttrGroupRefs(refs, &t.Attributes)
}

func (s *Set) expandAttrGroupRefs(refs []xml.Name, out *[]*Attribute) {
	for _, r := range refs {
		if ag, ok := s.AttrGroups[r]; ok {
			*out = append(*out, ag.Attributes...)
		}
	}
}

func (s *Set) collectAttributes(n *xdom.Node, sch *Schema, out *[]*Attribute, refs *[]xml.Name, anyAttr *bool) {
	for _, c := range n.Children {
		if c.Name.Space != NS {
			continue
		}
		switch c.Name.Local {
		case "attribute":
			*out = append(*out, s.parseAttribute(c, sch, false))
		case "attributeGroup":
			if r := c.Attr("ref"); r != "" {
				*refs = append(*refs, c.ResolveQName(r))
			} else {
				s.collectAttributes(c, sch, out, refs, anyAttr)
			}
		case "anyAttribute":
			*anyAttr = true
		}
	}
}

func (s *Set) parseAttribute(n *xdom.Node, sch *Schema, global bool) *Attribute {
	a := &Attribute{
		Use:     n.Attr("use"),
		Default: n.Attr("default"),
		Fixed:   n.Attr("fixed"),
		Doc:     documentation(n),
	}
	if a.Use == "" {
		a.Use = "optional"
	}
	if ref := n.Attr("ref"); ref != "" {
		a.Ref = n.ResolveQName(ref)
		a.Name = a.Ref
		a.Qualified = true
	} else if name := n.Attr("name"); name != "" {
		qualified := global || n.Attr("form") == "qualified" ||
			(n.Attr("form") == "" && sch.AttrFormDefault == "qualified")
		a.Qualified = qualified
		if qualified {
			a.Name = xml.Name{Space: sch.TargetNS, Local: name}
		} else {
			a.Name = xml.Name{Local: name}
		}
	}
	if t := n.Attr("type"); t != "" {
		a.TypeRef = n.ResolveQName(t)
	}
	for _, c := range n.Children {
		if c.Name.Space == NS && c.Name.Local == "simpleType" {
			a.InlineType = s.parseSimpleType(c, sch)
		}
	}
	return a
}

func (s *Set) parseParticle(n *xdom.Node, sch *Schema) *Particle {
	p := &Particle{
		MinOccurs: occurs(n, "minOccurs", 1),
		MaxOccurs: occurs(n, "maxOccurs", 1),
	}
	switch n.Name.Local {
	case "sequence":
		p.Kind = KindSequence
	case "choice":
		p.Kind = KindChoice
	case "all":
		p.Kind = KindAll
	case "group":
		p.Kind = KindGroupRef
		if r := n.Attr("ref"); r != "" {
			p.Ref = n.ResolveQName(r)
			return p
		}
		// Inline-Gruppendefinition: auf das Modellgruppen-Kind durchgreifen
		for _, c := range n.Children {
			if c.Name.Space == NS && isModelGroup(c.Name.Local) {
				return s.parseParticle(c, sch)
			}
		}
		return p
	case "any":
		p.Kind = KindAny
		p.AnyNS = n.Attr("namespace")
		return p
	}

	for _, c := range n.Children {
		if c.Name.Space != NS {
			continue
		}
		switch c.Name.Local {
		case "element":
			p.Children = append(p.Children, &Particle{
				Kind:      KindElement,
				Element:   s.parseElement(c, sch, false),
				MinOccurs: occurs(c, "minOccurs", 1),
				MaxOccurs: occurs(c, "maxOccurs", 1),
			})
		case "sequence", "choice", "all", "group", "any":
			p.Children = append(p.Children, s.parseParticle(c, sch))
		}
	}
	return p
}

func (s *Set) parseSimpleType(n *xdom.Node, sch *Schema) *SimpleType {
	t := &SimpleType{Doc: documentation(n)}
	if name := n.Attr("name"); name != "" {
		t.Name = xml.Name{Space: sch.TargetNS, Local: name}
	}

	for _, c := range n.Children {
		if c.Name.Space != NS {
			continue
		}
		switch c.Name.Local {
		case "restriction":
			t.Variety = VarietyAtomic
			t.Base = c.ResolveQName(c.Attr("base"))
			for _, f := range c.Children {
				if f.Name.Space != NS {
					continue
				}
				v := f.Attr("value")
				switch f.Name.Local {
				case "enumeration":
					t.Enumerations = append(t.Enumerations, v)
				case "pattern":
					t.Pattern = v
				case "minLength":
					t.MinLength = intPtr(v)
				case "maxLength":
					t.MaxLength = intPtr(v)
				case "length":
					t.Length = intPtr(v)
				case "minInclusive":
					t.MinInclusive = v
				case "maxInclusive":
					t.MaxInclusive = v
				case "minExclusive":
					t.MinExclusive = v
				case "maxExclusive":
					t.MaxExclusive = v
				case "totalDigits":
					t.TotalDigits = intPtr(v)
				case "fractionDigits":
					t.FractionDig = intPtr(v)
				case "simpleType":
					t.Members = append(t.Members, s.parseSimpleType(f, sch))
				}
			}
		case "list":
			t.Variety = VarietyList
			if it := c.Attr("itemType"); it != "" {
				t.ItemType = c.ResolveQName(it)
			}
		case "union":
			t.Variety = VarietyUnion
			for _, m := range strings.Fields(c.Attr("memberTypes")) {
				t.MemberTypes = append(t.MemberTypes, c.ResolveQName(m))
			}
			for _, mc := range c.Children {
				if mc.Name.Space == NS && mc.Name.Local == "simpleType" {
					t.Members = append(t.Members, s.parseSimpleType(mc, sch))
				}
			}
		}
	}
	return t
}

func occurs(n *xdom.Node, attr string, def int) int {
	v := n.Attr(attr)
	if v == "" {
		return def
	}
	if v == "unbounded" {
		return Unbounded
	}
	i, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return i
}

func intPtr(v string) *int {
	i, err := strconv.Atoi(v)
	if err != nil {
		return nil
	}
	return &i
}

func documentation(n *xdom.Node) string {
	a := n.Child(NS, "annotation")
	if a == nil {
		return ""
	}
	d := a.Child(NS, "documentation")
	if d == nil {
		return ""
	}
	return strings.Join(strings.Fields(d.Text), " ")
}
