package app

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/apeters/soapmacui/internal/project"
	"github.com/apeters/soapmacui/internal/soap"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// AttachInsertResult ist das Ergebnis des Ein-Klick-Anhangs.
type AttachInsertResult struct {
	Cancelled   bool             `json:"cancelled"`
	Attachment  *soap.Attachment `json:"attachment"`
	Body        string           `json:"body"`
	SelStart    int              `json:"selStart"`
	SelEnd      int              `json:"selEnd"`
	MTOMMode    string           `json:"mtomMode"`
	MTOMChanged bool             `json:"mtomChanged"`
	Reused      bool             `json:"reused"`
}

// AttachAndInsert erledigt in einem Schritt, was in SoapUI vier sind:
// Datei auswählen, in den Projektordner kopieren, Content-ID erzeugen, den
// xop:Include-Verweis an der Cursorposition einsetzen und MTOM einschalten.
//
// body kommt aus dem Editor, damit ungespeicherte Änderungen nicht verloren
// gehen; selStart/selEnd sind Cursor bzw. Markierung.
func (a *App) AttachAndInsert(projectID, requestID, endpointID, body string, selStart, selEnd int) (*AttachInsertResult, error) {
	p, err := a.get(projectID)
	if err != nil {
		return nil, err
	}
	r, _, _ := p.FindRequest(requestID)
	if r == nil {
		return nil, fmt.Errorf("request %s nicht gefunden", requestID)
	}

	src, err := a.pickFile()
	if err != nil {
		return nil, err
	}
	if src == "" {
		return &AttachInsertResult{Cancelled: true}, nil
	}
	att, reused, err := a.attachFile(p, r, src)
	if err != nil {
		return nil, err
	}

	newBody, selA, selB, err := soap.InsertXOPReference(body, selStart, selEnd, att.ID)
	if err != nil {
		return nil, err
	}
	r.Body = newBody
	r.Modified = time.Now()

	out := &AttachInsertResult{
		Attachment: att, Body: newBody, SelStart: selA, SelEnd: selB, Reused: reused,
	}

	// Ein Anhang ohne multipart-Modus ginge ins Leere. Wer explizit "inline"
	// oder "swa" gewählt hat, behält seine Wahl.
	if ep := p.FindEndpoint(endpointID); ep != nil {
		out.MTOMMode = string(ep.MTOM.Mode)
		if ep.MTOM.Mode == soap.AttachNone {
			ep.MTOM.Mode = soap.AttachMTOM
			out.MTOMMode, out.MTOMChanged = string(soap.AttachMTOM), true
		}
	}
	return out, project.Save(p)
}

// AddAttachment hängt eine Datei an, ohne den Body anzufassen.
func (a *App) AddAttachment(projectID, requestID string) (*soap.Attachment, error) {
	p, err := a.get(projectID)
	if err != nil {
		return nil, err
	}
	r, _, _ := p.FindRequest(requestID)
	if r == nil {
		return nil, fmt.Errorf("request %s nicht gefunden", requestID)
	}
	src, err := a.pickFile()
	if err != nil || src == "" {
		return nil, err
	}
	att, _, err := a.attachFile(p, r, src)
	if err != nil {
		return nil, err
	}
	r.Modified = time.Now()
	return att, project.Save(p)
}

// pickFile fragt nach einer Datei. Leerer Pfad heisst: abgebrochen.
func (a *App) pickFile() (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("Dateidialog steht nicht zur Verfügung")
	}
	return wailsrt.OpenFileDialog(a.ctx, wailsrt.OpenDialogOptions{
		Title: "Anhang auswählen",
		Filters: []wailsrt.FileFilter{
			{DisplayName: "XML", Pattern: "*.xml"},
			{DisplayName: "Alle Dateien", Pattern: "*.*"},
		},
	})
}

// attachFile kopiert die Datei in den Projektordner und hängt sie an den
// Request — es sei denn, dieselbe Datei hängt schon dran.
//
// Ohne diese Prüfung legt jeder Klick eine weitere Kopie an und setzt einen
// weiteren xop:Include. Wer zweimal klickt, weil beim ersten Mal scheinbar
// nichts passiert ist, verschickt sonst zwei Anhänge.
func (a *App) attachFile(p *project.Project, r *project.Request, src string) (*soap.Attachment, bool, error) {
	st, err := os.Stat(src)
	if err != nil {
		return nil, false, err
	}
	name := filepath.Base(src)

	for i := range r.Attachments {
		if r.Attachments[i].Name == name && r.Attachments[i].Size == st.Size() {
			return &r.Attachments[i], true, nil
		}
	}

	dstDir := filepath.Join(p.Dir, "attachments")
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, false, err
	}
	dst := filepath.Join(dstDir, name)
	if _, err := os.Stat(dst); err == nil {
		ext := filepath.Ext(name)
		dst = filepath.Join(dstDir, name[:len(name)-len(ext)]+"-"+project.NewID()[:4]+ext)
	}
	if err := copyFile(src, dst); err != nil {
		return nil, false, fmt.Errorf("anhang kopieren: %w", err)
	}
	att := soap.Attachment{
		ID:   soap.NewContentID(name),
		Name: name,
		Path: dst,
		Size: st.Size(),
	}
	r.Attachments = append(r.Attachments, att)
	return &r.Attachments[len(r.Attachments)-1], false, nil
}

// RemoveAttachment entfernt einen Anhang aus einem Request.
// Die kopierte Datei bleibt im Projektordner liegen — löschen ist eine
// bewusste Entscheidung des Nutzers, nicht ein Nebeneffekt.
func (a *App) RemoveAttachment(projectID, requestID, attachmentID string) error {
	p, err := a.get(projectID)
	if err != nil {
		return err
	}
	r, _, _ := p.FindRequest(requestID)
	if r == nil {
		return fmt.Errorf("request %s nicht gefunden", requestID)
	}
	out := r.Attachments[:0]
	for _, at := range r.Attachments {
		if at.ID != attachmentID {
			out = append(out, at)
		}
	}
	r.Attachments = out
	return project.Save(p)
}

// RevealPath zeigt eine Datei im Finder bzw. Explorer.
func (a *App) RevealPath(path string) error {
	if path == "" {
		return fmt.Errorf("kein Pfad angegeben")
	}
	// Wie bei jedem anderen Pfad aus der Oberfläche: nur Projekt- und
	// Cache-Ordner. Die Prüfung fehlte hier als einziger Stelle.
	clean, err := a.allowedPath(path)
	if err != nil {
		return err
	}
	if _, err := os.Stat(clean); err != nil {
		return fmt.Errorf("datei nicht gefunden: %s", clean)
	}
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", "-R", clean).Start()
	case "windows":
		return exec.Command("explorer", "/select,", clean).Start()
	default:
		return exec.Command("xdg-open", filepath.Dir(clean)).Start()
	}
}

// launchable listet Endungen, die LaunchServices ausführt statt anzeigt.
//
// Anhänge sind fremder Inhalt: entweder aus einer weitergereichten Projekt-
// datei oder direkt aus der Antwort einer Gegenstelle. "open" ohne -a übergibt
// die Datei an LaunchServices, und die startet bei diesen Endungen ein
// Programm — ohne Ausführbar-Bit, ohne Rückfrage.
var launchable = map[string]bool{
	".app": true, ".command": true, ".terminal": true, ".workflow": true,
	".scpt": true, ".scptd": true, ".applescript": true, ".osas": true,
	".sh": true, ".bash": true, ".zsh": true, ".csh": true, ".ksh": true,
	".py": true, ".rb": true, ".pl": true, ".php": true, ".jar": true,
	".pkg": true, ".mpkg": true, ".dmg": true, ".action": true,
	".prefpane": true, ".qlgenerator": true, ".saver": true, ".service": true,
	".definition": true, ".shortcut": true,
	// Verweisdateien: sie zeigen auf ein Ziel, das der Absender bestimmt.
	".webloc": true, ".inetloc": true, ".fileloc": true, ".url": true,
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := ioCopy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func ioCopy(dst io.Writer, src io.Reader) (int64, error) { return io.Copy(dst, src) }

// InsertReference setzt den xop:Include-Verweis eines bereits angehängten
// Anhangs an die Cursorposition. Nötig, wenn der Verweis gelöscht wurde oder
// derselbe Anhang an mehreren Stellen referenziert werden soll.
func (a *App) InsertReference(projectID, requestID, body string, selStart, selEnd int, cid string) (*AttachInsertResult, error) {
	p, err := a.get(projectID)
	if err != nil {
		return nil, err
	}
	r, _, _ := p.FindRequest(requestID)
	if r == nil {
		return nil, fmt.Errorf("request %s nicht gefunden", requestID)
	}
	known := false
	for _, at := range r.Attachments {
		if at.ID == cid {
			known = true
			break
		}
	}
	if !known {
		return nil, fmt.Errorf("kein Anhang mit der Content-ID %q an diesem Request", cid)
	}

	newBody, selA, selB, err := soap.InsertXOPReference(body, selStart, selEnd, cid)
	if err != nil {
		return nil, err
	}
	r.Body = newBody
	r.Modified = time.Now()
	return &AttachInsertResult{Body: newBody, SelStart: selA, SelEnd: selB}, project.Save(p)
}

// OrphanFile ist eine Datei im Anhang-Ordner des Projekts, auf die kein
// Request mehr verweist.
type OrphanFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// OrphanAttachments listet Dateien im Anhang-Ordner, die zu keinem Request
// mehr gehören.
//
// Anhänge zu entfernen löscht bewusst nur den Eintrag, nicht die Datei — sonst
// wäre ein Fehlklick unwiederbringlich. Ohne diese Liste blieben die Dateien
// aber unsichtbar liegen.
func (a *App) OrphanAttachments(projectID string) ([]OrphanFile, error) {
	p, err := a.get(projectID)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, itf := range p.Interfaces {
		for _, op := range itf.Operations {
			for _, r := range op.Requests {
				for _, at := range r.Attachments {
					used[filepath.Clean(at.Path)] = true
				}
			}
		}
	}

	dir := filepath.Join(p.Dir, "attachments")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []OrphanFile{}
	for _, e := range entries {
		if e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		full := filepath.Join(dir, e.Name())
		if used[filepath.Clean(full)] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, OrphanFile{Name: e.Name(), Path: full, Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// AttachExisting hängt eine bereits im Projektordner liegende Datei an einen
// Request, ohne sie erneut zu kopieren.
func (a *App) AttachExisting(projectID, requestID, path string) (*soap.Attachment, error) {
	p, err := a.get(projectID)
	if err != nil {
		return nil, err
	}
	r, _, _ := p.FindRequest(requestID)
	if r == nil {
		return nil, fmt.Errorf("request %s nicht gefunden", requestID)
	}
	// Nur Dateien aus dem Anhang-Ordner des Projekts — kein beliebiger Pfad
	// aus der Oberfläche.
	dir := filepath.Join(p.Dir, attachDirName)
	clean := filepath.Clean(path)
	if !inside(dir, clean) {
		return nil, fmt.Errorf("datei liegt nicht im Anhang-Ordner des Projekts")
	}
	st, err := os.Stat(clean)
	if err != nil {
		return nil, err
	}
	att := soap.Attachment{
		ID:   soap.NewContentID(filepath.Base(clean)),
		Name: filepath.Base(clean),
		Path: clean,
		Size: st.Size(),
	}
	r.Attachments = append(r.Attachments, att)
	r.Modified = time.Now()
	return &att, project.Save(p)
}

// AttachmentPreview ist ein Ausschnitt eines Anhangs für die Oberfläche.
type AttachmentPreview struct {
	Text      string `json:"text"`
	IsXML     bool   `json:"isXml"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
	Size      int64  `json:"size"`
}

// PreviewAttachment liefert den Anfang eines Anhangs als Text.
//
// Gelesen wird höchstens maxBytes — ein 200-MB-Anhang darf die Oberfläche
// nicht in die Knie zwingen. XML wird eingerückt, Binärdaten werden nicht
// ausgegeben, sondern nur gemeldet.
func (a *App) PreviewAttachment(path string, maxBytes int) (*AttachmentPreview, error) {
	if maxBytes <= 0 || maxBytes > 1<<20 {
		maxBytes = 64 << 10
	}
	clean, err := a.allowedPath(path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(clean)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(clean)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	buf := make([]byte, maxBytes)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return nil, err
	}
	data := buf[:n]

	out := &AttachmentPreview{Size: st.Size(), Truncated: st.Size() > int64(n)}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		out.Binary = true
		out.Text = fmt.Sprintf("Binärdatei, %d Bytes — keine Textvorschau.", st.Size())
		return out, nil
	}

	text := string(data)
	if trimmed := strings.TrimSpace(text); strings.HasPrefix(trimmed, "<") {
		out.IsXML = true
		// Nur einrücken, wenn der Ausschnitt für sich genommen wohlgeformt
		// ist — bei einem abgeschnittenen Dokument bleibt der Rohtext stehen.
		if !out.Truncated {
			if pretty, perr := soap.Reindent(text, "  "); perr == nil {
				text = pretty
			}
		}
	}
	out.Text = text
	return out, nil
}

// allowedPath lässt nur Dateien aus dem Cache oder aus einem geöffneten
// Projekt zu — kein beliebiger Pfad aus der Oberfläche.
func (a *App) allowedPath(path string) (string, error) {
	clean := filepath.Clean(path)
	roots := []string{a.paths.Cache, a.paths.Config}
	a.mu.RLock()
	for _, p := range a.open {
		roots = append(roots, p.Dir)
	}
	a.mu.RUnlock()

	for _, root := range roots {
		if root == "" {
			continue
		}
		if clean == root || strings.HasPrefix(clean, filepath.Clean(root)+string(os.PathSeparator)) {
			return clean, nil
		}
	}
	return "", fmt.Errorf("pfad liegt ausserhalb der Projekt- und Cache-Ordner")
}

// WriteAttachment schreibt einen Anhang zurück auf die Platte und aktualisiert
// die Grösse im Projekt. Nur Dateien aus Projekt- oder Cache-Ordner.
func (a *App) WriteAttachment(projectID, path, content string) error {
	clean, err := a.allowedPath(path)
	if err != nil {
		return err
	}
	if err := os.WriteFile(clean, []byte(content), 0o644); err != nil {
		return err
	}
	p, err := a.get(projectID)
	if err != nil {
		return nil // ausserhalb eines Projekts geschrieben: nichts weiter zu tun
	}
	st, err := os.Stat(clean)
	if err != nil {
		return err
	}
	changed := false
	for _, itf := range p.Interfaces {
		for _, op := range itf.Operations {
			for _, r := range op.Requests {
				for i := range r.Attachments {
					if filepath.Clean(r.Attachments[i].Path) == clean {
						r.Attachments[i].Size = st.Size()
						changed = true
					}
				}
			}
		}
	}
	if !changed {
		return nil
	}
	return project.Save(p)
}

// Editor ist ein auf dem Rechner gefundenes Textprogramm.
type Editor struct {
	Name string `json:"name"`
	App  string `json:"app"` // Name für "open -a"; leer = Standardprogramm
}

// ListEditors sucht die üblichen Texteditoren. Gefunden wird, was installiert
// ist — geraten wird nichts.
func (a *App) ListEditors() []Editor {
	out := []Editor{{Name: "Standardprogramm", App: ""}}
	if runtime.GOOS != "darwin" {
		return out
	}
	candidates := []string{
		"Sublime Text", "Visual Studio Code", "Zed", "BBEdit",
		"TextMate", "Nova", "CotEditor", "TextEdit",
	}
	roots := []string{"/Applications", filepath.Join(os.Getenv("HOME"), "Applications")}
	for _, name := range candidates {
		for _, root := range roots {
			if _, err := os.Stat(filepath.Join(root, name+".app")); err == nil {
				out = append(out, Editor{Name: name, App: name})
				break
			}
		}
	}
	return out
}

// OpenWith öffnet eine Datei im gewählten Programm.
func (a *App) OpenWith(path, app string) error {
	clean, err := a.allowedPath(path)
	if err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return a.RevealPath(clean)
	}
	if app == "" {
		// Ohne -a entscheidet LaunchServices, und bei diesen Endungen heisst
		// das: ausführen. Mit -a landet die Datei dagegen in dem Programm, das
		// ListEditors gefunden hat — einem Texteditor, der sie nur anzeigt.
		if launchable[strings.ToLower(filepath.Ext(clean))] {
			return fmt.Errorf("%s wird von macOS ausgeführt statt angezeigt — bitte ein Programm auswählen",
				filepath.Base(clean))
		}
		return exec.Command("open", clean).Start()
	}
	return exec.Command("open", "-a", app, clean).Start()
}
