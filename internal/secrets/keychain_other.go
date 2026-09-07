//go:build !darwin

package secrets

// NewSystem liefert auf Plattformen ohne angebundenen Systemspeicher den
// Sitzungsspeicher. Für Windows folgt hier die Anbindung an den
// Anmeldeinformationsspeicher.
func NewSystem() Store { return NewMemory() }
