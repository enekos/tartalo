package diag

import (
	"bytes"
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/token"
)

func TestErrorLegacyFormat(t *testing.T) {
	d := New(token.Pos{File: "foo.tt", Line: 12, Col: 5}, "expected `;`")
	got := d.Error()
	want := "foo.tt:12:5: expected `;`"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderFrameNoColor(t *testing.T) {
	src := MapSources{"foo.tt": "let x = 1\nlet y = \"hello\"\nlet z = 3\n"}
	d := New(token.Pos{File: "foo.tt", Line: 2, Col: 9}, "type mismatch").
		WithHint("`y` was declared as number but assigned a string").
		WithSuggest(`use a number literal, e.g. let y = 42`)

	var buf bytes.Buffer
	Render(&buf, []*Diag{d}, src, false)
	out := buf.String()

	for _, want := range []string{
		"error: type mismatch",
		"--> foo.tt:2:9",
		`let y = "hello"`,
		"        ^", // caret under col 9 (8 spaces of pad)
		"= help: `y` was declared as number but assigned a string",
		"= suggestion: use a number literal",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q\n--- got ---\n%s", want, out)
		}
	}
}

func TestSuggestBasic(t *testing.T) {
	cands := []string{"length", "filter", "map", "reduce"}
	if got := Suggest("lenght", cands); got != "length" {
		t.Fatalf("want length, got %q", got)
	}
	if got := Suggest("xyz", cands); got != "" {
		t.Fatalf("want empty, got %q", got)
	}
	// Case-insensitive exact match wins immediately.
	if got := Suggest("LENGTH", cands); got != "length" {
		t.Fatalf("want length, got %q", got)
	}
}

func TestRenderRangeUnderline(t *testing.T) {
	src := MapSources{"foo.tt": "let x = 1 + \"a\"\n"}
	d := New(token.Pos{File: "foo.tt", Line: 1, Col: 13}, "type mismatch").
		WithEnd(token.Pos{File: "foo.tt", Line: 1, Col: 16})

	var buf bytes.Buffer
	Render(&buf, []*Diag{d}, src, false)
	out := buf.String()

	// Three carets under "a" plus its quotes.
	if !strings.Contains(out, "^^^") {
		t.Errorf("expected 3-wide caret span, got:\n%s", out)
	}
}
