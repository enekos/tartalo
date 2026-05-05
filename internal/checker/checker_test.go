package checker_test

import (
	"strings"
	"testing"

	"github.com/enekosarasola/tartalo/internal/checker"
	"github.com/enekosarasola/tartalo/internal/lexer"
	"github.com/enekosarasola/tartalo/internal/parser"
)

func check(t *testing.T, src string) []error {
	t.Helper()
	toks, lerrs := lexer.New("t.tt", src).Tokenize()
	if len(lerrs) > 0 {
		t.Fatalf("lex errors: %v", lerrs)
	}
	file, perrs := parser.New(toks).Parse("t.tt")
	if len(perrs) > 0 {
		t.Fatalf("parse errors: %v", perrs)
	}
	_, cerrs := checker.New().CheckFile(file)
	return cerrs
}

func wantError(t *testing.T, src, contains string) {
	t.Helper()
	errs := check(t, src)
	if len(errs) == 0 {
		t.Fatalf("expected an error containing %q, got none", contains)
	}
	for _, e := range errs {
		if strings.Contains(e.Error(), contains) {
			return
		}
	}
	t.Fatalf("expected error containing %q, got: %v", contains, errs)
}

func wantOk(t *testing.T, src string) {
	t.Helper()
	errs := check(t, src)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestRejectStringPlusNumber(t *testing.T) {
	wantError(t, `let x: string = "a" + 1`,
		"+ requires both operands to be number or both to be string")
}

func TestRejectAnnotationMismatch(t *testing.T) {
	wantError(t, `let x: number = "hi"`, "type mismatch")
}

func TestRejectUndefined(t *testing.T) {
	wantError(t, `let x: number = nope`, `undefined name "nope"`)
}

func TestRejectArityMismatch(t *testing.T) {
	wantError(t, `
		func id(s: string): string { return s }
		func main(): void { echo(id()) }
	`, `expects 1 argument`)
}

func TestRejectArgTypeMismatch(t *testing.T) {
	wantError(t, `
		func main(): void { echo(42) }
	`, `argument 1 to "echo"`)
}

func TestRejectAssignToConst(t *testing.T) {
	wantError(t, `
		func main(): void {
			const k: number = 1
			k = 2
		}
	`, `cannot assign to const`)
}

func TestRejectVoidReturn(t *testing.T) {
	wantError(t, `
		func main(): void { return 1 }
	`, `void function cannot return a value`)
}

func TestRejectMissingReturnValue(t *testing.T) {
	wantError(t, `
		func f(): number { return }
		func main(): void {}
	`, `function returns number, return statement has no value`)
}

func TestAcceptForwardReference(t *testing.T) {
	// main calls f before f is declared in source order
	wantOk(t, `
		func main(): void { echo(f()) }
		func f(): string { return "ok" }
	`)
}

func TestAcceptStringConcat(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let a: string = "a"
			let b: string = "b"
			let c: string = a + b
			echo(c)
		}
	`)
}

func TestAcceptComplexBoolExpr(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let x: number = 5
			let ok: bool = (x > 1 && x < 10) || x == 0
			if ok { echo("yes") }
		}
	`)
}

func TestAcceptStringOrdering(t *testing.T) {
	wantOk(t, `
		func main(): void {
			let a: string = "a"
			let b: string = "b"
			if a < b { echo("yes") }
		}
	`)
}

func TestRejectMixedComparison(t *testing.T) {
	wantError(t, `
		func main(): void {
			let a: string = "a"
			let n: number = 1
			if a < n { echo("nope") }
		}
	`, `requires number or string operands`)
}
