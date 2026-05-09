package nativegen_test

import (
	"strings"
	"testing"
)

// Native counterparts of the sh map<K, V> tests in
// internal/codegen/map_test.go. The bodies are intentionally identical so
// any divergence in observable behaviour shows up as a test diff.

func TestNativeMapNewAndLen(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let m: map<string, number> = mapNew()
			echo(str(mapLen(m)))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "0" {
		t.Errorf("got %q want %q", got, "0")
	}
}

func TestNativeMapSetGetMissingFound(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let m0: map<string, number> = mapNew()
			let m1: map<string, number> = mapSet(m0, "alice", 30)
			let m2: map<string, number> = mapSet(m1, "bob", 25)
			let av: number? = mapGet(m2, "alice")
			let cv: number? = mapGet(m2, "carol")
			echo(str(av ?? -1))
			echo(str(cv ?? -1))
			echo(str(mapLen(m2)))
		}
	`)
	want := "30\n-1\n2\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeMapSetReplacesExisting(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let m0: map<string, number> = mapNew()
			let m1: map<string, number> = mapSet(m0, "k", 1)
			let m2: map<string, number> = mapSet(m1, "k", 99)
			echo(str(mapGet(m2, "k") ?? -1))
			echo(str(mapLen(m2)))
		}
	`)
	want := "99\n1\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeMapHas(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let m0: map<string, number> = mapNew()
			let m: map<string, number> = mapSet(m0, "x", 1)
			if mapHas(m, "x") { echo("yes") } else { echo("no") }
			if mapHas(m, "y") { echo("yes") } else { echo("no") }
		}
	`)
	want := "yes\nno\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeMapDelete(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let m0: map<string, number> = mapNew()
			let m1: map<string, number> = mapSet(m0, "a", 1)
			let m2: map<string, number> = mapSet(m1, "b", 2)
			let m3: map<string, number> = mapDelete(m2, "a")
			echo(str(mapLen(m3)))
			echo(str(mapGet(m3, "a") ?? -1))
			echo(str(mapGet(m3, "b") ?? -1))
		}
	`)
	want := "1\n-1\n2\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// Iteration order is sorted-by-key on both backends; this test pins that
// guarantee for the native side. The matching codegen test in the sh
// package asserts the same expected output.
func TestNativeMapKeysAndValuesSorted(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let m0: map<string, number> = mapNew()
			let m1: map<string, number> = mapSet(m0, "charlie", 3)
			let m2: map<string, number> = mapSet(m1, "alice",   1)
			let m3: map<string, number> = mapSet(m2, "bob",     2)
			for k in mapKeys(m3) { echo(k) }
			for v in mapValues(m3) { echo(str(v)) }
		}
	`)
	want := "alice\nbob\ncharlie\n1\n2\n3\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeMapWithNumberKeys(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let m0: map<number, string> = mapNew()
			let m1: map<number, string> = mapSet(m0, 1, "one")
			let m2: map<number, string> = mapSet(m1, 2, "two")
			echo(mapGet(m2, 1) ?? "?")
			echo(mapGet(m2, 2) ?? "?")
			echo(mapGet(m2, 3) ?? "?")
		}
	`)
	want := "one\ntwo\n?\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// Record-valued maps are supported on the native backend (Go's map[K]Struct
// composes naturally). The sh backend rejects this at codegen time with a
// runtime error pointing users to --target=native.
func TestNativeMapWithRecordValues(t *testing.T) {
	bin := build(t, `
		type Person = { name: string, age: number }

		func main(): void {
			let m0: map<string, Person> = mapNew()
			let m1: map<string, Person> = mapSet(m0, "alice", Person{name: "Alice", age: 30})
			let m2: map<string, Person> = mapSet(m1, "bob",   Person{name: "Bob",   age: 25})
			let p: Person? = mapGet(m2, "alice")
			if p != null {
				echo(p.name)
				echo(str(p.age))
			}
			echo(str(mapLen(m2)))
			for k in mapKeys(m2) { echo(k) }
		}
	`)
	want := "Alice\n30\n2\nalice\nbob\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
