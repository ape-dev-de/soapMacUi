package xsd

import (
	"encoding/xml"
	"strings"
)

// sampleValue liefert den Textwert für einen einfachen Typ.
//
// Enumerationen gewinnen immer: ein "?" wäre dort garantiert schemawidrig, und
// gerade bei einem pingeligen Server ist ein gültiger Wert mehr wert als ein
// erkennbarer Platzhalter. Sonst entscheidet ExampleValues, ob ein typgerechter
// Beispielwert oder der neutrale Platzhalter erzeugt wird.
func (g *Generator) sampleValue(t Type) string {
	st, _ := t.(*SimpleType)
	if st != nil {
		if v, ok := g.enumValue(st); ok {
			return v
		}
	}
	if !g.Opts.ExampleValues {
		return g.Opts.Placeholder
	}
	return builtinExample(g.builtinBase(st))
}

// enumValue sucht die erste Enumeration in der Restriktionskette.
func (g *Generator) enumValue(st *SimpleType) (string, bool) {
	seen := map[xml.Name]bool{}
	cur := st
	for i := 0; i < 16 && cur != nil; i++ {
		if len(cur.Enumerations) > 0 {
			return cur.Enumerations[0], true
		}
		for _, m := range cur.Members {
			if v, ok := g.enumValue(m); ok {
				return v, true
			}
		}
		if cur.Base.Local == "" || seen[cur.Base] {
			break
		}
		seen[cur.Base] = true
		next, _ := g.Set.ResolveType(cur.Base).(*SimpleType)
		cur = next
	}
	return "", false
}

// builtinBase läuft die Restriktionskette bis zum eingebauten Schema-Typ hoch.
func (g *Generator) builtinBase(st *SimpleType) string {
	if st == nil {
		return "string"
	}
	seen := map[xml.Name]bool{}
	cur := st
	for i := 0; i < 16 && cur != nil; i++ {
		if IsBuiltin(cur.Name) {
			return cur.Name.Local
		}
		if cur.Variety == VarietyList {
			if IsBuiltin(cur.ItemType) {
				return cur.ItemType.Local
			}
		}
		if cur.Variety == VarietyUnion && len(cur.MemberTypes) > 0 {
			if IsBuiltin(cur.MemberTypes[0]) {
				return cur.MemberTypes[0].Local
			}
		}
		if cur.Base.Local == "" || seen[cur.Base] {
			break
		}
		if IsBuiltin(cur.Base) {
			return cur.Base.Local
		}
		seen[cur.Base] = true
		next, _ := g.Set.ResolveType(cur.Base).(*SimpleType)
		cur = next
	}
	return "string"
}

// builtinExample liefert einen schemagültigen Beispielwert je eingebautem Typ.
func builtinExample(name string) string {
	switch strings.ToLower(name) {
	case "string", "normalizedstring", "token", "language", "name", "ncname",
		"nmtoken", "id", "idref", "entity", "anyuri", "anytype", "anysimpletype":
		if name == "anyURI" {
			return "http://example.org/"
		}
		return "text"
	case "boolean":
		return "true"
	case "decimal":
		return "0.0"
	case "float", "double":
		return "0.0"
	case "integer", "int", "long", "short", "byte":
		return "0"
	case "nonnegativeinteger", "positiveinteger", "unsignedint",
		"unsignedlong", "unsignedshort", "unsignedbyte":
		return "1"
	case "nonpositiveinteger", "negativeinteger":
		return "-1"
	case "date":
		return "2026-01-01"
	case "datetime":
		return "2026-01-01T12:00:00Z"
	case "time":
		return "12:00:00Z"
	case "gyear":
		return "2026"
	case "gyearmonth":
		return "2026-01"
	case "gmonth":
		return "--01"
	case "gmonthday":
		return "--01-01"
	case "gday":
		return "---01"
	case "duration":
		return "P1D"
	case "base64binary":
		return "MA=="
	case "hexbinary":
		return "30"
	case "qname":
		return "xs:string"
	default:
		return "text"
	}
}
