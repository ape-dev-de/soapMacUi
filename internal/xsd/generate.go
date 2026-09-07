package xsd

import (
	"encoding/xml"
	"fmt"
	"sort"
	"strings"
)

// GenOptions steuert die Erzeugung von Beispielinstanzen.
type GenOptions struct {
	MaxDepth        int    // Rekursionsschutz; 0 => 12
	ArraySamples    int    // Anzahl Beispiele bei maxOccurs > 1; 0 => 2
	IncludeOptional bool   // Elemente mit minOccurs=0 mit erzeugen
	CommentOptional bool   // optionale Elemente auskommentieren statt weglassen
	CommentChoices  bool   // nicht gewählte choice-Zweige als Kommentar zeigen
	ExampleValues   bool   // typgerechte Beispielwerte statt "?"
	Placeholder     string // Standardplatzhalter; "" => "?"
	Indent          string // "" => zwei Leerzeichen
}

func (o *GenOptions) norm() {
	if o.MaxDepth <= 0 {
		o.MaxDepth = 12
	}
	if o.ArraySamples <= 0 {
		o.ArraySamples = 2
	}
	if o.Placeholder == "" {
		o.Placeholder = "?"
	}
	if o.Indent == "" {
		o.Indent = "  "
	}
}

// Prefixer vergibt stabile Namespace-Präfixe.
type Prefixer struct {
	byURI map[string]string
	used  map[string]bool
	n     int
}

// NewPrefixer legt einen Prefixer an.
func NewPrefixer() *Prefixer {
	return &Prefixer{byURI: map[string]string{}, used: map[string]bool{}}
}

// Prefer schlägt ein Wunschpräfix für eine URI vor.
func (p *Prefixer) Prefer(uri, prefix string) {
	if uri == "" || prefix == "" {
		return
	}
	if _, ok := p.byURI[uri]; ok {
		return
	}
	if p.used[prefix] {
		return
	}
	p.byURI[uri] = prefix
	p.used[prefix] = true
}

// Prefix liefert das Präfix für eine URI und legt bei Bedarf eines an.
func (p *Prefixer) Prefix(uri string) string {
	if uri == "" {
		return ""
	}
	if pre, ok := p.byURI[uri]; ok {
		return pre
	}
	p.n++
	pre := fmt.Sprintf("ns%d", p.n)
	for p.used[pre] {
		p.n++
		pre = fmt.Sprintf("ns%d", p.n)
	}
	p.byURI[uri] = pre
	p.used[pre] = true
	return pre
}

// Declared liefert alle vergebenen Präfixe als Präfix -> URI.
func (p *Prefixer) Declared() map[string]string {
	out := make(map[string]string, len(p.byURI))
	for uri, pre := range p.byURI {
		out[pre] = uri
	}
	return out
}

// Generator erzeugt Beispielinstanzen aus einem Schema-Set.
type Generator struct {
	Set  *Set
	Opts GenOptions
	Pfx  *Prefixer

	needXSI bool
}

// NewGenerator erzeugt einen Generator mit sinnvollen Vorgaben.
func NewGenerator(s *Set, opts GenOptions) *Generator {
	opts.norm()
	g := &Generator{Set: s, Opts: opts, Pfx: NewPrefixer()}
	return g
}

type genCtx struct {
	sb    *strings.Builder
	depth int
	stack []xml.Name // Typ-Stack für Rekursionserkennung
}

// GenerateElement erzeugt die Beispielinstanz einer globalen Elementdeklaration.
// Zurück kommt das XML-Fragment; die benötigten Namespace-Deklarationen holt
// man danach über Pfx.Declared() und hängt sie an das umschliessende Element.
func (g *Generator) GenerateElement(name xml.Name, indentLevel int) (string, error) {
	e := g.Set.ResolveElement(name)
	if e == nil {
		return "", fmt.Errorf("element %s (%s) nicht im Schema gefunden", name.Local, name.Space)
	}
	c := &genCtx{sb: &strings.Builder{}, depth: indentLevel}
	g.writeElement(c, e, indentLevel, 1, 1)
	return strings.TrimRight(c.sb.String(), "\n"), nil
}

// GenerateType erzeugt den Inhalt eines Typs unter einem gegebenen Elementnamen.
// Wird für rpc/literal gebraucht, wo Parts Typen statt Elemente referenzieren.
func (g *Generator) GenerateType(elemName xml.Name, typeName xml.Name, indentLevel int) (string, error) {
	t := g.Set.ResolveType(typeName)
	if t == nil && !IsBuiltin(typeName) {
		return "", fmt.Errorf("typ %s (%s) nicht im Schema gefunden", typeName.Local, typeName.Space)
	}
	e := &Element{Name: elemName, TypeRef: typeName, MinOccurs: 1, MaxOccurs: 1, Qualified: elemName.Space != ""}
	c := &genCtx{sb: &strings.Builder{}, depth: indentLevel}
	g.writeElement(c, e, indentLevel, 1, 1)
	return strings.TrimRight(c.sb.String(), "\n"), nil
}

// NeedsXSI sagt, ob im erzeugten Fragment xsi:type oder xsi:nil verwendet wurde.
func (g *Generator) NeedsXSI() bool { return g.needXSI }

func (g *Generator) ind(level int) string { return strings.Repeat(g.Opts.Indent, level) }

func (g *Generator) qname(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return g.Pfx.Prefix(n.Space) + ":" + n.Local
}

// writeElement schreibt eine Elementdeklaration inklusive Wiederholungen.
func (g *Generator) writeElement(c *genCtx, e *Element, level, min, max int) {
	// ref auflösen
	if e.Ref.Local != "" {
		if target := g.Set.ResolveElement(e.Ref); target != nil {
			merged := *target
			merged.MinOccurs, merged.MaxOccurs = e.MinOccurs, e.MaxOccurs
			e = &merged
		}
	}

	// Abstraktes Element: über die Substitutionsgruppe ersetzen.
	if e.Abstract {
		subs := g.Set.Substitutes(e.Name)
		if len(subs) > 0 {
			c.sb.WriteString(g.ind(level) + "<!-- abstrakt, ersetzt durch " + subs[0].Name.Local)
			if len(subs) > 1 {
				var alts []string
				for _, s := range subs[1:] {
					alts = append(alts, s.Name.Local)
				}
				c.sb.WriteString("; Alternativen: " + strings.Join(alts, ", "))
			}
			c.sb.WriteString(" -->\n")
			g.writeElement(c, subs[0], level, min, max)
			return
		}
	}

	count := 1
	if e.MaxOccurs == Unbounded || e.MaxOccurs > 1 {
		count = g.Opts.ArraySamples
		if e.MaxOccurs > 1 && e.MaxOccurs < count {
			count = e.MaxOccurs
		}
	}

	optional := e.MinOccurs == 0
	if optional && !g.Opts.IncludeOptional && !g.Opts.CommentOptional {
		return
	}

	body := &strings.Builder{}
	sub := &genCtx{sb: body, depth: c.depth, stack: c.stack}
	for i := 0; i < count; i++ {
		g.writeOne(sub, e, level)
	}
	out := body.String()

	if optional && g.Opts.CommentOptional && !g.Opts.IncludeOptional {
		c.sb.WriteString(g.ind(level) + "<!-- optional -->\n")
		c.sb.WriteString(commentOut(out))
		return
	}
	if optional {
		c.sb.WriteString(g.ind(level) + "<!-- optional -->\n")
	}
	c.sb.WriteString(out)
}

func (g *Generator) writeOne(c *genCtx, e *Element, level int) {
	tag := g.qname(e.Name)
	t := g.resolveElemType(e)

	// Attribute vorbereiten
	var attrs []string
	if ct, ok := t.(*ComplexType); ok && ct.Abstract {
		if concrete := g.concreteFor(ct.Name); concrete != nil {
			g.needXSI = true
			attrs = append(attrs, fmt.Sprintf(`xsi:type="%s"`, g.qname(concrete.Name)))
			t = concrete
		}
	}
	if ct, ok := t.(*ComplexType); ok {
		for _, a := range g.effectiveAttributes(ct) {
			if a.Use == "prohibited" {
				continue
			}
			if a.Use != "required" && !g.Opts.IncludeOptional {
				continue
			}
			attrs = append(attrs, fmt.Sprintf(`%s="%s"`, g.attrName(a), g.attrValue(a)))
		}
	}
	open := tag
	if len(attrs) > 0 {
		open += " " + strings.Join(attrs, " ")
	}

	switch tt := t.(type) {
	case *ComplexType:
		// Rekursionsschutz über den Typ-Stack
		if tt.Name.Local != "" {
			if depthOf(c.stack, tt.Name) > 0 || len(c.stack) >= g.Opts.MaxDepth {
				c.sb.WriteString(g.ind(level) + "<" + open + ">")
				c.sb.WriteString("<!-- rekursiv: " + tt.Name.Local + " -->")
				c.sb.WriteString("</" + tag + ">\n")
				return
			}
			c.stack = append(c.stack, tt.Name)
			defer func() { c.stack = c.stack[:len(c.stack)-1] }()
		}

		inner := &strings.Builder{}
		sub := &genCtx{sb: inner, depth: c.depth, stack: c.stack}
		for _, p := range g.effectiveContent(tt) {
			g.writeParticle(sub, p, level+1)
		}
		if tt.SimpleContent {
			base := g.baseSimple(tt)
			c.sb.WriteString(g.ind(level) + "<" + open + ">" + g.sampleValue(base) + "</" + tag + ">\n")
			return
		}
		if inner.Len() == 0 {
			c.sb.WriteString(g.ind(level) + "<" + open + "/>\n")
			return
		}
		c.sb.WriteString(g.ind(level) + "<" + open + ">\n")
		c.sb.WriteString(inner.String())
		c.sb.WriteString(g.ind(level) + "</" + tag + ">\n")

	default:
		val := g.sampleValue(t)
		if e.Fixed != "" {
			val = e.Fixed
		} else if e.Default != "" {
			val = e.Default
		}
		c.sb.WriteString(g.ind(level) + "<" + open + ">" + escape(val) + "</" + tag + ">\n")
	}
}

func (g *Generator) writeParticle(c *genCtx, p *Particle, level int) {
	if p == nil {
		return
	}
	switch p.Kind {
	case KindElement:
		e := *p.Element
		e.MinOccurs, e.MaxOccurs = p.MinOccurs, p.MaxOccurs
		g.writeElement(c, &e, level, p.MinOccurs, p.MaxOccurs)

	case KindSequence, KindAll:
		reps := 1
		if p.MaxOccurs == Unbounded || p.MaxOccurs > 1 {
			reps = g.Opts.ArraySamples
		}
		for i := 0; i < reps; i++ {
			for _, ch := range p.Children {
				g.writeParticle(c, ch, level)
			}
		}

	case KindChoice:
		if len(p.Children) == 0 {
			return
		}
		if len(p.Children) > 1 {
			c.sb.WriteString(g.ind(level) + "<!-- Auswahl: 1 von " + fmt.Sprint(len(p.Children)) + " -->\n")
		}
		g.writeParticle(c, p.Children[0], level)
		if g.Opts.CommentChoices {
			for _, alt := range p.Children[1:] {
				b := &strings.Builder{}
				g.writeParticle(&genCtx{sb: b, stack: c.stack}, alt, level)
				if b.Len() > 0 {
					c.sb.WriteString(commentOut(b.String()))
				}
			}
		}

	case KindGroupRef:
		if gr, ok := g.Set.Groups[p.Ref]; ok && gr.Content != nil {
			g.writeParticle(c, gr.Content, level)
		}

	case KindAny:
		c.sb.WriteString(g.ind(level) + "<!-- beliebiges Element erlaubt (xs:any")
		if p.AnyNS != "" {
			c.sb.WriteString(", namespace=" + p.AnyNS)
		}
		c.sb.WriteString(") -->\n")
	}
}

// resolveElemType ermittelt den Typ einer Elementdeklaration.
func (g *Generator) resolveElemType(e *Element) Type {
	if e.InlineType != nil {
		return e.InlineType
	}
	if e.TypeRef.Local != "" {
		if t := g.Set.ResolveType(e.TypeRef); t != nil {
			return t
		}
		// eingebauter Typ
		return &SimpleType{Name: e.TypeRef, Base: e.TypeRef}
	}
	// ohne Typangabe: xs:anyType
	return &SimpleType{Name: xml.Name{Space: NS, Local: "anyType"}, Base: xml.Name{Space: NS, Local: "anyType"}}
}

// effectiveContent liefert die Inhaltsmodelle inklusive geerbter Basis-Inhalte.
// Bei extension kommt der Basisinhalt zuerst, danach der eigene — das ist die
// Reihenfolge, die im Instanzdokument gelten muss.
func (g *Generator) effectiveContent(t *ComplexType) []*Particle {
	var out []*Particle
	seen := map[xml.Name]bool{}
	var walk func(ct *ComplexType)
	walk = func(ct *ComplexType) {
		if ct == nil {
			return
		}
		if ct.Name.Local != "" {
			if seen[ct.Name] {
				return
			}
			seen[ct.Name] = true
		}
		if ct.Derivation == DerivExtension && ct.Base.Local != "" {
			if base, ok := g.Set.ResolveType(ct.Base).(*ComplexType); ok {
				walk(base)
			}
		}
		if ct.Content != nil {
			out = append(out, ct.Content)
		}
	}
	walk(t)
	return out
}

func (g *Generator) effectiveAttributes(t *ComplexType) []*Attribute {
	var out []*Attribute
	seen := map[xml.Name]bool{}
	var walk func(ct *ComplexType)
	walk = func(ct *ComplexType) {
		if ct == nil || (ct.Name.Local != "" && seen[ct.Name]) {
			return
		}
		if ct.Name.Local != "" {
			seen[ct.Name] = true
		}
		if ct.Base.Local != "" {
			if base, ok := g.Set.ResolveType(ct.Base).(*ComplexType); ok {
				walk(base)
			}
		}
		out = append(out, ct.Attributes...)
	}
	walk(t)
	return out
}

// baseSimple sucht bei simpleContent den zugrundeliegenden einfachen Typ.
func (g *Generator) baseSimple(t *ComplexType) Type {
	cur := t
	for i := 0; i < 16 && cur != nil; i++ {
		if cur.Base.Local == "" {
			break
		}
		b := g.Set.ResolveType(cur.Base)
		if st, ok := b.(*SimpleType); ok {
			return st
		}
		ct, ok := b.(*ComplexType)
		if !ok {
			return &SimpleType{Name: cur.Base, Base: cur.Base}
		}
		cur = ct
	}
	return &SimpleType{Name: xml.Name{Space: NS, Local: "string"}, Base: xml.Name{Space: NS, Local: "string"}}
}

// concreteFor sucht einen konkreten Typ, der von einem abstrakten ableitet.
func (g *Generator) concreteFor(abstract xml.Name) *ComplexType {
	var cands []*ComplexType
	for _, t := range g.Set.Types {
		ct, ok := t.(*ComplexType)
		if !ok || ct.Abstract || ct.Base.Local == "" {
			continue
		}
		// Ableitungskette hochlaufen
		cur := ct
		for i := 0; i < 16; i++ {
			if cur.Base == abstract {
				cands = append(cands, ct)
				break
			}
			next, ok := g.Set.ResolveType(cur.Base).(*ComplexType)
			if !ok {
				break
			}
			cur = next
		}
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].Name.Local < cands[j].Name.Local })
	return cands[0]
}

func (g *Generator) attrName(a *Attribute) string {
	if a.Qualified && a.Name.Space != "" {
		return g.qname(a.Name)
	}
	return a.Name.Local
}

func (g *Generator) attrValue(a *Attribute) string {
	if a.Fixed != "" {
		return escape(a.Fixed)
	}
	if a.Default != "" {
		return escape(a.Default)
	}
	t := Type(nil)
	if a.InlineType != nil {
		t = a.InlineType
	} else if a.TypeRef.Local != "" {
		if rt := g.Set.ResolveType(a.TypeRef); rt != nil {
			t = rt
		} else {
			t = &SimpleType{Name: a.TypeRef, Base: a.TypeRef}
		}
	}
	return escape(g.sampleValue(t))
}

func depthOf(stack []xml.Name, n xml.Name) int {
	cnt := 0
	for _, s := range stack {
		if s == n {
			cnt++
		}
	}
	return cnt
}

func commentOut(s string) string {
	s = strings.ReplaceAll(s, "--", "- -")
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var b strings.Builder
	for _, l := range lines {
		indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		b.WriteString(indent + "<!--" + strings.TrimLeft(l, " \t") + "-->\n")
	}
	return b.String()
}

func escape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return r.Replace(s)
}

// IndentOr liefert den konfigurierten Einzug oder den Standardwert.
func (o GenOptions) IndentOr() string {
	if o.Indent == "" {
		return "  "
	}
	return o.Indent
}
