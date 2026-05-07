package codegen_test

import (
	"strings"
	"testing"
)

func TestCastBetweenSameShape(t *testing.T) {
	sh := compile(t, `
		type RawUser   = { name: string, age: number, email: string }
		type ShortUser = { name: string, age: number }
		func main(): void {
			let raw: RawUser = RawUser{name: "Alice", age: 30, email: "a@x"}
			let short: ShortUser = raw as ShortUser
			echo(short.name + "/" + str(short.age))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "Alice/30" {
		t.Errorf("got %q", got)
	}
}

func TestCastIdentity(t *testing.T) {
	sh := compile(t, `
		type Person = { name: string, age: number }
		func main(): void {
			let p: Person = Person{name: "Bob", age: 25}
			let q: Person = p as Person
			echo(q.name)
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "Bob" {
		t.Errorf("got %q", got)
	}
}

func TestCastWithNestedField(t *testing.T) {
	sh := compile(t, `
		type Addr = { city: string, zip: number }
		type FullPerson  = { name: string, addr: Addr, email: string }
		type SlimPerson  = { name: string, addr: Addr }
		func main(): void {
			let full: FullPerson = FullPerson{
				name: "Alice",
				addr: Addr{city: "Madrid", zip: 28001},
				email: "a@x",
			}
			let slim: SlimPerson = full as SlimPerson
			echo(slim.name + " in " + slim.addr.city + " #" + str(slim.addr.zip))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "Alice in Madrid #28001" {
		t.Errorf("got %q", got)
	}
}

func TestCastRejectsMissingField(t *testing.T) {
	src := `
		type Small = { name: string }
		type Big   = { name: string, age: number }
		func main(): void {
			let s: Small = Small{name: "X"}
			let b: Big   = s as Big
			echo(b.name)
		}
	`
	_, errs := compileExpectError(t, src)
	if len(errs) == 0 {
		t.Fatalf("expected type error for cast missing field")
	}
	joined := strings.ToLower(joinErrs(errs))
	if !strings.Contains(joined, "no field") {
		t.Errorf("expected 'no field' in error, got: %v", errs)
	}
}

func TestCastRejectsTypeMismatch(t *testing.T) {
	src := `
		type A = { x: string }
		type B = { x: number }
		func main(): void {
			let a: A = A{x: "hi"}
			let b: B = a as B
			echo(str(b.x))
		}
	`
	_, errs := compileExpectError(t, src)
	if len(errs) == 0 {
		t.Fatalf("expected type error for incompatible field type")
	}
}

func TestCastRejectsCastFromPrimitive(t *testing.T) {
	src := `
		type A = { x: number }
		func main(): void {
			let a: A = 42 as A
			echo(str(a.x))
		}
	`
	_, errs := compileExpectError(t, src)
	if len(errs) == 0 {
		t.Fatalf("expected error casting primitive to record")
	}
}

func TestCastChainedFromCall(t *testing.T) {
	// Result of a record-returning call piped through `as` must work.
	sh := compile(t, `
		type RawUser   = { name: string, age: number, email: string }
		type SlimUser  = { name: string, age: number }
		func make(n: string, a: number): RawUser {
			return RawUser{name: n, age: a, email: "x"}
		}
		func main(): void {
			let s: SlimUser = make("Carol", 40) as SlimUser
			echo(s.name + "/" + str(s.age))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "Carol/40" {
		t.Errorf("got %q", got)
	}
}

func TestCastOptionalFields(t *testing.T) {
	// Source has optional name; target's optional field must carry the
	// __null sidecar through the cast.
	sh := compile(t, `
		type Raw  = { name: string?, age: number, ext: string }
		type Slim = { name: string?, age: number }
		func main(): void {
			let r: Raw = Raw{name: null, age: 40, ext: "x"}
			let s: Slim = r as Slim
			echo(str(s.age) + "/" + (s.name ?? "<none>"))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "40/<none>" {
		t.Errorf("got %q", got)
	}
}
