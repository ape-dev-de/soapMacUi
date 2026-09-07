// Package secrets hält Passwörter aus den Projektdateien heraus.
//
// Gespeichert wird ausschliesslich ein Referenzschlüssel; der Wert liegt im
// Schlüsselbund des Betriebssystems. Zusätzlich gibt es einen reinen
// Sitzungsspeicher für geteilte Rechner, bei dem nichts die Anwendung verlässt.
package secrets

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// ErrNotFound meldet ein fehlendes Geheimnis.
var ErrNotFound = errors.New("geheimnis nicht gefunden")

// Service ist der Dienstname, unter dem Einträge im Schlüsselbund liegen.
const Service = "SoapMacUi"

// Store ist die Schnittstelle zu einem Geheimnisspeicher.
type Store interface {
	Get(ref string) (string, error)
	Set(ref, value string) error
	Delete(ref string) error
	// Name beschreibt den Speicher für die UI.
	Name() string
}

// Ref baut einen stabilen Referenzschlüssel.
// Der Schlüssel enthält bewusst keine Klartextdaten ausser Projekt, Endpoint
// und Benutzername — das steht ohnehin in der Projektdatei.
func Ref(projectID, endpointID, username string) string {
	clean := func(s string) string {
		return strings.NewReplacer("/", "_", ":", "_", " ", "_").Replace(s)
	}
	return fmt.Sprintf("%s/%s/%s", clean(projectID), clean(endpointID), clean(username))
}

// Memory ist ein flüchtiger Speicher: nur diese Sitzung, nichts auf der Platte.
type Memory struct {
	mu sync.RWMutex
	m  map[string]string
}

// NewMemory erzeugt einen Sitzungsspeicher.
func NewMemory() *Memory { return &Memory{m: map[string]string{}} }

// Get liest ein Geheimnis.
func (s *Memory) Get(ref string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.m[ref]
	if !ok {
		return "", ErrNotFound
	}
	return v, nil
}

// Set legt ein Geheimnis ab.
func (s *Memory) Set(ref, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[ref] = value
	return nil
}

// Delete entfernt ein Geheimnis.
func (s *Memory) Delete(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, ref)
	return nil
}

// Name beschreibt den Speicher.
func (s *Memory) Name() string { return "Nur diese Sitzung (Arbeitsspeicher)" }

// Fallback kombiniert einen bevorzugten Speicher mit einem Rückfallspeicher.
// Schlägt der Schlüsselbund fehl (etwa weil der Nutzer den Zugriff verweigert),
// bleibt die Anwendung benutzbar, statt den Request scheitern zu lassen.
type Fallback struct {
	Primary   Store
	Secondary Store
}

// Get liest bevorzugt aus dem Primärspeicher.
func (f Fallback) Get(ref string) (string, error) {
	if v, err := f.Primary.Get(ref); err == nil {
		return v, nil
	}
	return f.Secondary.Get(ref)
}

// Set schreibt in den Primärspeicher, sonst in den Rückfallspeicher.
func (f Fallback) Set(ref, value string) error {
	if err := f.Primary.Set(ref, value); err == nil {
		return nil
	}
	return f.Secondary.Set(ref, value)
}

// Delete entfernt aus beiden Speichern.
func (f Fallback) Delete(ref string) error {
	_ = f.Primary.Delete(ref)
	return f.Secondary.Delete(ref)
}

// Name beschreibt den kombinierten Speicher.
func (f Fallback) Name() string { return f.Primary.Name() }
