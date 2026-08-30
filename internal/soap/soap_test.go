package soap

import (
	"strings"
	"testing"
)

func TestBuildCreateCommand(t *testing.T) {
	got := BuildCreateCommand("Hero", "Abcd1234", "a@b.com")
	want := `account create "Hero" "Abcd1234" "a@b.com"`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	got = BuildCreateCommand("Hero", "Abcd1234", "")
	if got != `account create "Hero" "Abcd1234"` {
		t.Fatalf("got %q", got)
	}
}

func TestBuildUnstuckCommand(t *testing.T) {
	got := BuildUnstuckCommand("Frostwarden")
	if got != `unstuck "Frostwarden" inn` {
		t.Fatalf("got %q", got)
	}
}

func TestBuildSetGMLevelCommand(t *testing.T) {
	got := BuildSetGMLevelCommand("Hero", 2)
	if got != `account set gmlevel "Hero" 2 -1` {
		t.Fatalf("got %q", got)
	}
}

func TestEnvelopeEscapesXML(t *testing.T) {
	cmd := `account create "Hero" "a<b&c>" "e@e.com"`
	env := Envelope(cmd)
	if strings.Contains(env, "<b&") || strings.Contains(env, "a<b") {
		t.Fatal("raw special chars leaked into XML")
	}
	if !strings.Contains(env, "&lt;") || !strings.Contains(env, "&amp;") {
		t.Fatalf("expected escaped XML, got %s", env)
	}
	if strings.Contains(env, "Abcd1234") {
		// password used in other tests only
	}
}

func TestRejectUnsafe(t *testing.T) {
	if err := rejectUnsafe(`pw"x`); err == nil {
		t.Fatal("quote should fail")
	}
	if err := rejectUnsafe("okpass"); err != nil {
		t.Fatal(err)
	}
}

func TestParseFault(t *testing.T) {
	raw := []byte(`<?xml version="1.0"?>
<SOAP-ENV:Envelope xmlns:SOAP-ENV="http://schemas.xmlsoap.org/soap/envelope/">
  <SOAP-ENV:Body>
    <SOAP-ENV:Fault><faultstring>Account with this name already exist!</faultstring></SOAP-ENV:Fault>
  </SOAP-ENV:Body>
</SOAP-ENV:Envelope>`)
	_, fault, err := ParseResponse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if fault == "" {
		t.Fatal("expected fault")
	}
}

func TestWrapDoesNotEchoPassword(t *testing.T) {
	err := wrapAccountErr(fmtErr(`soap: account create "Hero" "S3cretPass" failed`))
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "S3cretPass") {
		t.Fatalf("password leaked: %v", err)
	}
}

type fmtErr string

func (e fmtErr) Error() string { return string(e) }
