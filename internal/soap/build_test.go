package soap

import (
	"context"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/apeters/soapmacui/internal/wsdl"
	"github.com/apeters/soapmacui/internal/xsd"
)

func load(t *testing.T, path string) *wsdl.Definitions {
	t.Helper()
	d, err := wsdl.NewLoader(wsdl.FileFetcher{}).Load(context.Background(), path)
	if err != nil {
		t.Fatalf("laden: %v", err)
	}
	return d
}

func TestBeispielDienste(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		wantOps    int
		endpoint   string
	}{
		// document/literal, WSDL-Präfix, Schema per xsd:import + xsd:include
		{"bookstore", "../../testdata/bookstore/service.wsdl", 3, "https://books.example.org/soap/v1"},
		// rpc/literal, WSDL-Namespace als Default ohne Präfix, Schema inline
		{"legacy", "../../testdata/legacy/service.wsdl", 2, "http://legacy.example.org/soap"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := load(t, tc.path)
			eps := d.Endpoints()
			ops := d.Operations()
			t.Logf("Endpoints: %v · Operationen: %d · Schemata: %d · Typen: %d · globale Elemente: %d",
				eps, len(ops), len(d.Schemas.Schemas), len(d.Schemas.Types), len(d.Schemas.Elements))

			if len(eps) != 1 || eps[0] != tc.endpoint {
				t.Errorf("Endpoint-Erkennung: %v, erwartet [%s]", eps, tc.endpoint)
			}
			if len(ops) != tc.wantOps {
				t.Fatalf("%d Operationen, erwartet %d", len(ops), tc.wantOps)
			}

			b := NewBuilder(d, xsd.GenOptions{})
			for _, op := range ops {
				req, err := b.BuildRequest(op)
				if err != nil {
					t.Errorf("%s: %v", op.Operation, err)
					continue
				}
				if !strings.Contains(req, ":Envelope") || !strings.Contains(req, ":Body>") {
					t.Errorf("%s: Envelope unvollständig:\n%s", op.Operation, req)
				}
			}
		})
	}
}

// TestGeneratorDeckungAbleitung prüft die Punkte, an denen ein naiver
// Generator scheitert: geerbte Felder, Arrays, Enumerationen, Auswahl.
func TestGeneratorDeckungAbleitung(t *testing.T) {
	d := load(t, "../../testdata/bookstore/service.wsdl")
	b := NewBuilder(d, xsd.GenOptions{})

	byName := map[string]string{}
	for _, op := range d.Operations() {
		req, err := b.BuildRequest(op)
		if err != nil {
			t.Fatalf("%s: %v", op.Operation, err)
		}
		byName[op.Operation] = req
	}

	login := byName["login"]
	t.Logf("login:\n%s", login)
	for _, want := range []string{"<tns:user>?</tns:user>", "<tns:password>?</tns:password>"} {
		if !strings.Contains(login, want) {
			t.Errorf("geerbtes Feld fehlt: %s", want)
		}
	}

	find := byName["findBooks"]
	t.Logf("findBooks:\n%s", find)
	if !strings.Contains(find, "<tns:user>") {
		t.Error("findBooks erbt die Zugangsdaten nicht")
	}
	if !strings.Contains(find, "Auswahl: 1 von 3") {
		t.Error("choice wurde nicht als Auswahl gekennzeichnet")
	}
	if strings.Count(find, "<tns:byIsbn>")+strings.Count(find, "<tns:byAuthor>")+
		strings.Count(find, "<tns:byFormat>") != 1 {
		t.Error("aus der Auswahl wurde nicht genau ein Zweig erzeugt")
	}
}

// TestArrayEnumAttribut erzeugt den Antworttyp direkt: dort stecken Array,
// Enumeration, Pflichtattribut und nillable, die im Request nicht vorkommen.
func TestArrayEnumAttribut(t *testing.T) {
	d := load(t, "../../testdata/bookstore/service.wsdl")
	g := xsd.NewGenerator(d.Schemas, xsd.GenOptions{IncludeOptional: true})
	frag, err := g.GenerateElement(
		xml.Name{Space: "urn:example:bookstore:v1", Local: "findBooksResponse"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("findBooksResponse:\n%s", frag)

	// Enumeration: ein "?" wäre garantiert schemawidrig.
	if strings.Contains(frag, "format>?<") {
		t.Error("Enumeration als Platzhalter erzeugt statt als gültiger Wert")
	}
	if !strings.Contains(frag, ">hardcover<") {
		t.Error("erster Enumerationswert fehlt")
	}
	// Array: maxOccurs="unbounded" soll mehrere Beispiele liefern.
	if n := strings.Count(frag, ":author>") / 2; n < 2 { // öffnend+schliessend
		t.Errorf("Array lieferte nur %d Beispiel(e)", n)
	}
	// Pflichtattribut muss am Element hängen.
	if !strings.Contains(frag, `lang="`) {
		t.Error("Pflichtattribut lang fehlt")
	}
	// Optionales Element nur bei IncludeOptional.
	if !strings.Contains(frag, "subtitle") {
		t.Error("optionales Element fehlt trotz IncludeOptional")
	}
}

func TestMTOMVorschlagAusMimeBinding(t *testing.T) {
	d := load(t, "../../testdata/bookstore/service.wsdl")
	var found bool
	for _, op := range d.Operations() {
		if op.Operation == "uploadCover" {
			found = true
			if !op.SuggestMTOM {
				t.Error("uploadCover hat mime:multipartRelated, MTOM wurde nicht vorgeschlagen")
			}
		}
	}
	if !found {
		t.Fatal("uploadCover nicht gefunden")
	}
}

func TestRPCLiteralWrapper(t *testing.T) {
	d := load(t, "../../testdata/legacy/service.wsdl")
	b := NewBuilder(d, xsd.GenOptions{})
	for _, op := range d.Operations() {
		if op.Operation != "getTicket" {
			continue
		}
		req, err := b.BuildRequest(op)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("getTicket (rpc/literal):\n%s", req)
		if !strings.Contains(req, ":getTicket>") {
			t.Error("rpc-Wrapper mit dem Operationsnamen fehlt")
		}
		if !strings.Contains(req, "<id>") {
			t.Error("typreferenzierter Part wurde nicht als unqualifiziertes Element erzeugt")
		}
	}
}
