package lexer

import (
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/token"
)

func tokenize(t *testing.T, src string) []token.Token {
	t.Helper()
	toks, errs := New("test.tt", src).Tokenize()
	if len(errs) > 0 {
		t.Fatalf("unexpected lex errors: %v", errs)
	}
	return toks
}

func kinds(toks []token.Token) []token.Kind {
	out := make([]token.Kind, len(toks))
	for i, t := range toks {
		out[i] = t.Kind
	}
	return out
}

func eqKinds(a, b []token.Kind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func format(ks []token.Kind) string {
	parts := make([]string, len(ks))
	for i, k := range ks {
		parts[i] = k.String()
	}
	return strings.Join(parts, " ")
}

func TestSimpleDecl(t *testing.T) {
	toks := tokenize(t, `let x: number = 42`)
	want := []token.Kind{
		token.Let, token.Ident, token.Colon, token.TyNumber,
		token.Assign, token.Int, token.EOF,
	}
	if !eqKinds(kinds(toks), want) {
		t.Fatalf("want %s, got %s", format(want), format(kinds(toks)))
	}
	if toks[1].Value != "x" {
		t.Errorf("ident value: %q", toks[1].Value)
	}
	if toks[5].Value != "42" {
		t.Errorf("int value: %q", toks[5].Value)
	}
}

func TestStringInterpolation(t *testing.T) {
	toks := tokenize(t, `"Hello, ${who}!"`)
	want := []token.Kind{
		token.StringStart,
		token.StringPart, // "Hello, "
		token.InterpStart,
		token.Ident,
		token.InterpEnd,
		token.StringPart, // "!"
		token.StringEnd,
		token.EOF,
	}
	if !eqKinds(kinds(toks), want) {
		t.Fatalf("want %s, got %s", format(want), format(kinds(toks)))
	}
	if toks[1].Value != "Hello, " {
		t.Errorf("first part: %q", toks[1].Value)
	}
	if toks[5].Value != "!" {
		t.Errorf("second part: %q", toks[5].Value)
	}
}

func TestStringNoInterpolation(t *testing.T) {
	toks := tokenize(t, `"plain"`)
	want := []token.Kind{
		token.StringStart, token.StringPart, token.StringEnd, token.EOF,
	}
	if !eqKinds(kinds(toks), want) {
		t.Fatalf("got %s", format(kinds(toks)))
	}
	if toks[1].Value != "plain" {
		t.Errorf("part: %q", toks[1].Value)
	}
}

func TestNestedBraceInInterpolation(t *testing.T) {
	// Make sure { inside ${...} doesn't end the interpolation prematurely.
	toks := tokenize(t, `"${ f({a: 1}) }"`)
	gotKinds := kinds(toks)
	// We don't care about every token but we do care about exactly one InterpEnd
	// closing the matching outer brace.
	endCount := 0
	for _, k := range gotKinds {
		if k == token.InterpEnd {
			endCount++
		}
	}
	if endCount != 1 {
		t.Fatalf("expected 1 InterpEnd, got %d (%s)", endCount, format(gotKinds))
	}
}

func TestCommandLiteral(t *testing.T) {
	toks := tokenize(t, "`ls -1 ${dir}`")
	want := []token.Kind{
		token.CmdStart,
		token.CmdPart, // "ls -1 "
		token.InterpStart,
		token.Ident,
		token.InterpEnd,
		token.CmdPart, // "" (between } and `)
		token.CmdEnd,
		token.EOF,
	}
	if !eqKinds(kinds(toks), want) {
		t.Fatalf("want %s, got %s", format(want), format(kinds(toks)))
	}
}

func TestComment(t *testing.T) {
	toks := tokenize(t, "let x = 1 // trailing\nlet y = 2")
	gotKinds := kinds(toks)
	want := []token.Kind{
		token.Let, token.Ident, token.Assign, token.Int,
		token.Let, token.Ident, token.Assign, token.Int, token.EOF,
	}
	if !eqKinds(gotKinds, want) {
		t.Fatalf("want %s, got %s", format(want), format(gotKinds))
	}
}

func TestOperators(t *testing.T) {
	toks := tokenize(t, "== != <= >= && || ! .. + - * / %")
	want := []token.Kind{
		token.Eq, token.Neq, token.Lte, token.Gte,
		token.AndAnd, token.OrOr, token.Bang, token.DotDot,
		token.Plus, token.Minus, token.Star, token.Slash, token.Percent,
		token.EOF,
	}
	if !eqKinds(kinds(toks), want) {
		t.Fatalf("want %s, got %s", format(want), format(kinds(toks)))
	}
}

func TestEscapes(t *testing.T) {
	toks := tokenize(t, `"a\nb\tc\\d\"e\$f"`)
	if toks[1].Value != "a\nb\tc\\d\"e$f" {
		t.Errorf("escape decoding wrong: %q", toks[1].Value)
	}
}
