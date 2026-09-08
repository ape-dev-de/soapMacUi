package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/apeters/soapmacui/internal/project"
	"github.com/apeters/soapmacui/internal/soap"
)

// TestAttachmentSource deckt den Weg ab, den AttachExisting nicht sieht:
// Anhänge, die schon als Eintrag in einer fremden project.json stehen.
func TestAttachmentSource(t *testing.T) {
	dir := t.TempDir()
	p := &project.Project{Dir: dir}
	attach := filepath.Join(dir, attachDirName)
	if err := os.MkdirAll(attach, 0o755); err != nil {
		t.Fatal(err)
	}

	echt := filepath.Join(attach, "cover.png")
	if err := os.WriteFile(echt, []byte("PNG"), 0o644); err != nil {
		t.Fatal(err)
	}

	fremd := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(fremd, []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Ein Symlink im Anhang-Ordner, der hinausführt — sieht der Prüfung nach
	// einem Pfad im Projekt aus, liest aber die fremde Datei.
	link := filepath.Join(attach, "harmlos.png")
	if err := os.Symlink(fremd, link); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		at   soap.Attachment
		ok   bool
	}{
		{"Datei im Anhang-Ordner", soap.Attachment{Name: "cover.png", Path: echt}, true},
		{"aus einem Script erzeugt", soap.Attachment{Name: "gen.xml", Inline: []byte("<x/>")}, true},
		{"weder Datei noch Inhalt", soap.Attachment{Name: "leer"}, false},
		{"absoluter Pfad ausserhalb", soap.Attachment{Name: "Logo.png", Path: fremd}, false},
		{"Symlink nach draussen", soap.Attachment{Name: "harmlos.png", Path: link}, false},
		{"Ausbruch über ..", soap.Attachment{
			Name: "Logo.png",
			Path: filepath.Join(attach, "..", "..", "geheim.txt"),
		}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := attachmentSource(p, c.at)
			if c.ok && err != nil {
				t.Errorf("unerwartet abgelehnt: %v", err)
			}
			if !c.ok && err == nil {
				t.Errorf("unerwartet akzeptiert: %s", c.at.Path)
			}
		})
	}
}

func TestInside(t *testing.T) {
	root := filepath.Clean("/a/b")
	drin := []string{"/a/b", "/a/b/c", "/a/b/c/d.txt", "/a/b/./c"}
	draussen := []string{"/a", "/a/bc", "/a/bc/d.txt", "/a/b/../c", "/x"}

	for _, p := range drin {
		if !inside(root, p) {
			t.Errorf("inside(%q, %q) = false, erwartet true", root, p)
		}
	}
	for _, p := range draussen {
		if inside(root, p) {
			t.Errorf("inside(%q, %q) = true, erwartet false", root, p)
		}
	}
}
