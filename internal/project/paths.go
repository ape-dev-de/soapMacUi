// Package project hält Projektmodell und Persistenz.
//
// Projektdefinitionen liegen als Dateien im Projektordner: diffbar, mergebar
// und im Repo des Kunden ablegbar. Request-Bodies stehen bewusst in eigenen
// .xml-Dateien, damit die Bytes exakt erhalten bleiben.
package project

import (
	"fmt"
	"os"
	"path/filepath"
)

// AppName ist der Ordnername unter Application Support bzw. AppData.
const AppName = "SoapMacUi"

// Paths bündelt die Standardpfade der Anwendung.
type Paths struct {
	Config        string // Einstellungen, Arbeitsbereich
	Data          string // Verlauf, Blobs
	Cache         string // Wegwerfbares
	HistoryDB     string
	BlobDir       string
	SettingsFile  string
	WorkspaceFile string
}

// StdPaths ermittelt die Standardpfade des Betriebssystems und legt sie an.
// macOS: ~/Library/Application Support/SoapMacUi
// Windows: %AppData%\SoapMacUi
func StdPaths() (Paths, error) {
	cfg, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("Konfigurationsverzeichnis ermitteln: %w", err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		cache = filepath.Join(cfg, AppName, "Cache")
	} else {
		cache = filepath.Join(cache, AppName)
	}

	base := filepath.Join(cfg, AppName)
	p := Paths{
		Config:        base,
		Data:          base,
		Cache:         cache,
		HistoryDB:     filepath.Join(base, "history.db"),
		BlobDir:       filepath.Join(base, "blobs"),
		SettingsFile:  filepath.Join(base, "settings.json"),
		WorkspaceFile: filepath.Join(base, "workspace.json"),
	}
	for _, d := range []string{p.Config, p.Cache, p.BlobDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return Paths{}, fmt.Errorf("Verzeichnis anlegen (%s): %w", d, err)
		}
	}
	return p, nil
}

// DefaultProjectsDir ist der Vorschlagsort für neue Projekte.
func (p Paths) DefaultProjectsDir() string { return filepath.Join(p.Config, "Projects") }
