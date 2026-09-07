package soap

import (
	"strings"
	"testing"
)

const envDoc = `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:tns="urn:example:bookstore:v1">
  <soapenv:Header/>
  <soapenv:Body>
    <tns:uploadCover>
      <tns:image>?</tns:image>
    </tns:uploadCover>
  </soapenv:Body>
</soapenv:Envelope>`

func TestInsertReplacesPlaceholderOnCursorLine(t *testing.T) {
	// Cursor irgendwo in der Zeile mit dem Platzhalter
	cur := strings.Index(envDoc, "<tns:image>") + 4
	out, a, b, err := InsertXOPReference(envDoc, cur, cur, "cover-1@soapmacui")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `<tns:image><xop:Include href="cid:cover-1@soapmacui"/></tns:image>`) {
		t.Fatalf("Platzhalter nicht ersetzt:\n%s", out)
	}
	if !strings.Contains(out, `xmlns:xop="`+XOPNamespace+`"`) {
		t.Fatal("xop-Namespace fehlt")
	}
	// Die zurückgegebene Markierung muss genau den Verweis umfassen.
	if got := out[a:b]; got != `<xop:Include href="cid:cover-1@soapmacui"/>` {
		t.Fatalf("Markierung falsch: %q", got)
	}
}

func TestNamespaceShiftMovesCursor(t *testing.T) {
	// Ohne Korrektur der Verschiebung landet der Verweis zu früh.
	cur := strings.Index(envDoc, "?")
	out, a, b, err := InsertXOPReference(envDoc, cur, cur+1, "x@y")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `<tns:image><xop:Include href="cid:x@y"/></tns:image>`) {
		t.Fatalf("Markierung wurde versetzt ersetzt:\n%s", out)
	}
	if out[a:b] != `<xop:Include href="cid:x@y"/>` {
		t.Fatalf("Markierung falsch: %q", out[a:b])
	}
}

func TestNamespaceAddedOnlyOnce(t *testing.T) {
	cur := strings.Index(envDoc, "?")
	out, _, _, _ := InsertXOPReference(envDoc, cur, cur, "a@b")
	out2, _, _, _ := InsertXOPReference(out, len(out)-30, len(out)-30, "c@d")
	if strings.Count(out2, "xmlns:xop=") != 1 {
		t.Fatalf("Namespace mehrfach ergänzt:\n%s", out2)
	}
}

func TestInsertAtCursorWhenNoPlaceholder(t *testing.T) {
	doc := `<S:Envelope xmlns:S="http://schemas.xmlsoap.org/soap/envelope/"><S:Body><a>Text</a></S:Body></S:Envelope>`
	cur := strings.Index(doc, "Text")
	out, _, _, err := InsertXOPReference(doc, cur, cur, "z@1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `<a><xop:Include href="cid:z@1"/>Text</a>`) {
		t.Fatalf("nicht an der Cursorposition eingefügt:\n%s", out)
	}
}
