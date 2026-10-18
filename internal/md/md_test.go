package md

import (
	"strings"
	"testing"
)

func TestHTMLEscapesRawHTML(t *testing.T) {
	out := HTML(`<script>alert(1)</script>`)
	if strings.Contains(out, "<script>") {
		t.Fatalf("raw script survived: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Fatalf("expected escaped script, got %s", out)
	}
}

func TestHTMLRejectsJavascriptLinks(t *testing.T) {
	out := HTML(`[x](javascript:alert(1))`)
	if strings.Contains(out, `href="javascript:`) {
		t.Fatalf("javascript href survived: %s", out)
	}
	if strings.Contains(out, "<a ") {
		t.Fatalf("unsafe link became an anchor: %s", out)
	}
}

func TestHTMLCodeFenceCopyAndEscape(t *testing.T) {
	out := HTML("```\nset realmlist example.tld\n<script>\n```")
	if !strings.Contains(out, `class="kb-code"`) || !strings.Contains(out, `class="kb-copy"`) {
		t.Fatalf("missing copy wrapper: %s", out)
	}
	if !strings.Contains(out, "set realmlist example.tld") {
		t.Fatalf("missing code body: %s", out)
	}
	if strings.Contains(out, "<script>") {
		t.Fatalf("script in fence not escaped: %s", out)
	}
	if !strings.Contains(out, `href="/realmlist.wtf"`) {
		t.Fatal("realmlist fence should offer a download")
	}
}

func TestHTMLTable(t *testing.T) {
	src := "| A | B |\n| --- | --- |\n| 1 | <x> |\n"
	out := HTML(src)
	if !strings.Contains(out, "<table>") || !strings.Contains(out, "<th>") {
		t.Fatalf("missing table: %s", out)
	}
	if !strings.Contains(out, "&lt;x&gt;") {
		t.Fatalf("cell not escaped: %s", out)
	}
	if strings.Contains(out, "<x>") {
		t.Fatalf("raw html in cell: %s", out)
	}
}

func TestHTMLSafeRelativeLink(t *testing.T) {
	out := HTML(`[Downloads](/downloads)`)
	if !strings.Contains(out, `href="/downloads"`) {
		t.Fatalf("relative link: %s", out)
	}
}

func TestHTMLListsAndHeadings(t *testing.T) {
	src := "## Title\n\n- one\n- two\n\n1. first\n2. second\n"
	out := HTML(src)
	if !strings.Contains(out, "<h2>") || !strings.Contains(out, "<ul>") || !strings.Contains(out, "<ol>") {
		t.Fatalf("structure: %s", out)
	}
}
