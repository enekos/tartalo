package codegen_test

import (
	"strings"
	"testing"
)

func TestRecordConstructAndAccess(t *testing.T) {
	sh := compile(t, `
		type Person = {
			name: string,
			age: number,
		}
		func main(): void {
			let p: Person = Person{name: "Alice", age: 30}
			echo(p.name + " is " + str(p.age))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "Alice is 30" {
		t.Errorf("got %q", got)
	}
}

func TestRecordFieldAssignment(t *testing.T) {
	sh := compile(t, `
		type Counter = { value: number }
		func main(): void {
			let c: Counter = Counter{value: 0}
			c.value = c.value + 1
			c.value = c.value + 1
			echo(str(c.value))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "2" {
		t.Errorf("got %q", got)
	}
}

func TestRecordPassedToFunction(t *testing.T) {
	sh := compile(t, `
		type Person = { name: string, age: number }
		func describe(p: Person): string {
			return p.name + ":" + str(p.age)
		}
		func main(): void {
			let alice: Person = Person{name: "alice", age: 30}
			let bob: Person = Person{name: "bob", age: 25}
			echo(describe(alice))
			echo(describe(bob))
		}
	`)
	out := runShell(t, sh)
	want := "alice:30\nbob:25\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestRecordReturnedFromFunction(t *testing.T) {
	sh := compile(t, `
		type Person = { name: string, age: number }
		func make(n: string, a: number): Person {
			return Person{name: n, age: a}
		}
		func main(): void {
			let p: Person = make("carol", 40)
			echo(p.name + "/" + str(p.age))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "carol/40" {
		t.Errorf("got %q", got)
	}
}

func TestRecordAliasingIsCopy(t *testing.T) {
	// Mutating the alias must not change the original.
	sh := compile(t, `
		type Counter = { value: number }
		func main(): void {
			let a: Counter = Counter{value: 7}
			let b: Counter = a
			b.value = 99
			echo(str(a.value) + "," + str(b.value))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "7,99" {
		t.Errorf("got %q", got)
	}
}

func TestRecordChainedCalls(t *testing.T) {
	// Two consecutive record-returning calls must not clobber each other via __ret.
	sh := compile(t, `
		type Pair = { a: string, b: string }
		func make(x: string, y: string): Pair {
			return Pair{a: x, b: y}
		}
		func main(): void {
			echo(make("1", "2").a + "/" + make("3", "4").b)
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "1/4" {
		t.Errorf("got %q", got)
	}
}

func TestRecordInferredFromLiteral(t *testing.T) {
	// `let p = Person{...}` should infer `: Person` without explicit annotation.
	sh := compile(t, `
		type Point = { x: number, y: number }
		func main(): void {
			let p = Point{x: 3, y: 4}
			echo(str(p.x * p.x + p.y * p.y))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "25" {
		t.Errorf("got %q", got)
	}
}

func TestRejectMissingField(t *testing.T) {
	src := `
		type P = { a: string, b: string }
		func main(): void {
			let p: P = P{a: "x"}
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, "missing field") {
		t.Fatalf("expected 'missing field', got: %v", errs)
	}
}

func TestRejectUnknownField(t *testing.T) {
	src := `
		type P = { a: string }
		func main(): void {
			let p: P = P{a: "x", c: "y"}
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, `has no field "c"`) {
		t.Fatalf("expected 'has no field' error, got: %v", errs)
	}
}

func TestRejectBadFieldAccess(t *testing.T) {
	src := `
		type P = { a: string }
		func main(): void {
			let p: P = P{a: "x"}
			echo(p.nope)
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, `has no field "nope"`) {
		t.Fatalf("expected 'has no field' error, got: %v", errs)
	}
}

func TestRejectFieldOnNonRecord(t *testing.T) {
	src := `
		func main(): void {
			let s = "hi"
			echo(s.length)
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 {
		t.Fatal("expected an error for field access on a string")
	}
}

func TestRejectArrayOfRecords(t *testing.T) {
	src := `
		type P = { a: string }
		func main(): void {
			let xs: P[] = []
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, "arrays of records") {
		t.Fatalf("expected 'arrays of records' error, got: %v", errs)
	}
}

func containsErr(errs []error, sub string) bool {
	for _, e := range errs {
		if strings.Contains(e.Error(), sub) {
			return true
		}
	}
	return false
}
