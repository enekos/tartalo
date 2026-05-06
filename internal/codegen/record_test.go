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

func TestAcceptArrayOfRecordsEmpty(t *testing.T) {
	sh := compile(t, `
		type P = { a: string }
		func main(): void {
			let xs: P[] = []
			echo(str(len(xs)))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "0" {
		t.Errorf("got %q", got)
	}
}

func TestArrayOfRecordsLitAndIndex(t *testing.T) {
	sh := compile(t, `
		type Person = { name: string, age: number }
		func main(): void {
			let people: Person[] = [
				Person{name: "Alice", age: 30},
				Person{name: "Bob", age: 25},
				Person{name: "Carol", age: 41},
			]
			echo(str(len(people)))
			echo(people[0].name + ":" + str(people[0].age))
			echo(people[2].name + ":" + str(people[2].age))
		}
	`)
	out := runShell(t, sh)
	want := "3\nAlice:30\nCarol:41\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestArrayOfRecordsForIn(t *testing.T) {
	sh := compile(t, `
		type Person = { name: string, age: number }
		func main(): void {
			let people: Person[] = [
				Person{name: "Alice", age: 30},
				Person{name: "Bob", age: 25},
			]
			for p in people {
				echo(p.name + "/" + str(p.age))
			}
		}
	`)
	out := runShell(t, sh)
	want := "Alice/30\nBob/25\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestArrayOfRecordsPassAndReturn(t *testing.T) {
	sh := compile(t, `
		type Person = { name: string, age: number }
		func first(xs: Person[]): Person {
			return xs[0]
		}
		func make(): Person[] {
			return [Person{name: "X", age: 1}, Person{name: "Y", age: 2}]
		}
		func main(): void {
			let xs: Person[] = make()
			let p: Person = first(xs)
			echo(p.name + ":" + str(p.age))
			echo(str(len(xs)))
		}
	`)
	out := runShell(t, sh)
	want := "X:1\n2\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestArrayOfRecordsNested(t *testing.T) {
	sh := compile(t, `
		type Addr = { city: string, zip: number }
		type Person = { name: string, addr: Addr }
		func main(): void {
			let xs: Person[] = [
				Person{name: "Alice", addr: Addr{city: "Madrid", zip: 28001}},
				Person{name: "Bob", addr: Addr{city: "Bilbao", zip: 48000}},
			]
			echo(xs[0].name + "/" + xs[0].addr.city + "/" + str(xs[0].addr.zip))
			echo(xs[1].name + "/" + xs[1].addr.city + "/" + str(xs[1].addr.zip))
		}
	`)
	out := runShell(t, sh)
	want := "Alice/Madrid/28001\nBob/Bilbao/48000\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestNestedRecordConstructAndAccess(t *testing.T) {
	sh := compile(t, `
		type Addr = { city: string, zip: number }
		type Person = { name: string, addr: Addr }
		func main(): void {
			let p: Person = Person{name: "Alice", addr: Addr{city: "Madrid", zip: 28001}}
			echo(p.name + " in " + p.addr.city + " (" + str(p.addr.zip) + ")")
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "Alice in Madrid (28001)" {
		t.Errorf("got %q", got)
	}
}

func TestNestedRecordFieldAssignment(t *testing.T) {
	sh := compile(t, `
		type Addr = { city: string, zip: number }
		type Person = { name: string, addr: Addr }
		func main(): void {
			let p: Person = Person{name: "Bob", addr: Addr{city: "Bilbao", zip: 48000}}
			p.addr.zip = p.addr.zip + 1
			p.addr.city = "Donostia"
			echo(p.addr.city + ":" + str(p.addr.zip))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "Donostia:48001" {
		t.Errorf("got %q", got)
	}
}

func TestNestedRecordReplaceWholeSubrecord(t *testing.T) {
	sh := compile(t, `
		type Addr = { city: string, zip: number }
		type Person = { name: string, addr: Addr }
		func main(): void {
			let p: Person = Person{name: "Bob", addr: Addr{city: "x", zip: 1}}
			p.addr = Addr{city: "y", zip: 2}
			echo(p.addr.city + ":" + str(p.addr.zip))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "y:2" {
		t.Errorf("got %q", got)
	}
}

func TestNestedRecordPassedToFunction(t *testing.T) {
	sh := compile(t, `
		type Addr = { city: string, zip: number }
		type Person = { name: string, addr: Addr }
		func describe(p: Person): string {
			return p.name + "@" + p.addr.city + "/" + str(p.addr.zip)
		}
		func main(): void {
			let p: Person = Person{name: "Carol", addr: Addr{city: "NYC", zip: 10001}}
			echo(describe(p))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "Carol@NYC/10001" {
		t.Errorf("got %q", got)
	}
}

func TestNestedRecordReturnedFromFunction(t *testing.T) {
	sh := compile(t, `
		type Addr = { city: string, zip: number }
		type Person = { name: string, addr: Addr }
		func make(): Person {
			return Person{name: "Dan", addr: Addr{city: "Tokyo", zip: 100}}
		}
		func main(): void {
			let p: Person = make()
			echo(p.name + "/" + p.addr.city + "/" + str(p.addr.zip))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "Dan/Tokyo/100" {
		t.Errorf("got %q", got)
	}
}

func TestNestedRecordCopyOnAssign(t *testing.T) {
	// Aliasing a nested record must copy every leaf, including the inner ones.
	sh := compile(t, `
		type Addr = { city: string, zip: number }
		type Person = { name: string, addr: Addr }
		func main(): void {
			let a: Person = Person{name: "A", addr: Addr{city: "x", zip: 1}}
			let b: Person = a
			b.addr.city = "y"
			b.addr.zip = 99
			echo(a.addr.city + "/" + str(a.addr.zip) + "," + b.addr.city + "/" + str(b.addr.zip))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "x/1,y/99" {
		t.Errorf("got %q", got)
	}
}

func TestRecordWithStringArrayField(t *testing.T) {
	sh := compile(t, `
		type Tagged = { name: string, tags: string[] }
		func main(): void {
			let t: Tagged = Tagged{name: "post", tags: ["a", "b", "c"]}
			echo(t.name + ":" + str(len(t.tags)) + ":" + t.tags[1])
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "post:3:b" {
		t.Errorf("got %q", got)
	}
}

func TestRecordWithNumberArrayField(t *testing.T) {
	sh := compile(t, `
		type Hist = { name: string, counts: number[] }
		func sum(h: Hist): number {
			let total: number = 0
			for c in h.counts {
				total = total + c
			}
			return total
		}
		func main(): void {
			let h: Hist = Hist{name: "h", counts: [1, 2, 3, 4]}
			echo(str(sum(h)))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "10" {
		t.Errorf("got %q", got)
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
