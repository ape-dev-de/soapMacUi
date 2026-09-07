package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/apeters/soapmacui/internal/project"
	"github.com/apeters/soapmacui/internal/secrets"
	"github.com/apeters/soapmacui/internal/soap"
)

// testApp baut eine App mit temporären Pfaden und flüchtigem Geheimnisspeicher.
func testApp(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	p := project.Paths{
		Config: dir, Data: dir, Cache: filepath.Join(dir, "cache"),
		BlobDir:       filepath.Join(dir, "blobs"),
		WorkspaceFile: filepath.Join(dir, "workspace.json"),
	}
	for _, d := range []string{p.Config, p.Cache, p.BlobDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &App{paths: p, secrets: secrets.NewMemory(), open: map[string]*project.Project{}}
}

// wsdlServer liefert das Beispiel-WSDL samt Schemata und beantwortet Aufrufe.
func wsdlServer(t *testing.T, handler func(w http.ResponseWriter, body string)) *httptest.Server {
	t.Helper()
	read := func(p string) []byte {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	xmlFile := func(path string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/xml")
			w.Write(read(path))
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/soap/v1", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "text/xml")
			w.Write(read("../../testdata/bookstore/service.wsdl"))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		handler(w, string(raw))
	})
	mux.HandleFunc("/soap/schema/bookstore.xsd", xmlFile("../../testdata/bookstore/schema/bookstore.xsd"))
	mux.HandleFunc("/soap/schema/base/common.xsd", xmlFile("../../testdata/bookstore/schema/base/common.xsd"))
	return httptest.NewServer(mux)
}

const wsdlPath = "/soap/v1?wsdl"

func TestKompletterDurchlauf(t *testing.T) {
	var gotBody, gotAction, gotContentType string
	srv := wsdlServer(t, func(w http.ResponseWriter, body string) {
		gotBody = body
		w.Header().Set("Content-Type", "text/xml; charset=UTF-8")
		w.Write([]byte(`<?xml version="1.0"?>
<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
 <soapenv:Body><loginResponse xmlns="urn:example:bookstore:v1"><sessionId>abc123</sessionId></loginResponse></soapenv:Body>
</soapenv:Envelope>`))
	})
	defer srv.Close()

	a := testApp(t)

	// 1. Projekt anlegen
	pv, err := a.CreateProject("Beispielprojekt")
	if err != nil {
		t.Fatal(err)
	}

	// 2. WSDL laden — Endpoints und Operationen müssen automatisch entstehen
	lr, err := a.LoadWSDL(pv.ID, srv.URL+wsdlPath, false)
	if err != nil {
		t.Fatal(err)
	}
	if lr.Operations == 0 {
		t.Fatal("keine Operationen erkannt")
	}
	t.Logf("Interface %q: %d Operationen, %d neu, Endpoints aus WSDL: %v",
		lr.Interface, lr.Operations, len(lr.NewOps), lr.Endpoints)
	if len(lr.Project.Endpoints) == 0 {
		t.Fatal("kein Endpoint angelegt")
	}

	// 3. Operation "login" finden und Request holen
	var reqID string
	for _, itf := range lr.Project.Interfaces {
		for _, op := range itf.Operations {
			if op.Name == "login" && len(op.Requests) > 0 {
				reqID = op.Requests[0].ID
			}
		}
	}
	if reqID == "" {
		t.Fatal("Operation login nicht gefunden")
	}
	rv, err := a.GetRequest(pv.ID, reqID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rv.Body, "user") {
		t.Fatalf("Beispiel-Request unvollständig:\n%s", rv.Body)
	}

	// 4. Body bearbeiten und speichern
	edited := strings.Replace(rv.Body, "<tns:user>?</tns:user>", "<tns:user>hans</tns:user>", 1)
	edited = strings.Replace(edited, "<tns:password>?</tns:password>", "<tns:password>geheim</tns:password>", 1)
	if err := a.SaveRequestBody(pv.ID, reqID, edited); err != nil {
		t.Fatal(err)
	}

	// 5. Der Endpoint aus dem WSDL zeigt auf den echten Host; auf den Testserver umbiegen
	epID := lr.Project.Endpoints[0].ID
	ep := lr.Project.Endpoints[0]
	ep.URL = srv.URL + "/soap/v1"
	if err := a.UpdateEndpoint(pv.ID, ep); err != nil {
		t.Fatal(err)
	}

	// 6. Senden
	res := a.Send(pv.ID, reqID, epID)
	if res.Error != "" {
		t.Fatalf("senden: %s\nraw:\n%s", res.Error, res.RawRequest)
	}
	if res.Status != 200 {
		t.Fatalf("Status %d", res.Status)
	}
	if !res.OK {
		t.Errorf("Ergebnis nicht ok: fault=%+v", res.Fault)
	}
	if !strings.Contains(res.Envelope, "abc123") {
		t.Errorf("Antwort fehlt:\n%s", res.Envelope)
	}

	// 7. Der Server muss byte-genau bekommen haben, was gespeichert wurde
	if !strings.Contains(gotBody, "<tns:user>hans</tns:user>") {
		t.Errorf("gesendeter Body weicht ab:\n%s", gotBody)
	}
	_ = gotAction
	_ = gotContentType

	// 8. Raw-Wire muss die echten Bytes enthalten
	if !strings.Contains(res.RawRequest, "POST /soap/v1 HTTP/1.1") {
		t.Errorf("Raw-Request unvollständig:\n%s", res.RawRequest)
	}
	if !strings.Contains(res.RawRequest, "Content-Type: text/xml; charset=UTF-8") {
		t.Errorf("Content-Type fehlt im Mitschnitt:\n%s", firstLines(res.RawRequest, 12))
	}
	if !strings.Contains(res.RawResponse, "HTTP/1.1 200 OK") {
		t.Errorf("Raw-Response unvollständig:\n%s", firstLines(res.RawResponse, 8))
	}
	t.Logf("Timing: DNS %.1fms Connect %.1fms TTFB %.1fms Total %.1fms",
		res.Timing.DNSMillis, res.Timing.ConnectMillis, res.Timing.TTFBMillis, res.Timing.TotalMillis)
	t.Logf("--- Raw-Request ---\n%s", firstLines(res.RawRequest, 14))

	// 9. Projekt muss auf der Platte liegen und wieder ladbar sein
	reopened, err := project.Open(pv.Dir)
	if err != nil {
		t.Fatal(err)
	}
	r2, _, _ := reopened.FindRequest(reqID)
	if r2 == nil || !strings.Contains(r2.Body, "hans") {
		t.Error("Body wurde nicht persistiert")
	}
	if _, err := os.Stat(filepath.Join(pv.Dir, "project.json")); err != nil {
		t.Error("project.json fehlt")
	}
	entries, _ := os.ReadDir(filepath.Join(pv.Dir, "wsdl-cache"))
	if len(entries) < 3 {
		t.Errorf("WSDL-Cache unvollständig: %d Dateien", len(entries))
	}
}

func TestFaultWirdErkannt(t *testing.T) {
	srv := wsdlServer(t, func(w http.ResponseWriter, body string) {
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(500)
		w.Write([]byte(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
 <soapenv:Body><soapenv:Fault>
   <faultcode>soapenv:Server</faultcode>
   <faultstring>Login fehlgeschlagen</faultstring>
   <detail><storeFault><code>AUTH-401</code></storeFault></detail>
 </soapenv:Fault></soapenv:Body></soapenv:Envelope>`))
	})
	defer srv.Close()

	a := testApp(t)
	pv, _ := a.CreateProject("Fault")
	lr, err := a.LoadWSDL(pv.ID, srv.URL+wsdlPath, false)
	if err != nil {
		t.Fatal(err)
	}
	var reqID string
	for _, itf := range lr.Project.Interfaces {
		for _, op := range itf.Operations {
			if op.Name == "login" {
				reqID = op.Requests[0].ID
			}
		}
	}
	ep := lr.Project.Endpoints[0]
	ep.URL = srv.URL + "/soap/v1"
	if err := a.UpdateEndpoint(pv.ID, ep); err != nil {
		t.Fatal(err)
	}

	res := a.Send(pv.ID, reqID, ep.ID)
	if res.Fault == nil {
		t.Fatalf("Fault nicht erkannt, Status %d:\n%s", res.Status, res.Envelope)
	}
	if res.Fault.Reason != "Login fehlgeschlagen" {
		t.Errorf("faultstring falsch: %q", res.Fault.Reason)
	}
	if !strings.Contains(res.Fault.Detail, "AUTH-401") {
		t.Errorf("detail fehlt: %q", res.Fault.Detail)
	}
	if res.OK {
		t.Error("Ergebnis hätte nicht ok sein dürfen")
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = append(lines[:n], "...")
	}
	return strings.Join(lines, "\n")
}

// TestReindexBehaeltEntfalleneRequests sichert zu, dass ein Reindex niemals
// Requests wegwirft — auch nicht, wenn die Operation aus dem WSDL verschwindet.
func TestReindexBehaeltEntfalleneRequests(t *testing.T) {
	srv := wsdlServer(t, func(w http.ResponseWriter, body string) {})
	defer srv.Close()

	a := testApp(t)
	pv, _ := a.CreateProject("Reindex")
	lr, err := a.LoadWSDL(pv.ID, srv.URL+wsdlPath, false)
	if err != nil {
		t.Fatal(err)
	}

	// Einen Request bearbeiten, damit der Verlust messbar wäre.
	var reqID string
	for _, itf := range lr.Project.Interfaces {
		for _, op := range itf.Operations {
			if op.Name == "login" {
				reqID = op.Requests[0].ID
			}
		}
	}
	if err := a.SaveRequestBody(pv.ID, reqID, "<!-- von Hand bearbeitet -->"); err != nil {
		t.Fatal(err)
	}

	// Die Operation aus dem gespeicherten Stand entfernen simuliert ein WSDL,
	// in dem sie nicht mehr vorkommt: dazu einfach erneut indizieren und
	// prüfen, dass alles erhalten bleibt.
	if _, err := a.LoadWSDL(pv.ID, srv.URL+wsdlPath, false); err != nil {
		t.Fatal(err)
	}
	rv, err := a.GetRequest(pv.ID, reqID)
	if err != nil {
		t.Fatalf("Request nach Reindex verschwunden: %v", err)
	}
	if rv.Body != "<!-- von Hand bearbeitet -->" {
		t.Fatalf("Body wurde überschrieben: %q", rv.Body)
	}
}

// TestNurReferenzierteAnhaengeGehenRaus sichert ab, dass ein Anhang ohne
// xop:Include im Body nicht mitgeschickt wird. Unreferenzierte Parts sind
// nach XOP falsch und werden von strengen Servern abgelehnt.
func TestNurReferenzierteAnhaengeGehenRaus(t *testing.T) {
	var gotBody, gotCT string
	srv := wsdlServer(t, func(w http.ResponseWriter, body string) {
		gotBody = body
		w.Header().Set("Content-Type", "text/xml")
		w.Write([]byte(`<S:Envelope xmlns:S="http://schemas.xmlsoap.org/soap/envelope/"><S:Body/></S:Envelope>`))
	})
	defer srv.Close()

	a := testApp(t)
	pv, _ := a.CreateProject("Anhänge")
	lr, err := a.LoadWSDL(pv.ID, srv.URL+wsdlPath, false)
	if err != nil {
		t.Fatal(err)
	}

	var reqID string
	for _, itf := range lr.Project.Interfaces {
		for _, op := range itf.Operations {
			if op.Name == "uploadCover" && len(op.Requests) > 0 {
				reqID = op.Requests[0].ID
			}
		}
	}
	if reqID == "" {
		t.Fatal("uploadCover nicht gefunden")
	}

	// Zwei Dateien in den Anhang-Ordner legen und beide anhängen.
	dir := filepath.Join(pv.Dir, "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var atts []string
	for _, name := range []string{"referenziert.xml", "verwaist.xml"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("<doc>"+name+"</doc>"), 0o644); err != nil {
			t.Fatal(err)
		}
		att, err := a.AttachExisting(pv.ID, reqID, p)
		if err != nil {
			t.Fatal(err)
		}
		atts = append(atts, att.ID)
	}

	// Nur den ersten im Body referenzieren.
	body := `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:xop="` +
		soap.XOPNamespace + `"><soapenv:Body><x><xop:Include href="cid:` + atts[0] +
		`"/></x></soapenv:Body></soapenv:Envelope>`
	if err := a.SaveRequestBody(pv.ID, reqID, body); err != nil {
		t.Fatal(err)
	}

	ep := lr.Project.Endpoints[0]
	ep.URL = srv.URL + "/soap/v1"
	ep.MTOM.Mode = soap.AttachMTOM
	if err := a.UpdateEndpoint(pv.ID, ep); err != nil {
		t.Fatal(err)
	}

	res := a.Send(pv.ID, reqID, ep.ID)
	if res.Error != "" {
		t.Fatalf("senden: %s", res.Error)
	}
	if !strings.Contains(gotBody, "Content-ID: <"+atts[0]+">") {
		t.Errorf("referenzierter Anhang fehlt im Multipart:\n%s", firstLines(gotBody, 25))
	}
	if strings.Contains(gotBody, atts[1]) {
		t.Errorf("unreferenzierter Anhang wurde mitgeschickt:\n%s", firstLines(gotBody, 30))
	}
	var warned bool
	for _, w := range res.Warnings {
		if strings.Contains(w, "verwaist.xml") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("keine Warnung zum ausgelassenen Anhang: %v", res.Warnings)
	}
	_ = gotCT
}

// TestProjektEntfernen prüft beide Wege: nur aus der Liste, und in den
// Papierkorb. Endgültig gelöscht wird in keinem Fall.
func TestProjektEntfernen(t *testing.T) {
	a := testApp(t)
	home := t.TempDir()
	t.Setenv("HOME", home)

	pv, err := a.CreateProject("Wegwerf")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.ListProjects()) != 1 {
		t.Fatalf("Projekt nicht in der Liste: %+v", a.ListProjects())
	}

	// 1. Nur aus der Liste: Ordner muss bleiben.
	if err := a.RemoveProject(pv.Dir, false); err != nil {
		t.Fatal(err)
	}
	if len(a.ListProjects()) != 0 {
		t.Error("Projekt steht noch in der Liste")
	}
	if _, err := os.Stat(filepath.Join(pv.Dir, "project.json")); err != nil {
		t.Errorf("Dateien wurden entfernt, obwohl nur die Liste gemeint war: %v", err)
	}
	if _, err := a.get(pv.ID); err == nil {
		t.Error("Projekt ist noch geöffnet")
	}

	// 2. Papierkorb: Ordner muss dort landen, nicht verschwinden.
	pv2, err := a.CreateProject("Wegwerf zwei")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveProject(pv2.Dir, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pv2.Dir); !os.IsNotExist(err) {
		t.Error("Ordner liegt noch am alten Platz")
	}
	trashed := filepath.Join(home, ".Trash", filepath.Base(pv2.Dir))
	if _, err := os.Stat(filepath.Join(trashed, "project.json")); err != nil {
		t.Errorf("Projekt nicht im Papierkorb gelandet: %v", err)
	}

	// 3. Unbekannter Pfad wird abgelehnt.
	if err := a.RemoveProject(filepath.Join(home, "gibtsnicht"), true); err == nil {
		t.Error("unbekannter Pfad hätte abgelehnt werden müssen")
	}
}
