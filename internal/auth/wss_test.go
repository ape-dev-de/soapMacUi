package auth

import (
	"strings"
	"testing"
)

const env = `<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/">
  <soapenv:Header/>
  <soapenv:Body>
    <x/>
  </soapenv:Body>
</soapenv:Envelope>`

func TestInjectIntoSelfClosingHeader(t *testing.T) {
	c := DefaultConfig()
	c.Kind, c.Username = KindWSS, "hans"
	out, err := InjectUsernameToken(env, c, "geheim")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"<soapenv:Header>", "</soapenv:Header>", "wsse:Security", "<wsse:Username>hans</wsse:Username>", "geheim", "wsse:Nonce", "wsu:Created", `soapenv:mustUnderstand="1"`} {
		if !strings.Contains(out, want) {
			t.Errorf("fehlt: %s\n%s", want, out)
		}
	}
	if strings.Contains(out, "<soapenv:Header/>") {
		t.Error("selbstschliessender Header wurde nicht ersetzt")
	}
	// Der Body darf nicht angefasst worden sein.
	if !strings.Contains(out, "  <soapenv:Body>\n    <x/>\n  </soapenv:Body>") {
		t.Error("Body wurde verändert")
	}
}

func TestDigestDiffersFromPlaintext(t *testing.T) {
	c := DefaultConfig()
	c.Kind, c.Username, c.PasswordType = KindWSS, "hans", PasswordDigest
	out, err := InjectUsernameToken(env, c, "geheim")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, ">geheim<") {
		t.Fatal("Klartextpasswort im Digest-Modus gesendet")
	}
	if !strings.Contains(out, "#PasswordDigest") {
		t.Fatal("Digest-Type-URI fehlt")
	}
}

func TestInjectWithoutExistingHeader(t *testing.T) {
	e := `<S:Envelope xmlns:S="http://schemas.xmlsoap.org/soap/envelope/"><S:Body><x/></S:Body></S:Envelope>`
	c := DefaultConfig()
	c.Kind, c.Username = KindWSS, "a"
	out, err := InjectUsernameToken(e, c, "b")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "<S:Header>") || !strings.Contains(out, "</S:Header>") {
		t.Fatalf("Header wurde nicht angelegt:\n%s", out)
	}
	if strings.Index(out, "<S:Header>") > strings.Index(out, "<S:Body>") {
		t.Error("Header steht nach dem Body")
	}
}
