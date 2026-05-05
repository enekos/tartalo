package codegen_test

import (
	"strings"
	"testing"

	"github.com/enekosarasola/tartalo/internal/checker"
	"github.com/enekosarasola/tartalo/internal/lexer"
	"github.com/enekosarasola/tartalo/internal/parser"
)

func checkOnly(t *testing.T, src string) []error {
	t.Helper()
	toks, lerrs := lexer.New("t.tt", src).Tokenize()
	if len(lerrs) > 0 {
		t.Fatalf("lex: %v", lerrs)
	}
	file, perrs := parser.New(toks).Parse("t.tt")
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs)
	}
	_, cerrs := checker.New().CheckFile(file)
	return cerrs
}

func TestInferLetTypes(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let s = "hello"
			let n = 41 + 1
			let b = n > 10
			if b {
				echo(s + "!")
			}
			echo(str(n))
		}
	`)
	out := runShell(t, sh)
	want := "hello!\n42\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestInferRejectsVoid(t *testing.T) {
	// `echo` returns void; can't bind it to a name.
	src := `
		func main(): void {
			let x = echo("hi")
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 {
		t.Fatal("expected an error inferring from void")
	}
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "cannot infer type") {
			found = true
		}
	}
	if !found {
		t.Errorf("error mismatch: %v", errs)
	}
}
