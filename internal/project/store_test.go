package project

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestInProject deckt den Fall ab, für den die Prüfung da ist: eine
// weitergereichte project.json, die mit bodyFile aus dem Projekt herauszeigt.
func TestInProject(t *testing.T) {
	root := filepath.Clean("/tmp/projekt")

	ok := []struct {
		rel  string
		want string
	}{
		{"requests/a.xml", "/tmp/projekt/requests/a.xml"},
		{"requests/../requests/a.xml", "/tmp/projekt/requests/a.xml"},
		{"./a.xml", "/tmp/projekt/a.xml"},
		{"unterordner/tief/a.xml", "/tmp/projekt/unterordner/tief/a.xml"},
	}
	for _, c := range ok {
		got, err := InProject(root, c.rel)
		if err != nil {
			t.Errorf("InProject(%q) unerwarteter Fehler: %v", c.rel, err)
			continue
		}
		if got != filepath.Clean(c.want) {
			t.Errorf("InProject(%q) = %q, erwartet %q", c.rel, got, c.want)
		}
	}

	bad := []string{
		"",
		"../geheim.xml",
		"requests/../../geheim.xml",
		"../../../../../../Users/opfer/.ssh/id_rsa",
		"/etc/passwd",
		"/Users/opfer/.ssh/id_rsa",
	}
	for _, rel := range bad {
		if got, err := InProject(root, rel); err == nil {
			t.Errorf("InProject(%q) = %q, erwartet Ablehnung", rel, got)
		}
	}

	// Ein Nachbarordner mit gemeinsamem Namenspräfix darf nicht als "innerhalb"
	// durchgehen — der Grund, warum verglichen wird und nicht nur Präfix.
	if got, err := InProject(root, "../projekt-anders/a.xml"); err == nil {
		t.Errorf("Nachbarordner akzeptiert: %q", got)
	}

	if runtime.GOOS == "windows" {
		if _, err := InProject(`C:\projekt`, `C:geheim.xml`); err == nil {
			t.Error("laufwerksrelativer Pfad akzeptiert")
		}
	}
}

// TestOpenLehntAusbrechendesBodyFileAb prüft das Verhalten am offenen Projekt:
// der Eintrag wird entschärft, das Projekt bleibt benutzbar.
func TestOpenLehntAusbrechendesBodyFileAb(t *testing.T) {
	dir := t.TempDir()
	p, err := Create(dir, "Test")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	p.Interfaces = []*Interface{{
		ID:   "i1",
		Name: "Svc",
		Operations: []*Operation{{
			ID:   "o1",
			Name: "op",
			Requests: []*Request{{
				ID:       "r1",
				Name:     "Request 1",
				BodyFile: "../../../../../../etc/passwd",
			}},
		}},
	}}
	// An der Save-Prüfung vorbei direkt schreiben, damit genau die Datei
	// entsteht, die ein fremdes Projekt mitbrächte.
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := writeAtomic(filepath.Join(p.Dir, projectFile), data); err != nil {
		t.Fatalf("Projektdatei schreiben: %v", err)
	}

	got, err := Open(p.Dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	r := got.Interfaces[0].Operations[0].Requests[0]
	if !strings.Contains(r.Body, "abgelehnt") {
		t.Errorf("Body = %q, erwartet einen Ablehnungshinweis", r.Body)
	}
	if strings.Contains(r.Body, "root:") {
		t.Error("Inhalt einer projektfremden Datei ist im Body gelandet")
	}
	if r.BodyFile != "" {
		t.Errorf("BodyFile = %q, erwartet leer, damit Save wieder kanonisch vergibt", r.BodyFile)
	}
}

// TestOpenVerwirftFremdeFarbe: die Farbe kommt aus derselben weitergereichten
// Datei wie alles andere und darf nichts Beliebiges in die Oberfläche tragen.
func TestOpenVerwirftFremdeFarbe(t *testing.T) {
	dir := t.TempDir()
	p, err := Create(dir, "Test")
	if err != nil {
		t.Fatal(err)
	}
	for _, farbe := range []string{"'; drop table --", "<script>", "rgb(1,2,3)", "../../etc"} {
		p.Color = farbe
		data, err := json.MarshalIndent(p, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := writeAtomic(filepath.Join(p.Dir, projectFile), data); err != nil {
			t.Fatal(err)
		}
		got, err := Open(p.Dir)
		if err != nil {
			t.Fatal(err)
		}
		if got.Color != "" {
			t.Errorf("Farbe %q überlebt als %q, erwartet leer", farbe, got.Color)
		}
	}

	// Ein gültiger Palettenschlüssel bleibt dagegen stehen.
	p.Color = "teal"
	data, _ := json.MarshalIndent(p, "", "  ")
	if err := writeAtomic(filepath.Join(p.Dir, projectFile), data); err != nil {
		t.Fatal(err)
	}
	got, err := Open(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Color != "teal" {
		t.Errorf("gültige Farbe = %q, erwartet \"teal\"", got.Color)
	}
}
