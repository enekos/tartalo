package codegen_test

import (
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/parser"
)

func compileExpectError(t *testing.T, src string) (string, []error) {
	t.Helper()
	toks, lerrs := lexer.New("test.tt", src).Tokenize()
	if len(lerrs) > 0 {
		return "", lerrs
	}
	file, perrs := parser.New(toks).Parse("test.tt")
	if len(perrs) > 0 {
		return "", perrs
	}
	_, cerrs := checker.New().CheckFile(file)
	return "", cerrs
}

func joinErrs(errs []error) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "\n")
}

func TestRecordSpreadOverridesField(t *testing.T) {
	sh := compile(t, `
		type Person = {
			name: string,
			age: number,
		}
		func main(): void {
			let alice: Person = Person{name: "Alice", age: 30}
			let older: Person = Person{...alice, age: 31}
			echo(older.name + "/" + str(older.age))
			echo(alice.name + "/" + str(alice.age))
		}
	`)
	out := runShell(t, sh)
	want := "Alice/31\nAlice/30\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestRecordSpreadKeepsEverything(t *testing.T) {
	sh := compile(t, `
		type P = { a: string, b: number, c: bool }
		func main(): void {
			let original: P = P{a: "x", b: 7, c: true}
			let copy: P = P{...original}
			echo(copy.a + "/" + str(copy.b) + "/" + str(copy.c))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "x/7/1" {
		t.Errorf("got %q", got)
	}
}

func TestRecordSpreadWithNestedRecord(t *testing.T) {
	sh := compile(t, `
		type Addr = { city: string, zip: number }
		type Person = { name: string, addr: Addr }
		func main(): void {
			let alice: Person = Person{
				name: "Alice",
				addr: Addr{city: "Madrid", zip: 28001},
			}
			let renamed: Person = Person{...alice, name: "Alicia"}
			echo(renamed.name + " in " + renamed.addr.city + " #" + str(renamed.addr.zip))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	want := "Alicia in Madrid #28001"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestRecordSpreadRejectsCrossType(t *testing.T) {
	src := `
		type A = { x: number }
		type B = { x: number }
		func main(): void {
			let a: A = A{x: 1}
			let b: B = B{...a}
			echo(str(b.x))
		}
	`
	_, errs := compileExpectError(t, src)
	if len(errs) == 0 {
		t.Fatalf("expected type error for cross-type spread, got none")
	}
	joined := strings.ToLower(joinErrs(errs))
	if !strings.Contains(joined, "spread source") {
		t.Errorf("expected error mentioning spread source, got: %v", errs)
	}
}

func TestRecordSpreadOverrideTypeChecks(t *testing.T) {
	src := `
		type P = { a: string, b: number }
		func main(): void {
			let p: P = P{a: "x", b: 1}
			let q: P = P{...p, b: "not a number"}
			echo(q.a)
		}
	`
	_, errs := compileExpectError(t, src)
	if len(errs) == 0 {
		t.Fatalf("expected type error for bad override, got none")
	}
}
