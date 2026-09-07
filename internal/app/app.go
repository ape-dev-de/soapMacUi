// Package app ist die Brücke zwischen Oberfläche und Kern.
//
// Alles, was gross werden kann — Request- und Response-Bodies, Anhänge —
// bleibt hier in Go. Die Oberfläche bekommt Kennungen und Metadaten, und den
// Text nur für den gerade sichtbaren Tab.
package app

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/apeters/soapmacui/internal/project"
	"github.com/apeters/soapmacui/internal/secrets"
	"github.com/apeters/soapmacui/internal/soap"
	"github.com/apeters/soapmacui/internal/xsd"
)

// App hält den Anwendungszustand.
type App struct {
	ctx     context.Context
	paths   project.Paths
	secrets secrets.Store

	mu   sync.RWMutex
	open map[string]*project.Project

	wsMu      sync.Mutex
	workspace Workspace
}

// Workspace merkt sich, welche Projekte bekannt sind.
type Workspace struct {
	Projects []WorkspaceEntry `json:"projects"`
}

// WorkspaceEntry ist ein bekannter Projektpfad.
type WorkspaceEntry struct {
	Dir      string    `json:"dir"`
	Name     string    `json:"name"`
	LastOpen time.Time `json:"lastOpen"`
}

// New erzeugt die Anwendung.
func New() (*App, error) {
	p, err := project.StdPaths()
	if err != nil {
		return nil, err
	}
	a := &App{paths: p, secrets: secrets.NewSystem(), open: map[string]*project.Project{}}
	a.loadWorkspace()
	return a, nil
}

// Startup wird von Wails beim Start aufgerufen.
func (a *App) Startup(ctx context.Context) { a.ctx = ctx }

// -------------------------------------------------------------------------
// Sichten für die Oberfläche

// ProjectRef ist ein Eintrag in der Projektliste.
type ProjectRef struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Dir      string `json:"dir"`
	Open     bool   `json:"open"`
	LastOpen string `json:"lastOpen"`
}

// ProjectView ist der Projektbaum ohne Bodies.
type ProjectView struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Dir          string             `json:"dir"`
	Interfaces   []InterfaceView    `json:"interfaces"`
	Endpoints    []project.Endpoint `json:"endpoints"`
	Environments []EnvView          `json:"environments"`
	Variables    map[string]string  `json:"variables"`
}

// EnvView ist eine Umgebung.
type EnvView struct {
	ID         string            `json:"id"`
	Name       string            `json:"name"`
	EndpointID string            `json:"endpointId"`
	Variables  map[string]string `json:"variables"`
}

// InterfaceView ist eine Schnittstelle mit ihren Operationen.
type InterfaceView struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	WSDLURL    string          `json:"wsdlUrl"`
	LoadedAt   string          `json:"loadedAt"`
	Operations []OperationView `json:"operations"`
}

// OperationView ist eine Operation mit ihren gespeicherten Requests.
type OperationView struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	Service     string       `json:"service"`
	Port        string       `json:"port"`
	SOAPAction  string       `json:"soapAction"`
	Style       string       `json:"style"`
	Version     string       `json:"soapVersion"`
	Doc         string       `json:"documentation"`
	SuggestMTOM bool         `json:"suggestMtom"`
	Retired     bool         `json:"retired"`
	Requests    []RequestRef `json:"requests"`
}

// RequestRef ist ein Request ohne Body.
type RequestRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Size int    `json:"size"`
}

// RequestView ist ein Request samt Body — nur für den sichtbaren Tab.
type RequestView struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Body        string            `json:"body"`
	Headers     []project.KV      `json:"headers"`
	Attachments []soap.Attachment `json:"attachments"`
	OperationID string            `json:"operationId"`
	Operation   string            `json:"operation"`
	SOAPAction  string            `json:"soapAction"`
	Version     string            `json:"soapVersion"`
	SuggestMTOM bool              `json:"suggestMtom"`

	Wire *soap.WireOptions `json:"wire"`
	MTOM *soap.MTOMOptions `json:"mtom"`
}

// -------------------------------------------------------------------------
// Projekte

// ListProjects liefert alle bekannten Projekte.
func (a *App) ListProjects() []ProjectRef {
	a.wsMu.Lock()
	entries := append([]WorkspaceEntry(nil), a.workspace.Projects...)
	a.wsMu.Unlock()

	// Bewusst keine Sortierung: würde die Liste nach jedem Öffnen neu ordnen,
	// und der Eintrag springt unter dem Mauszeiger weg.

	out := make([]ProjectRef, 0, len(entries))
	for _, e := range entries {
		ref := ProjectRef{Name: e.Name, Dir: e.Dir, LastOpen: e.LastOpen.Format(time.RFC3339)}
		a.mu.RLock()
		for id, p := range a.open {
			if p.Dir == e.Dir {
				ref.ID, ref.Open = id, true
			}
		}
		a.mu.RUnlock()
		out = append(out, ref)
	}
	return out
}

// CreateProject legt ein Projekt im Standardordner an.
func (a *App) CreateProject(name string) (*ProjectView, error) {
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("bitte einen Projektnamen angeben")
	}
	dir := filepath.Join(a.paths.DefaultProjectsDir(), safeDirName(name))
	if _, err := os.Stat(dir); err == nil {
		dir += "-" + project.NewID()[:4]
	}
	p, err := project.Create(dir, name)
	if err != nil {
		return nil, err
	}
	a.register(p)
	return a.view(p), nil
}

// OpenProject öffnet ein Projekt aus einem Ordner.
func (a *App) OpenProject(dir string) (*ProjectView, error) {
	p, err := project.Open(dir)
	if err != nil {
		return nil, err
	}
	a.register(p)
	return a.view(p), nil
}

// GetProject liefert den Projektbaum.
func (a *App) GetProject(id string) (*ProjectView, error) {
	p, err := a.get(id)
	if err != nil {
		return nil, err
	}
	return a.view(p), nil
}

// CloseProject schliesst ein Projekt und gibt seinen Speicher frei.
func (a *App) CloseProject(id string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.open, id)
	return nil
}

// LoadResult meldet das Ergebnis eines WSDL-Ladevorgangs.
type LoadResult struct {
	Project    *ProjectView `json:"project"`
	Interface  string       `json:"interface"`
	Endpoints  []string     `json:"endpoints"`
	NewOps     []string     `json:"newOperations"`
	GoneOps    []string     `json:"removedOperations"`
	ChangedOps []string     `json:"changedOperations"`
	Operations int          `json:"operationCount"`
}

// LoadWSDL lädt oder reindiziert ein WSDL.
func (a *App) LoadWSDL(projectID, wsdlURL string, insecure bool) (*LoadResult, error) {
	p, err := a.get(projectID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(wsdlURL) == "" {
		return nil, fmt.Errorf("bitte eine WSDL-Adresse angeben")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	res, err := project.LoadWSDL(ctx, p, strings.TrimSpace(wsdlURL), &fetcher{insecure: insecure}, xsd.GenOptions{})
	if err != nil {
		return nil, err
	}
	a.touchWorkspace(p)

	count := 0
	if res.Interface != nil {
		count = len(res.Interface.Operations)
	}
	name := ""
	if res.Interface != nil {
		name = res.Interface.Name
	}
	return &LoadResult{
		Project: a.view(p), Interface: name, Endpoints: res.Endpoints,
		NewOps: res.NewOps, GoneOps: res.GoneOps, ChangedOps: res.ChangedOps,
		Operations: count,
	}, nil
}

// GetRequest liefert einen Request samt Body.
func (a *App) GetRequest(projectID, requestID string) (*RequestView, error) {
	p, err := a.get(projectID)
	if err != nil {
		return nil, err
	}
	r, op, _ := p.FindRequest(requestID)
	if r == nil {
		return nil, fmt.Errorf("request %s nicht gefunden", requestID)
	}
	return &RequestView{
		ID: r.ID, Name: r.Name, Body: r.Body, Headers: r.Headers,
		Attachments: r.Attachments, OperationID: op.ID, Operation: op.Name,
		SOAPAction: op.SOAPAction, Version: op.Version, SuggestMTOM: op.SuggestMTOM,
		Wire: r.Wire, MTOM: r.MTOM,
	}, nil
}

// SaveRequestBody speichert den Body byte-genau so, wie er im Editor steht.
func (a *App) SaveRequestBody(projectID, requestID, body string) error {
	p, err := a.get(projectID)
	if err != nil {
		return err
	}
	r, _, _ := p.FindRequest(requestID)
	if r == nil {
		return fmt.Errorf("request %s nicht gefunden", requestID)
	}
	r.Body, r.Modified = body, time.Now()
	return project.Save(p)
}

// Send führt einen Aufruf aus.
func (a *App) Send(projectID, requestID, endpointID string) *SendResult {
	p, err := a.get(projectID)
	if err != nil {
		return &SendResult{Error: err.Error()}
	}
	r, op, _ := p.FindRequest(requestID)
	if r == nil {
		return &SendResult{Error: "Request nicht gefunden"}
	}
	ep := p.FindEndpoint(endpointID)
	if ep == nil {
		return &SendResult{Error: "Endpoint nicht gefunden"}
	}
	timeout := ep.HTTP.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout+10*time.Second)
	defer cancel()
	return a.send(ctx, p, r, op, ep)
}

// SendToAll schickt denselben Request an mehrere Endpoints — praktisch für den
// Vergleich zwischen Test- und Produktivsystem.
func (a *App) SendToAll(projectID, requestID string, endpointIDs []string) []*SendResult {
	out := make([]*SendResult, 0, len(endpointIDs))
	for _, id := range endpointIDs {
		out = append(out, a.Send(projectID, requestID, id))
	}
	return out
}

// UpdateEndpoint übernimmt Änderungen an einem Endpoint.
func (a *App) UpdateEndpoint(projectID string, ep project.Endpoint) error {
	p, err := a.get(projectID)
	if err != nil {
		return err
	}
	cur := p.FindEndpoint(ep.ID)
	if cur == nil {
		return fmt.Errorf("endpoint %s nicht gefunden", ep.ID)
	}
	*cur = ep
	return project.Save(p)
}

// AddEndpoint legt einen weiteren Endpoint an.
func (a *App) AddEndpoint(projectID, name, url string) (*ProjectView, error) {
	p, err := a.get(projectID)
	if err != nil {
		return nil, err
	}
	p.Endpoints = append(p.Endpoints, project.NewEndpoint(project.NewID(), name, url))
	if err := project.Save(p); err != nil {
		return nil, err
	}
	return a.view(p), nil
}

// SetSecret legt ein Passwort im Schlüsselbund ab und verknüpft es mit dem
// Endpoint. Das Passwort selbst wird nie in der Projektdatei gespeichert.
func (a *App) SetSecret(projectID, endpointID, password string) error {
	p, err := a.get(projectID)
	if err != nil {
		return err
	}
	ep := p.FindEndpoint(endpointID)
	if ep == nil {
		return fmt.Errorf("endpoint %s nicht gefunden", endpointID)
	}
	ref := secrets.Ref(p.ID, ep.ID, ep.Auth.Username)
	if err := a.secrets.Set(ref, password); err != nil {
		return fmt.Errorf("schlüsselbund: %w", err)
	}
	ep.Auth.SecretRef = ref
	return project.Save(p)
}

// SecretStoreName beschreibt, wo Passwörter landen.
func (a *App) SecretStoreName() string { return a.secrets.Name() }

// Paths liefert die Standardpfade für die Oberfläche.
func (a *App) Paths() map[string]string {
	return map[string]string{
		"config":   a.paths.Config,
		"cache":    a.paths.Cache,
		"projects": a.paths.DefaultProjectsDir(),
		"history":  a.paths.HistoryDB,
	}
}

// FormatXML formatiert XML für den Editor. Bewusst eine eigene Aktion:
// automatisch formatiert wird nie.
func (a *App) FormatXML(body string) (string, error) { return soap.Reindent(body, "  ") }

// -------------------------------------------------------------------------
// intern

func (a *App) register(p *project.Project) {
	a.mu.Lock()
	a.open[p.ID] = p
	a.mu.Unlock()
	a.touchWorkspace(p)
}

func (a *App) get(id string) (*project.Project, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	p, ok := a.open[id]
	if !ok {
		return nil, fmt.Errorf("projekt %s ist nicht geöffnet", id)
	}
	return p, nil
}

func (a *App) view(p *project.Project) *ProjectView {
	v := &ProjectView{ID: p.ID, Name: p.Name, Dir: p.Dir, Variables: p.Variables}
	for _, e := range p.Endpoints {
		v.Endpoints = append(v.Endpoints, *e)
	}
	for _, e := range p.Environments {
		v.Environments = append(v.Environments, EnvView{ID: e.ID, Name: e.Name, EndpointID: e.EndpointID, Variables: e.Variables})
	}
	for _, itf := range p.Interfaces {
		iv := InterfaceView{ID: itf.ID, Name: itf.Name, WSDLURL: itf.WSDLURL, LoadedAt: itf.LoadedAt.Format(time.RFC3339)}
		for _, op := range itf.Operations {
			ov := OperationView{
				ID: op.ID, Name: op.Name, Service: op.Service, Port: op.Port,
				SOAPAction: op.SOAPAction, Style: op.Style, Version: op.Version,
				Doc: op.Doc, SuggestMTOM: op.SuggestMTOM, Retired: op.Retired,
			}
			for _, r := range op.Requests {
				ov.Requests = append(ov.Requests, RequestRef{ID: r.ID, Name: r.Name, Size: len(r.Body)})
			}
			iv.Operations = append(iv.Operations, ov)
		}
		v.Interfaces = append(v.Interfaces, iv)
	}
	return v
}

func safeDirName(s string) string {
	s = strings.TrimSpace(s)
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		case r == ' ':
			return '-'
		default:
			return '-'
		}
	}, s)
}

// fetcher lädt WSDL-Dokumente über HTTP.
type fetcher struct{ insecure bool }

func (f *fetcher) Fetch(ctx context.Context, rawurl string) ([]byte, error) {
	u, err := url.Parse(rawurl)
	if err != nil || u.Scheme == "" || u.Scheme == "file" {
		return readLocal(rawurl)
	}
	tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: f.insecure, MinVersion: tls.VersionTLS10}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 30 * time.Second}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/xml, application/xml, */*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %s beim Laden von %s", resp.Status, rawurl)
	}
	// 32 MiB reichen für jedes realistische WSDL und schützen vor Ausreissern.
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}

func readLocal(p string) ([]byte, error) {
	if u, err := url.Parse(p); err == nil && u.Scheme == "file" {
		p = u.Path
	}
	return os.ReadFile(p)
}

// SaveRequestHeaders speichert die Kopfzeilen eines Requests.
func (a *App) SaveRequestHeaders(projectID, requestID string, headers []project.KV) error {
	p, err := a.get(projectID)
	if err != nil {
		return err
	}
	r, _, _ := p.FindRequest(requestID)
	if r == nil {
		return fmt.Errorf("request %s nicht gefunden", requestID)
	}
	r.Headers = headers
	r.Modified = time.Now()
	return project.Save(p)
}
