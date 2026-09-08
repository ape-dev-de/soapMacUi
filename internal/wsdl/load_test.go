package wsdl

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recordingFetcher merkt sich, wonach gefragt wurde, und liefert vorbereitete
// Antworten. So lässt sich prüfen, dass ein abgelehnter Import gar nicht erst
// geholt wird — nicht bloss, dass sein Inhalt später nicht auftaucht.
type recordingFetcher struct {
	docs  map[string]string
	asked []string
}

func (f *recordingFetcher) Fetch(_ context.Context, rawurl string) ([]byte, error) {
	f.asked = append(f.asked, rawurl)
	if d, ok := f.docs[rawurl]; ok {
		return []byte(d), nil
	}
	return nil, fmt.Errorf("nicht vorhanden: %s", rawurl)
}

func (f *recordingFetcher) hatGefragtNach(teil string) bool {
	for _, a := range f.asked {
		if strings.Contains(a, teil) {
			return true
		}
	}
	return false
}

func wsdlMitSchemaImport(loc string) string {
	return fmt.Sprintf(`<?xml version="1.0"?>
<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
                  xmlns:xs="http://www.w3.org/2001/XMLSchema"
                  targetNamespace="urn:t">
  <wsdl:types>
    <xs:schema targetNamespace="urn:t">
      <xs:import namespace="urn:t" schemaLocation="%s"/>
    </xs:schema>
  </wsdl:types>
</wsdl:definitions>`, loc)
}

// TestEntferntesWsdlDarfKeineLokalenDateienLesen ist der Kern von S4: das
// Dokument kommt über das Netz, der Import zeigt auf die Platte.
func TestEntferntesWsdlDarfKeineLokalenDateienLesen(t *testing.T) {
	geheim := filepath.Join(t.TempDir(), "geheim.xsd")
	if err := os.WriteFile(geheim, []byte("<xs:schema/>"), 0o600); err != nil {
		t.Fatal(err)
	}

	faelle := []struct {
		loc          string
		abgelehnt    bool
		warumErlaubt string
	}{
		// Explizites file:-Schema — der eigentliche Angriff, wird abgelehnt.
		{loc: "file://" + geheim, abgelehnt: true},
		{loc: "file://" + filepath.Dir(geheim) + "/../geheim.xsd", abgelehnt: true},

		// Blanke Pfade sind gegenüber einer http-Basis relative URL-Referenzen
		// und werden von resolveRef zu https://partner.example/… aufgelöst.
		// Sie erreichen die Platte also gar nicht erst; abgelehnt werden sie
		// deshalb nicht, sondern laufen als gewöhnlicher HTTP-Abruf ins Leere.
		{loc: geheim, warumErlaubt: "wird zur URL auf demselben Host"},
		{loc: "/etc/passwd", warumErlaubt: "wird zur URL auf demselben Host"},
	}

	for _, c := range faelle {
		f := &recordingFetcher{docs: map[string]string{
			"https://partner.example/svc?wsdl": wsdlMitSchemaImport(c.loc),
		}}
		_, err := NewLoader(f).Load(context.Background(), "https://partner.example/svc?wsdl")

		if c.abgelehnt {
			if err == nil || !strings.Contains(err.Error(), "abgelehnt") {
				t.Errorf("Import %q wurde nicht abgelehnt: %v", c.loc, err)
			}
		}

		// Die eigentliche Zusicherung, unabhängig davon, ob abgelehnt oder
		// umgeschrieben wurde: nichts ausser http(s) darf angefragt werden.
		// Der FileFetcher würde alles andere von der Platte lesen.
		for _, a := range f.asked {
			if !isRemote(a) {
				t.Errorf("Import %q (%s): Fetcher wurde nach %q gefragt — kein http(s)",
					c.loc, c.warumErlaubt, a)
			}
		}
	}
}

// TestEntferntesWsdlDarfQuerUeberHosts hält fest, dass die Grenze bewusst nur
// das Schema betrifft — Schemata liegen regelmässig auf anderen Hosts.
func TestEntferntesWsdlDarfQuerUeberHosts(t *testing.T) {
	f := &recordingFetcher{docs: map[string]string{
		"https://partner.example/svc?wsdl": wsdlMitSchemaImport("https://schemas.anderswo.example/t.xsd"),
		"https://schemas.anderswo.example/t.xsd": `<?xml version="1.0"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t"/>`,
	}}
	// Der Ladevorgang scheitert später an "kein Service und kein Binding" —
	// hier zählt nur, dass der fremde Host überhaupt angefragt wurde.
	_, _ = NewLoader(f).Load(context.Background(), "https://partner.example/svc?wsdl")
	if !f.hatGefragtNach("schemas.anderswo.example") {
		t.Errorf("Import auf anderen Host wurde nicht gefolgt: %v", f.asked)
	}
}

// TestLokalesWsdlBleibtInSeinemOrdner deckt die andere Richtung ab.
func TestLokalesWsdlBleibtInSeinemOrdner(t *testing.T) {
	dir := t.TempDir()
	drin := filepath.Join(dir, "svc.wsdl")
	draussen := filepath.Join(filepath.Dir(dir), "fremd.xsd")

	if err := os.WriteFile(drin, []byte(wsdlMitSchemaImport("../fremd.xsd")), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draussen, []byte(`<?xml version="1.0"?>
<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema" targetNamespace="urn:t"/>`), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := NewLoader(FileFetcher{}).Load(context.Background(), drin)
	if err == nil || !strings.Contains(err.Error(), "abgelehnt") {
		t.Errorf("Ausbruch aus dem WSDL-Ordner nicht abgelehnt: %v", err)
	}
}

// TestLokalesWsdlLaedtNachbarschema stellt sicher, dass der Normalfall — das
// Beispiel aus testdata mit relativem schema/-Pfad — weiter funktioniert.
func TestLokalesWsdlLaedtNachbarschema(t *testing.T) {
	d, err := NewLoader(FileFetcher{}).Load(context.Background(), "../../testdata/bookstore/service.wsdl")
	if err != nil {
		t.Fatalf("Beispiel-WSDL laden: %v", err)
	}
	if len(d.Services) == 0 {
		t.Error("kein Service gefunden")
	}
	var xsds int
	for _, s := range d.Sources {
		if s.Kind == "xsd" {
			xsds++
		}
	}
	// bookstore.xsd und das per xs:include geholte base/common.xsd.
	if xsds < 2 {
		t.Errorf("nur %d Schemata geladen, erwartet mindestens 2", xsds)
	}
}

// TestZweiPortsErzeugenJedeOperationZweimal hält die Kardinalität fest, auf der
// die Oberfläche aufbaut: die Adresse hängt am Port, nicht an der Operation,
// und zwei Ports über demselben portType liefern jede Operation doppelt — mit
// unterschiedlichem Port und unterschiedlicher Adresse. Siehe docs/wsdl-modell.md.
func TestZweiPortsErzeugenJedeOperationZweimal(t *testing.T) {
	const doc = `<?xml version="1.0"?>
<wsdl:definitions xmlns:wsdl="http://schemas.xmlsoap.org/wsdl/"
                  xmlns:soap="http://schemas.xmlsoap.org/wsdl/soap/"
                  xmlns:xs="http://www.w3.org/2001/XMLSchema"
                  xmlns:tns="urn:t" targetNamespace="urn:t">
  <wsdl:types><xs:schema targetNamespace="urn:t">
    <xs:element name="ping" type="xs:string"/>
  </xs:schema></wsdl:types>
  <wsdl:message name="pingIn"><wsdl:part name="parameters" element="tns:ping"/></wsdl:message>
  <wsdl:portType name="PT">
    <wsdl:operation name="ping"><wsdl:input message="tns:pingIn"/></wsdl:operation>
  </wsdl:portType>
  <wsdl:binding name="B" type="tns:PT">
    <soap:binding style="document" transport="http://schemas.xmlsoap.org/soap/http"/>
    <wsdl:operation name="ping">
      <soap:operation soapAction="urn:t:ping"/>
      <wsdl:input><soap:body use="literal"/></wsdl:input>
    </wsdl:operation>
  </wsdl:binding>
  <wsdl:service name="S">
    <wsdl:port name="PortA" binding="tns:B">
      <soap:address location="https://a.example.org/svc"/>
    </wsdl:port>
    <wsdl:port name="PortB" binding="tns:B">
      <soap:address location="https://b.example.org/svc"/>
    </wsdl:port>
  </wsdl:service>
</wsdl:definitions>`

	f := &recordingFetcher{docs: map[string]string{"https://h.example/svc?wsdl": doc}}
	d, err := NewLoader(f).Load(context.Background(), "https://h.example/svc?wsdl")
	if err != nil {
		t.Fatalf("laden: %v", err)
	}

	ops := d.Operations()
	if len(ops) != 2 {
		t.Fatalf("%d Operationen, erwartet 2 (eine je Port)", len(ops))
	}
	if ops[0].Operation != "ping" || ops[1].Operation != "ping" {
		t.Fatalf("unerwartete Namen: %q, %q", ops[0].Operation, ops[1].Operation)
	}
	// Dasselbe Binding in zwei Ports ist zulässig — die Ports müssen sich
	// trotzdem in Name und Adresse unterscheiden, sonst kann die Oberfläche
	// die beiden Einträge nicht auseinanderhalten.
	if ops[0].Port == ops[1].Port {
		t.Errorf("beide Operationen melden Port %q", ops[0].Port)
	}
	if ops[0].Endpoint == ops[1].Endpoint {
		t.Errorf("beide Operationen melden Adresse %q", ops[0].Endpoint)
	}
	if ops[0].Binding != ops[1].Binding {
		t.Errorf("Binding sollte in beiden Ports dasselbe sein: %q vs %q", ops[0].Binding, ops[1].Binding)
	}

	// Und die Adressen landen beide als Endpoint-Vorschlag im Projekt.
	if got := d.Endpoints(); len(got) != 2 {
		t.Errorf("Endpoints() = %v, erwartet zwei Adressen", got)
	}
}
