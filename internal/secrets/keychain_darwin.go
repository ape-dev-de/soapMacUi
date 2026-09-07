//go:build darwin

package secrets

import (
	keychain "github.com/keybase/go-keychain"
)

// Keychain speichert Geheimnisse im Schlüsselbund von macOS.
type Keychain struct{}

// NewSystem liefert den Systemspeicher mit Sitzungsspeicher als Rückfall.
func NewSystem() Store {
	return Fallback{Primary: Keychain{}, Secondary: NewMemory()}
}

// Name beschreibt den Speicher.
func (Keychain) Name() string { return "macOS Schlüsselbund" }

// Get liest ein Passwort aus dem Schlüsselbund.
func (Keychain) Get(ref string) (string, error) {
	q := keychain.NewItem()
	q.SetSecClass(keychain.SecClassGenericPassword)
	q.SetService(Service)
	q.SetAccount(ref)
	q.SetMatchLimit(keychain.MatchLimitOne)
	q.SetReturnData(true)

	results, err := keychain.QueryItem(q)
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", ErrNotFound
	}
	return string(results[0].Data), nil
}

// Set legt ein Passwort im Schlüsselbund ab oder aktualisiert es.
func (k Keychain) Set(ref, value string) error {
	item := keychain.NewItem()
	item.SetSecClass(keychain.SecClassGenericPassword)
	item.SetService(Service)
	item.SetAccount(ref)
	item.SetLabel("SoapMacUi — " + ref)
	item.SetData([]byte(value))
	item.SetSynchronizable(keychain.SynchronizableNo)
	item.SetAccessible(keychain.AccessibleWhenUnlocked)

	err := keychain.AddItem(item)
	if err == keychain.ErrorDuplicateItem {
		q := keychain.NewItem()
		q.SetSecClass(keychain.SecClassGenericPassword)
		q.SetService(Service)
		q.SetAccount(ref)
		return keychain.UpdateItem(q, item)
	}
	return err
}

// Delete entfernt ein Passwort aus dem Schlüsselbund.
func (Keychain) Delete(ref string) error {
	q := keychain.NewItem()
	q.SetSecClass(keychain.SecClassGenericPassword)
	q.SetService(Service)
	q.SetAccount(ref)
	err := keychain.DeleteItem(q)
	if err == keychain.ErrorItemNotFound {
		return nil
	}
	return err
}
