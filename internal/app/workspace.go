package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/apeters/soapmacui/internal/project"
)

func (a *App) loadWorkspace() {
	data, err := os.ReadFile(a.paths.WorkspaceFile)
	if err != nil {
		return
	}
	a.wsMu.Lock()
	defer a.wsMu.Unlock()
	_ = json.Unmarshal(data, &a.workspace)
}

// touchWorkspace merkt sich ein Projekt als zuletzt geöffnet.
func (a *App) touchWorkspace(p *project.Project) {
	a.wsMu.Lock()
	defer a.wsMu.Unlock()

	found := false
	for i := range a.workspace.Projects {
		if a.workspace.Projects[i].Dir == p.Dir {
			a.workspace.Projects[i].Name = p.Name
			a.workspace.Projects[i].Color = p.Color
			a.workspace.Projects[i].LastOpen = time.Now()
			found = true
			break
		}
	}
	if !found {
		a.workspace.Projects = append(a.workspace.Projects, WorkspaceEntry{
			Dir: p.Dir, Name: p.Name, Color: p.Color, LastOpen: time.Now(),
		})
	}
	if data, err := json.MarshalIndent(a.workspace, "", "  "); err == nil {
		_ = os.WriteFile(a.paths.WorkspaceFile, data, 0o644)
	}
}

// RemoveProject nimmt ein Projekt aus der Liste.
//
// toTrash legt den Ordner zusätzlich in den Papierkorb — bewusst dorthin und
// nicht endgültig gelöscht: ein Projekt enthält Requests, Anhänge und den
// WSDL-Cache, und ein Fehlklick darf das nicht vernichten.
func (a *App) RemoveProject(dir string, toTrash bool) error {
	dir = filepath.Clean(dir)

	a.wsMu.Lock()
	known := false
	keep := a.workspace.Projects[:0]
	for _, e := range a.workspace.Projects {
		if filepath.Clean(e.Dir) == dir {
			known = true
			continue
		}
		keep = append(keep, e)
	}
	a.workspace.Projects = keep
	a.wsMu.Unlock()

	if !known {
		return fmt.Errorf("projekt %s steht nicht in der Liste", dir)
	}
	a.persistWorkspace()

	// Geöffnete Instanz schliessen, damit nichts mehr zurückgeschrieben wird.
	a.mu.Lock()
	for id, p := range a.open {
		if filepath.Clean(p.Dir) == dir {
			delete(a.open, id)
		}
	}
	a.mu.Unlock()

	if !toTrash {
		return nil
	}
	return moveToTrash(dir)
}

// moveToTrash verschiebt einen Ordner in den Papierkorb des Benutzers.
func moveToTrash(dir string) error {
	if _, err := os.Stat(dir); err != nil {
		return nil // schon weg
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	trash := filepath.Join(home, ".Trash")
	if err := os.MkdirAll(trash, 0o700); err != nil {
		return err
	}
	base := filepath.Base(dir)
	target := filepath.Join(trash, base)
	if _, err := os.Stat(target); err == nil {
		target = filepath.Join(trash, base+"-"+time.Now().Format("20060102-150405"))
	}
	if err := os.Rename(dir, target); err != nil {
		return fmt.Errorf("in den Papierkorb verschieben: %w", err)
	}
	return nil
}

// persistWorkspace schreibt die Projektliste.
func (a *App) persistWorkspace() {
	a.wsMu.Lock()
	defer a.wsMu.Unlock()
	if data, err := json.MarshalIndent(a.workspace, "", "  "); err == nil {
		_ = os.WriteFile(a.paths.WorkspaceFile, data, 0o644)
	}
}
