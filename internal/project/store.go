package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/apeters/soapmacui/internal/soap"
	"github.com/apeters/soapmacui/internal/wsdl"
	"github.com/apeters/soapmacui/internal/xsd"
)

const (
	projectFile = "project.json"
	requestsDir = "requests"
	cacheDir    = "wsdl-cache"
	attachDir   = "attachments"
	scriptsDir  = "scripts"
)

// NewID erzeugt eine kurze, eindeutige Kennung.
func NewID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Create legt ein neues, leeres Projekt an.
func Create(dir, name string) (*Project, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	for _, sub := range []string{requestsDir, cacheDir, attachDir, scriptsDir} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			return nil, err
		}
	}
	now := time.Now()
	p := &Project{
		SchemaVersion: SchemaVersion,
		ID:            NewID(),
		Name:          name,
		Created:       now,
		Modified:      now,
		Variables:     map[string]string{},
		Dir:           dir,
	}
	return p, Save(p)
}

// Open liest ein Projekt samt aller Request-Bodies.
func Open(dir string) (*Project, error) {
	data, err := os.ReadFile(filepath.Join(dir, projectFile))
	if err != nil {
		return nil, fmt.Errorf("projekt öffnen: %w", err)
	}
	var p Project
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("projekt lesen: %w", err)
	}
	if p.SchemaVersion > SchemaVersion {
		return nil, fmt.Errorf("projekt hat Format-Version %d, diese Anwendung kennt nur %d — bitte aktualisieren",
			p.SchemaVersion, SchemaVersion)
	}
	p.Dir = dir
	if p.Variables == nil {
		p.Variables = map[string]string{}
	}

	for _, itf := range p.Interfaces {
		for _, op := range itf.Operations {
			for _, r := range op.Requests {
				if r.BodyFile == "" {
					continue
				}
				b, err := os.ReadFile(filepath.Join(dir, r.BodyFile))
				if err != nil {
					// Ein fehlender Body darf das Projekt nicht unbrauchbar machen.
					r.Body = fmt.Sprintf("<!-- Body-Datei %s nicht lesbar: %v -->", r.BodyFile, err)
					continue
				}
				r.Body = string(b)
			}
		}
	}
	return &p, nil
}

// Save schreibt Projektdatei und alle Request-Bodies.
//
// Die Bodies gehen byte-genau raus: kein Trim, kein Reformatieren, keine
// Normalisierung der Zeilenenden.
func Save(p *Project) error {
	if p.Dir == "" {
		return fmt.Errorf("projekt hat kein Verzeichnis")
	}
	p.Modified = time.Now()
	p.SchemaVersion = SchemaVersion

	for _, itf := range p.Interfaces {
		for _, op := range itf.Operations {
			for _, r := range op.Requests {
				if r.BodyFile == "" {
					r.BodyFile = filepath.Join(requestsDir, r.ID+".xml")
				}
				path := filepath.Join(p.Dir, r.BodyFile)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					return err
				}
				if err := writeAtomic(path, []byte(r.Body)); err != nil {
					return fmt.Errorf("body %s schreiben: %w", r.BodyFile, err)
				}
			}
		}
	}

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(p.Dir, projectFile), append(data, '\n'))
}

// writeAtomic schreibt über eine temporäre Datei, damit ein Absturz mitten im
// Schreiben keine halbe Projektdatei hinterlässt.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ImportResult beschreibt, was beim Laden eines WSDL herauskam.
type ImportResult struct {
	Interface  *Interface `json:"interface"`
	Endpoints  []string   `json:"endpoints"`
	NewOps     []string   `json:"newOperations"`
	GoneOps    []string   `json:"removedOperations"`
	ChangedOps []string   `json:"changedOperations"`
}

// LoadWSDL lädt ein WSDL und legt daraus eine Schnittstelle an.
// Existiert bereits eine Schnittstelle mit derselben URL, wird neu indiziert:
// bestehende Requests bleiben erhalten, Änderungen werden gemeldet.
func LoadWSDL(ctx context.Context, p *Project, url string, fetch wsdl.Fetcher, gen xsd.GenOptions) (*ImportResult, error) {
	defs, err := wsdl.NewLoader(fetch).Load(ctx, url)
	if err != nil {
		return nil, err
	}

	existing := p.interfaceByURL(url)
	itf := &Interface{
		ID:       NewID(),
		Name:     defs.Name,
		WSDLURL:  url,
		LoadedAt: time.Now(),
		TargetNS: defs.TargetNS,
	}
	if itf.Name == "" {
		itf.Name = firstServiceName(defs)
	}
	if existing != nil {
		itf.ID, itf.Name = existing.ID, existing.Name
	}

	// Quelldokumente im Projektordner ablegen — damit ist offline arbeiten und
	// ein späterer Diff gegen den alten Stand möglich.
	if err := os.MkdirAll(filepath.Join(p.Dir, cacheDir), 0o755); err != nil {
		return nil, err
	}
	for _, src := range defs.Sources {
		name := cacheName(src.URL, src.Kind)
		rel := filepath.Join(cacheDir, name)
		if err := writeAtomic(filepath.Join(p.Dir, rel), src.Content); err != nil {
			return nil, fmt.Errorf("WSDL-Cache schreiben: %w", err)
		}
		itf.CacheFiles = append(itf.CacheFiles, rel)
	}

	builder := soap.NewBuilder(defs, gen)
	res := &ImportResult{Endpoints: defs.Endpoints()}

	prev := map[string]*Operation{}
	if existing != nil {
		for _, op := range existing.Operations {
			prev[opKey(op.Service, op.Port, op.Name)] = op
		}
	}
	seen := map[string]bool{}

	for _, v := range defs.Operations() {
		key := opKey(v.Service, v.Port, v.Operation)
		seen[key] = true

		op := &Operation{
			ID:           NewID(),
			Name:         v.Operation,
			Service:      v.Service,
			Port:         v.Port,
			Binding:      v.Binding,
			SOAPAction:   v.SOAPAction,
			Style:        v.Style,
			Use:          v.Use,
			Version:      string(v.Version),
			Doc:          v.Doc,
			SuggestMTOM:  v.SuggestMTOM,
			WSDLEndpoint: v.Endpoint,
		}

		sample, err := builder.BuildRequest(v)
		if err != nil {
			sample = fmt.Sprintf("<!-- Beispiel-Request konnte nicht erzeugt werden: %v -->", err)
		}

		if old, ok := prev[key]; ok {
			// Reindex: bestehende Requests übernehmen, nichts überschreiben.
			op.ID = old.ID
			op.Requests = old.Requests
			if len(op.Requests) > 0 && op.Requests[0].Body != sample {
				res.ChangedOps = append(res.ChangedOps, v.Operation)
			}
		} else {
			res.NewOps = append(res.NewOps, v.Operation)
		}
		if len(op.Requests) == 0 {
			op.Requests = []*Request{{
				ID:       NewID(),
				Name:     "Request 1",
				Body:     sample,
				Modified: time.Now(),
			}}
		}
		itf.Operations = append(itf.Operations, op)
	}

	// Entfallene Operationen bleiben erhalten, nur markiert. Sie wegzuwerfen
	// hiesse, die daran hängenden Requests und Anhänge stillschweigend zu
	// löschen — und ein Reindex darf niemals Daten vernichten.
	var retired []string
	for key, old := range prev {
		if seen[key] {
			continue
		}
		old.Retired = true
		itf.Operations = append(itf.Operations, old)
		retired = append(retired, key)
		res.GoneOps = append(res.GoneOps, old.Name)
	}
	sort.Strings(retired)

	// Endpoints aus dem WSDL ergänzen, vorhandene nicht anfassen.
	for _, ep := range res.Endpoints {
		if p.endpointByURL(ep) == nil {
			p.Endpoints = append(p.Endpoints, NewEndpoint(NewID(), endpointLabel(ep), ep))
		}
	}
	if len(p.Environments) == 0 && len(p.Endpoints) > 0 {
		p.Environments = append(p.Environments, &Environment{
			ID: NewID(), Name: "Default", EndpointID: p.Endpoints[0].ID, Variables: map[string]string{},
		})
	}

	if existing != nil {
		for i, e := range p.Interfaces {
			if e == existing {
				p.Interfaces[i] = itf
				break
			}
		}
	} else {
		p.Interfaces = append(p.Interfaces, itf)
	}
	res.Interface = itf
	return res, Save(p)
}

func (p *Project) interfaceByURL(url string) *Interface {
	for _, i := range p.Interfaces {
		if i.WSDLURL == url {
			return i
		}
	}
	return nil
}

func (p *Project) endpointByURL(url string) *Endpoint {
	for _, e := range p.Endpoints {
		if e.URL == url {
			return e
		}
	}
	return nil
}

func opKey(service, port, op string) string { return service + "/" + port + "/" + op }

func firstServiceName(d *wsdl.Definitions) string {
	for _, op := range d.Operations() {
		return op.Service
	}
	return "Interface"
}

// endpointLabel macht aus einer URL eine kurze, sprechende Bezeichnung.
func endpointLabel(raw string) string {
	s := strings.TrimPrefix(strings.TrimPrefix(raw, "https://"), "http://")
	if i := strings.IndexAny(s, "/?"); i > 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '.'); i > 0 {
		s = s[:i]
	}
	if s == "" {
		return "Endpoint"
	}
	return s
}

var unsafeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// cacheName macht aus einer Quell-URL einen stabilen, sicheren Dateinamen.
func cacheName(rawurl, kind string) string {
	base := rawurl
	if i := strings.LastIndexAny(base, "/"); i >= 0 && i < len(base)-1 {
		base = base[i+1:]
	}
	base = unsafeName.ReplaceAllString(base, "_")
	if base == "" || base == "_" {
		base = kind
	}
	sum := sha(rawurl)
	if !strings.HasSuffix(strings.ToLower(base), "."+kind) {
		base += "." + kind
	}
	return sum[:8] + "-" + base
}

func sha(s string) string {
	var b [8]byte
	h := uint64(1469598103934665603)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	for i := range b {
		b[i] = byte(h >> (8 * i))
	}
	return hex.EncodeToString(b[:])
}
