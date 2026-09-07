package soap

import "testing"

const sample = `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:tns="urn:example:bookstore:v1">
  <soapenv:Header/>
  <soapenv:Body>
    <tns:login>
      <tns:user>hans</tns:user>
    </tns:login>
  </soapenv:Body>
</soapenv:Envelope>`

func TestEncodeBodyExactIsByteFaithful(t *testing.T) {
	o := DefaultWireOptions()
	o.XMLDeclaration = false
	got, err := EncodeBody(sample, o)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sample {
		t.Fatalf("exact hat die Bytes verändert:\n%q", got)
	}
}

func TestEncodeBodyStrip(t *testing.T) {
	o := DefaultWireOptions()
	o.Body, o.XMLDeclaration = BodyStrip, false
	got, err := EncodeBody(sample, o)
	if err != nil {
		t.Fatal(err)
	}
	want := `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/" xmlns:tns="urn:example:bookstore:v1"><soapenv:Header/><soapenv:Body><tns:login><tns:user>hans</tns:user></tns:login></soapenv:Body></soapenv:Envelope>`
	if string(got) != want {
		t.Fatalf("strip falsch:\n got %s\nwant %s", got, want)
	}
}

func TestReindentKeepsPrefixes(t *testing.T) {
	out, err := Reindent(`<a:x xmlns:a="urn:x"><a:y>1</a:y><a:z/></a:x>`, "  ")
	if err != nil {
		t.Fatal(err)
	}
	want := "<a:x xmlns:a=\"urn:x\">\n  <a:y>1</a:y>\n  <a:z></a:z>\n</a:x>"
	if out != want {
		t.Fatalf("reindent falsch:\n got %q\nwant %q", out, want)
	}
}

func TestLatin1RejectsUndisplayable(t *testing.T) {
	o := DefaultWireOptions()
	o.Encoding, o.XMLDeclaration = "ISO-8859-1", false
	if _, err := EncodeBody("<a>€</a>", o); err == nil {
		t.Fatal("Euro-Zeichen hätte in ISO-8859-1 abgelehnt werden müssen")
	}
	got, err := EncodeBody("<a>Grüße</a>", o)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len("<a>Gre</a>")+2 {
		t.Fatalf("Latin-1-Länge unerwartet: %d (%q)", len(got), got)
	}
}
