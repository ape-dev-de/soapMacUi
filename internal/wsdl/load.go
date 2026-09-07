package wsdl

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/apeters/soapmacui/internal/xdom"
	"github.com/apeters/soapmacui/internal/xsd"
)

// Fetcher holt ein Dokument. Wird injiziert, damit geschützte WSDLs mit
// denselben TLS- und Auth-Einstellungen geladen werden wie die späteren Calls.
type Fetcher interface {
	Fetch(ctx context.Context, rawurl string) ([]byte, error)
}

// FileFetcher liest von der Platte. Nützlich für Tests und Offline-Arbeit.
type FileFetcher struct{}

// Fetch liest eine Datei oder file://-URL.
func (FileFetcher) Fetch(_ context.Context, rawurl string) ([]byte, error) {
	p := rawurl
	if u, err := url.Parse(rawurl); err == nil && u.Scheme == "file" {
		p = u.Path
	}
	return os.ReadFile(p)
}

// Loader lädt ein WSDL samt aller referenzierten Dokumente.
type Loader struct {
	Fetch Fetcher

	// MaxDocs begrenzt die Anzahl geladener Dokumente. Schützt vor
	// Import-Zyklen über wechselnde URLs und vor bösartigen Schemata.
	MaxDocs int

	visited map[string]bool
	docs    int
}

// NewLoader erzeugt einen Loader.
func NewLoader(f Fetcher) *Loader {
	return &Loader{Fetch: f, MaxDocs: 200, visited: map[string]bool{}}
}

// Load lädt das WSDL unter rawurl und löst alle Imports auf.
func (l *Loader) Load(ctx context.Context, rawurl string) (*Definitions, error) {
	if l.visited == nil {
		l.visited = map[string]bool{}
	}
	if l.MaxDocs == 0 {
		l.MaxDocs = 200
	}
	d := newDefinitions()
	if err := l.loadWSDL(ctx, d, rawurl); err != nil {
		return nil, err
	}
	if len(d.Services) == 0 && len(d.Bindings) == 0 {
		return nil, fmt.Errorf("kein Service und kein Binding gefunden — ist %s wirklich ein WSDL?", rawurl)
	}
	return d, nil
}

func (l *Loader) loadWSDL(ctx context.Context, d *Definitions, rawurl string) error {
	key := normKey(rawurl)
	if l.visited[key] {
		return nil
	}
	l.visited[key] = true
	if l.docs++; l.docs > l.MaxDocs {
		return fmt.Errorf("zu viele importierte Dokumente (>%d) — vermutlich ein Import-Zyklus", l.MaxDocs)
	}

	body, err := l.Fetch.Fetch(ctx, rawurl)
	if err != nil {
		return fmt.Errorf("WSDL laden (%s): %w", rawurl, err)
	}
	root, err := xdom.Parse(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("WSDL parsen (%s): %w", rawurl, err)
	}
	if root.Name.Space != NS || root.Name.Local != "definitions" {
		return fmt.Errorf("%s ist kein wsdl:definitions, sondern <%s>", rawurl, root.Name.Local)
	}
	d.Sources = append(d.Sources, Source{URL: rawurl, Kind: "wsdl", Content: body})
	d.parseDefinitions(root, rawurl)

	// wsdl:import
	for _, imp := range root.ChildrenNamed(NS, "import") {
		loc := imp.Attr("location")
		if loc == "" {
			continue
		}
		if err := l.loadWSDL(ctx, d, resolveRef(rawurl, loc)); err != nil {
			return err
		}
	}

	// Schema-Imports aus wsdl:types
	if types := root.Child(NS, "types"); types != nil {
		for _, sc := range types.ChildrenNamed(xsd.NS, "schema") {
			if err := l.loadSchemaRefs(ctx, d, sc, rawurl); err != nil {
				return err
			}
		}
	}
	return nil
}

// loadSchemaRefs folgt xsd:import, xsd:include und xsd:redefine.
func (l *Loader) loadSchemaRefs(ctx context.Context, d *Definitions, schema *xdom.Node, base string) error {
	for _, c := range schema.Children {
		if c.Name.Space != xsd.NS {
			continue
		}
		switch c.Name.Local {
		case "import", "include", "redefine":
			loc := c.Attr("schemaLocation")
			if loc == "" {
				// xsd:import ohne schemaLocation ist zulässig: der Namespace
				// gilt dann als anderweitig bekannt. Nichts zu tun.
				continue
			}
			if err := l.loadSchema(ctx, d, resolveRef(base, loc)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (l *Loader) loadSchema(ctx context.Context, d *Definitions, rawurl string) error {
	key := normKey(rawurl)
	if l.visited[key] {
		return nil
	}
	l.visited[key] = true
	if l.docs++; l.docs > l.MaxDocs {
		return fmt.Errorf("zu viele importierte Dokumente (>%d) — vermutlich ein Import-Zyklus", l.MaxDocs)
	}

	body, err := l.Fetch.Fetch(ctx, rawurl)
	if err != nil {
		return fmt.Errorf("Schema laden (%s): %w", rawurl, err)
	}
	root, err := xdom.Parse(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("Schema parsen (%s): %w", rawurl, err)
	}
	if root.Name.Space != xsd.NS || root.Name.Local != "schema" {
		return fmt.Errorf("%s ist kein xs:schema, sondern <%s>", rawurl, root.Name.Local)
	}
	d.Sources = append(d.Sources, Source{URL: rawurl, Kind: "xsd", Content: body})
	d.Schemas.ParseSchema(root, rawurl)
	return l.loadSchemaRefs(ctx, d, root, rawurl)
}

// resolveRef löst eine relative Referenz gegen die Basis-URL auf.
// Funktioniert für http(s), file und blanke Dateipfade.
func resolveRef(base, ref string) string {
	if ref == "" {
		return ""
	}
	bu, errB := url.Parse(base)
	ru, errR := url.Parse(ref)
	if errB == nil && errR == nil && bu.Scheme != "" && bu.Scheme != "file" {
		return bu.ResolveReference(ru).String()
	}
	if ru != nil && errR == nil && ru.Scheme != "" {
		return ref
	}
	if filepath.IsAbs(ref) {
		return ref
	}
	bp := base
	if errB == nil && bu.Scheme == "file" {
		bp = bu.Path
	}
	return filepath.Join(filepath.Dir(bp), ref)
}

// normKey normalisiert eine URL für die Zyklenerkennung.
func normKey(s string) string {
	s = strings.TrimSpace(s)
	if u, err := url.Parse(s); err == nil && u.Scheme != "" {
		u.Fragment = ""
		return strings.ToLower(u.Scheme+"://"+u.Host) + u.Path + "?" + u.RawQuery
	}
	if abs, err := filepath.Abs(s); err == nil {
		return abs
	}
	return s
}
