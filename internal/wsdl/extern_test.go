//go:build extern

// Externe Prüfung gegen öffentlich erreichbare SOAP-Dienste.
//
// Bewusst hinter einem Build-Tag: diese Tests hängen an fremden Servern und
// haben in CI nichts zu suchen — ein Ausfall dort wäre kein Fehler bei uns.
// Der reguläre Lauf bleibt hermetisch (httptest, testdata/).
//
//	make test-extern
//
// Was sie leisten: echte WSDLs sind unordentlicher als jedes Beispiel. Sie
// haben mehrere Ports, SOAP 1.1 und 1.2 nebeneinander, rpc/encoded, Importe
// über Hostgrenzen und Eigenheiten der jeweiligen Stacks (.NET, Axis, Caché).
// Genau daran zeigt sich, ob der Loader trägt.
package wsdl

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"
)

// httpFetcher holt Dokumente über das Netz. Eigene Instanz statt der aus der
// App, damit dieser Test nichts aus internal/app braucht.
type httpFetcher struct{ c *http.Client }

func (f httpFetcher) Fetch(ctx context.Context, rawurl string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawurl, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "SoapMacUi/0.1 (Test)")
	res, err := f.c.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 8<<20))
}

var externe = []struct {
	name string
	url  string
	// minOps ist die untere Schranke, nicht die exakte Zahl — die Dienste
	// dürfen sich weiterentwickeln, ohne den Test rot zu machen.
	minOps int
}{
	{"dneonline Calculator (.NET, SOAP 1.1 + 1.2)", "http://www.dneonline.com/calculator.asmx?WSDL", 4},
	{"dataaccess NumberConversion", "https://www.dataaccess.com/webservicesserver/NumberConversion.wso?WSDL", 2},
	{"learnwebservices Hello", "https://apps.learnwebservices.com/services/hello?wsdl", 1},
	{"crcind SOAP.Demo (InterSystems Caché)", "https://www.crcind.com/csp/samples/SOAP.Demo.cls?WSDL", 1},
}

func TestExterneWSDLs(t *testing.T) {
	f := httpFetcher{c: &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}}

	for _, c := range externe {
		t.Run(c.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()

			d, err := NewLoader(f).Load(ctx, c.url)
			if err != nil {
				// Fremder Server nicht erreichbar ist kein Fehler bei uns.
				t.Skipf("nicht ladbar: %v", err)
			}

			ops := d.Operations()
			if len(ops) < c.minOps {
				t.Errorf("%d Operationen, erwartet mindestens %d", len(ops), c.minOps)
			}
			if len(d.Endpoints()) == 0 {
				t.Error("keine Adresse im WSDL gefunden")
			}

			// Jede Operation muss die Adresse ihres eigenen Ports tragen —
			// die Zusicherung, auf der die Oberfläche aufbaut.
			for _, op := range ops {
				if op.Endpoint == "" {
					t.Errorf("Operation %s/%s/%s ohne Adresse", op.Service, op.Port, op.Operation)
				}
			}

			versionen := map[SOAPVersion]int{}
			portNamen := map[string]bool{}
			for _, op := range ops {
				versionen[op.Version]++
				portNamen[op.Port] = true
			}
			t.Logf("%d Quelldokumente, %d Operationen, %d Ports %v, Versionen %v",
				len(d.Sources), len(ops), len(portNamen), keys(portNamen), versionen)
		})
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
