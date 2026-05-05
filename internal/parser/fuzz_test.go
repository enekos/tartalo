package parser_test

import (
	"strings"
	"testing"

	"github.com/enekosarasola/tartalo/internal/checker"
	"github.com/enekosarasola/tartalo/internal/lexer"
	"github.com/enekosarasola/tartalo/internal/parser"
)

// FuzzCompilerFrontEnd seeds Go's built-in fuzzer with a small corpus of
// real tartalo programs and a handful of edge cases, then lets it mutate
// them. The contract: lex+parse+check must never panic, regardless of
// input. Errors are fine; panics are bugs.
func FuzzCompilerFrontEnd(f *testing.F) {
	for _, seed := range []string{
		"",
		"// comment",
		`func main(): void {}`,
		`let x: number = 1`,
		`let x: string? = null`,
		`type T = { a: string }`,
		`func f(p: T): T { return p }`,
		`if true { echo("hi") }`,
		`for i in 0..10 { echo(str(i)) }`,
		`match 1 { 1 => echo("a") _ => echo("b") }`,
		`import { x } from "./y.tt"`,
		`export func f(): void {}`,
		`let xs = [1, 2, 3]`,
		`let s = "interp ${1 + 2}"`,
		"`cmd ${var}`",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, src string) {
		// Bound input size so the fuzzer doesn't get stuck on multi-megabyte
		// pathologies; parser robustness for huge files is covered separately.
		if len(src) > 4096 {
			t.Skip()
		}
		// We don't care about the result — only that the front-end doesn't
		// panic. Recover-and-fail is the contract.
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("front-end panicked on:\n%q\n%v", src, r)
			}
		}()
		toks, _ := lexer.New("fuzz.tt", src).Tokenize()
		file, _ := parser.New(toks).Parse("fuzz.tt")
		if file != nil {
			checker.New().CheckFile(file)
		}
	})
}

// TestCorpusFromFuzz runs each entry in our seed list through the front
// end. It's the deterministic fallback that ensures even people without
// `go test -fuzz` get coverage for these cases.
func TestCorpusFromFuzz(t *testing.T) {
	for _, src := range fuzzCorpus {
		src := src
		t.Run(strings.ReplaceAll(src, "\n", "\\n"), func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("panic: %v", r)
				}
			}()
			toks, _ := lexer.New("t.tt", src).Tokenize()
			file, _ := parser.New(toks).Parse("t.tt")
			if file != nil {
				checker.New().CheckFile(file)
			}
		})
	}
}

var fuzzCorpus = []string{
	"",
	`func main(): void {}`,
	`let x: T??`,
	`let x: T?[]?`,
	`func f(): T? { return null }`,
	`match null { null => echo("n") }`, // null pattern not supported, must reject
	`let x: number = 1; let x: number = 2`,
	`let x: string = "${1}"`,
	"let x = ``", // empty cmd lit
	`let x: string[] = []`,
	`echo(echo("hi"))`,                // void result fed to echo
	`func f(): number { return f() }`, // infinite recursion at type level — checks fine
	"\xff\xfe binary garbage",
	"\x00\x00\x00",
}
