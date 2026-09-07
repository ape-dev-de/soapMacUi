// Package xdom stellt einen kleinen, namespace-korrekten DOM bereit.
//
// Gos encoding/xml löst Namespaces zwar für Element- und Attributnamen auf,
// nicht aber für QNames, die als Attribut*wert* stehen — genau das braucht man
// bei XSD/WSDL ständig (type="aes:String.80", ref=, base=, binding=, message=).
// Deshalb halten wir die xmlns-Deklarationen pro Knoten selbst fest.
package xdom

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

// Node ist ein Elementknoten samt gültigem Namespace-Kontext.
type Node struct {
	Name     xml.Name
	Attrs    []xml.Attr
	Children []*Node
	Text     string // zusammengefasste CharData der direkten Kinder
	Parent   *Node

	// ns bildet Präfix -> Namespace-URI ab, inklusive geerbter Deklarationen.
	// Der leere Schlüssel ist der Default-Namespace.
	ns map[string]string
}

// Parse liest ein komplettes Dokument und liefert das Wurzelelement.
func Parse(r io.Reader) (*Node, error) {
	dec := xml.NewDecoder(r)
	// WSDLs aus PHP-Stacks deklarieren gern ISO-8859-1. Ohne CharsetReader
	// bricht encoding/xml darauf ab.
	dec.CharsetReader = charsetReader

	var root, cur *Node
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("xml: %w", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			n := &Node{Name: t.Name, Parent: cur}
			n.ns = inheritNS(cur)
			for _, a := range t.Attr {
				switch {
				case a.Name.Space == "xmlns":
					n.ns[a.Name.Local] = a.Value
				case a.Name.Space == "" && a.Name.Local == "xmlns":
					n.ns[""] = a.Value
				default:
					n.Attrs = append(n.Attrs, a)
				}
			}
			if cur != nil {
				cur.Children = append(cur.Children, n)
			} else if root == nil {
				root = n
			}
			cur = n

		case xml.EndElement:
			if cur != nil {
				cur = cur.Parent
			}

		case xml.CharData:
			if cur != nil {
				cur.Text += string(t)
			}
		}
	}
	if root == nil {
		return nil, fmt.Errorf("xml: kein Wurzelelement gefunden")
	}
	return root, nil
}

// charsetReader unterstützt die Encodings, die in freier Wildbahn bei
// SOAP-Stacks auftauchen. Alles andere lehnen wir mit klarer Meldung ab,
// statt still falsch zu dekodieren.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return input, nil
	case "iso-8859-1", "iso8859-1", "latin1", "iso-8859-15", "windows-1252", "cp1252":
		return &latin1Reader{r: bufio.NewReader(input)}, nil
	default:
		return nil, fmt.Errorf("xml: nicht unterstütztes Encoding %q", charset)
	}
}

// latin1Reader dekodiert Latin-1-Bytes nach UTF-8.
// Windows-1252 weicht nur im Bereich 0x80-0x9F ab; dort weichen wir auf das
// Ersetzungszeichen aus, statt Bytes zu verschlucken.
type latin1Reader struct {
	r   *bufio.Reader
	buf []byte
}

func (l *latin1Reader) Read(p []byte) (int, error) {
	for len(l.buf) == 0 {
		b, err := l.r.ReadByte()
		if err != nil {
			return 0, err
		}
		l.buf = utf8.AppendRune(l.buf[:0], rune(b))
	}
	n := copy(p, l.buf)
	l.buf = l.buf[n:]
	return n, nil
}

func inheritNS(parent *Node) map[string]string {
	m := make(map[string]string, 8)
	if parent != nil {
		for k, v := range parent.ns {
			m[k] = v
		}
	}
	return m
}

// Attr liefert den Wert eines Attributs ohne Namespace.
func (n *Node) Attr(local string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == local && a.Name.Space == "" {
			return a.Value
		}
	}
	return ""
}

// AttrNS liefert den Wert eines namespace-qualifizierten Attributs.
func (n *Node) AttrNS(space, local string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == local && a.Name.Space == space {
			return a.Value
		}
	}
	return ""
}

// HasAttr sagt, ob ein Attribut überhaupt gesetzt wurde. Wichtig, um
// "nicht angegeben" von "explizit auf den Defaultwert gesetzt" zu trennen.
func (n *Node) HasAttr(local string) bool {
	for _, a := range n.Attrs {
		if a.Name.Local == local && a.Name.Space == "" {
			return true
		}
	}
	return false
}

// ResolveQName löst einen QName im Namespace-Kontext dieses Knotens auf.
// Ein QName ohne Präfix nutzt den Default-Namespace — anders als bei
// Attributnamen, wo ein fehlendes Präfix "kein Namespace" bedeutet.
func (n *Node) ResolveQName(qname string) xml.Name {
	qname = strings.TrimSpace(qname)
	if qname == "" {
		return xml.Name{}
	}
	if i := strings.IndexByte(qname, ':'); i >= 0 {
		prefix, local := qname[:i], qname[i+1:]
		return xml.Name{Space: n.ns[prefix], Local: local}
	}
	return xml.Name{Space: n.ns[""], Local: qname}
}

// LookupPrefix sucht ein Präfix, das auf die URI zeigt.
func (n *Node) LookupPrefix(uri string) (string, bool) {
	for p, u := range n.ns {
		if u == uri && p != "" {
			return p, true
		}
	}
	return "", false
}

// Namespaces liefert eine Kopie des gültigen Präfix-Mappings.
func (n *Node) Namespaces() map[string]string {
	m := make(map[string]string, len(n.ns))
	for k, v := range n.ns {
		m[k] = v
	}
	return m
}

// Child liefert das erste Kind mit passendem Namespace und Namen.
func (n *Node) Child(space, local string) *Node {
	for _, c := range n.Children {
		if c.Name.Space == space && c.Name.Local == local {
			return c
		}
	}
	return nil
}

// ChildrenNamed liefert alle Kinder mit passendem Namespace und Namen.
func (n *Node) ChildrenNamed(space, local string) []*Node {
	var out []*Node
	for _, c := range n.Children {
		if c.Name.Space == space && c.Name.Local == local {
			out = append(out, c)
		}
	}
	return out
}

// Trimmed liefert den Textinhalt ohne umgebenden Whitespace.
func (n *Node) Trimmed() string { return strings.TrimSpace(n.Text) }
