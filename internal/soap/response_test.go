package soap

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestSpillAttachmentEndungAusContentType hält fest, dass die Gegenstelle
// nicht bestimmt, als was ein Anhang auf der Platte landet. Eine Content-ID
// wie "bericht.terminal" ergäbe sonst eine Datei, die der Finder ausführt.
func TestSpillAttachmentEndungAusContentType(t *testing.T) {
	dir := t.TempDir()

	cases := []struct {
		cid, ct string
		want    string
	}{
		{"bericht.terminal", "application/xml", "bericht.xml"},
		{"start.command", "text/plain", "start.txt"},
		{"skript.sh", "application/octet-stream", "skript.bin"},
		{"cover", "image/png", "cover.png"},
		{"rechnung.pdf", "application/pdf", "rechnung.pdf"},
		{"teil@example.org", "application/xml", "teil-example.xml"},
		// Ein Content-ID, von dem nach der Bereinigung nichts übrig bleibt.
		{"...", "application/xml", "attachment.xml"},
	}

	for _, c := range cases {
		got, err := spillAttachment(strings.NewReader("inhalt"), c.cid, c.ct, dir)
		if err != nil {
			t.Errorf("cid %q: %v", c.cid, err)
			continue
		}
		if got.Name != c.want {
			t.Errorf("cid %q, Content-Type %q: Name = %q, erwartet %q",
				c.cid, c.ct, got.Name, c.want)
		}
		if filepath.Dir(got.Path) != dir {
			t.Errorf("cid %q: Datei ausserhalb des Zielordners: %s", c.cid, got.Path)
		}
	}
}

// TestSpillAttachmentBleibtImOrdner deckt den Ausbruchsversuch über die
// Content-ID ab. safeName ersetzt Trenner, hier wird das Ergebnis geprüft.
func TestSpillAttachmentBleibtImOrdner(t *testing.T) {
	dir := t.TempDir()
	for _, cid := range []string{
		"../../../../etc/cron.d/pwn",
		"/etc/passwd",
		"..\\..\\windows\\system32\\x",
	} {
		got, err := spillAttachment(strings.NewReader("x"), cid, "application/xml", dir)
		if err != nil {
			t.Errorf("cid %q: %v", cid, err)
			continue
		}
		if filepath.Dir(got.Path) != dir {
			t.Errorf("cid %q: Datei landete in %s statt in %s", cid, filepath.Dir(got.Path), dir)
		}
	}
}
