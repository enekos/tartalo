package parser_test

import (
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/parser"
)

// safeParse runs the full lex+parse pipeline and recovers from any panic.
// Returns the panic message if one happened, empty otherwise. Used by the
// robustness tests to assert that the front-end never crashes on malformed
// input — even if it has to bail out with errors.
func safeParse(src string) (panicMsg string) {
	defer func() {
		if r := recover(); r != nil {
			panicMsg = trace(r)
		}
	}()
	toks, _ := lexer.New("t.tt", src).Tokenize()
	parser.New(toks).Parse("t.tt")
	return ""
}

func trace(r any) string {
	switch v := r.(type) {
	case string:
		return v
	case error:
		return v.Error()
	default:
		return "non-error panic"
	}
}

// Each entry in this slice is a deliberately broken (or merely odd) source
// that the front-end should reject without crashing. The test asserts that:
//   - we get an error (parse never succeeds), OR
//   - we get no error (some inputs are surprisingly legal, that's fine).
//
// The single thing we DO NOT tolerate is a panic.
var malformedSources = []string{
	"",                                     // empty input
	" ",                                    // whitespace only
	"\n\n\n",                               // newlines only
	"// comment only\n",                    // comment only
	"func",                                 // bare keyword
	"func main",                            // missing parens
	"func main(",                           // unterminated params
	"func main()",                          // missing return
	"func main(): void",                    // missing body
	"func main(): void {",                  // unterminated body
	"func main(): void {}",                 // OK actually
	"let",                                  // bare let
	"let x",                                // missing =
	"let x =",                              // missing value
	"let x: =",                             // missing type
	"let x: number = ",                     // missing rhs
	"let x: number = 1 + ",                 // unterminated expression
	"let x: number = (((",                  // unbalanced parens
	"let x: number = ((1)+(2",              // unbalanced parens
	"let x: string = \"unterm",             // unterminated string
	"let x: string = \"a${\"",              // unterminated interp
	"let x: string = `unterm",              // unterminated cmd
	"let x: string = \"\\q\"",              // unknown escape
	"let x: number = 0..10",                // range outside for
	"let x: bool = true && && false",       // double-op
	"if {}",                                // missing condition
	"if true {",                            // unterminated then
	"if true {} else",                      // missing else body
	"if true {} else if",                   // unterminated else-if
	"for {}",                               // missing iter
	"for x in {}",                          // missing iter expr
	"match {}",                             // missing subject
	"match 1 {",                            // unterminated arms
	"match 1 { 1 }",                        // missing =>
	"match 1 { 1 => }",                     // missing body
	"type",                                 // bare keyword
	"type Foo",                             // missing =
	"type Foo =",                           // missing body
	"type Foo = {",                         // unterminated
	"type Foo = { a: }",                    // missing field type
	"import",                               // bare keyword
	"import {",                             // unterminated
	"import { a }",                         // missing from
	"import { a } from",                    // missing path
	"import { a } from x",                  // path not a string
	"export",                               // bare keyword
	"func f(): void { return ",             // unterminated return
	"func f(): void { let x = }",           // empty rhs
	"func f(): void { x = }",               // empty rhs in assign
	"func f(): void { x. }",                // missing field name
	"func f(): void { x[ }",                // unterminated index
	"func f(): void { f() }",               // ok
	"func f(): void { f( }",                // unterminated call
	"func f(): void { f(,) }",              // empty arg before comma
	"let x: T??",                           // double optional in v0
	"let x: string?? = null",               // double optional with assign
	"let x: T[][]?",                        // ok grammatically; may reject in checker
	"let x: T?[]",                          // optional-array
	"let x = null",                         // bare null inference (rejected in checker)
	"let x: string? = null!",               // unwrap of literal null (compile-time? runtime?)
	"func f(): void {} func f(): void {}",  // duplicate funcs
	"let x: number = 1; let x: number = 2", // duplicate globals
	`func main(): void { match "x" { 1 => echo("oops") } }`, // type-mismatched pattern
	"!!!",                   // sequence of operators
	"...",                   // dots
	"=>=>",                  // arrows
	"\x00\x01\x02",          // control bytes
	"// \xff\xfe garbage\n", // invalid UTF-8 in comment
	`"\xff"`,                // invalid UTF-8 in string
}

func TestParserDoesNotPanicOnMalformedInputs(t *testing.T) {
	for _, src := range malformedSources {
		src := src
		t.Run(snippet(src), func(t *testing.T) {
			if msg := safeParse(src); msg != "" {
				t.Errorf("parser panicked on %q: %s", src, msg)
			}
		})
	}
}

func snippet(s string) string {
	const max = 40
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\t", "\\t")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// TestDeeplyNestedExpression checks the parser doesn't blow up on aggressive
// nesting — useful both for stack-recursion bounds and for catching O(n²)
// allocation behaviour.
func TestDeeplyNestedExpression(t *testing.T) {
	const depth = 256
	src := "let x: number = " + strings.Repeat("(", depth) + "1" + strings.Repeat(")", depth)
	if msg := safeParse(src); msg != "" {
		t.Fatalf("panic on depth-%d nesting: %s", depth, msg)
	}
}

// TestVeryLongIdentifier — the lexer should slurp arbitrary-length idents
// without misbehaving.
func TestVeryLongIdentifier(t *testing.T) {
	long := strings.Repeat("a", 10_000)
	src := "let " + long + ": number = 1"
	if msg := safeParse(src); msg != "" {
		t.Fatalf("panic on long ident: %s", msg)
	}
}

// TestVeryLongString — same for strings.
func TestVeryLongString(t *testing.T) {
	body := strings.Repeat("x", 100_000)
	src := `let x: string = "` + body + `"`
	if msg := safeParse(src); msg != "" {
		t.Fatalf("panic on long string: %s", msg)
	}
}

// TestManyImports — the parser must accept large numbers of imports without
// quadratic slowness or crashes.
func TestManyImports(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 200; i++ {
		b.WriteString(`import { x } from "./x.tt"` + "\n")
	}
	if msg := safeParse(b.String()); msg != "" {
		t.Fatalf("panic on many imports: %s", msg)
	}
}
