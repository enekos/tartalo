package codegen_test

import (
	"strings"
	"testing"
)

// Minimal end-to-end tests for the map<K, V> builtins on the sh target. The
// native counterparts live in internal/nativegen/map_test.go and assert
// byte-identical stdout for the same programs.

func TestMapNewAndLen(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let m: map<string, number> = mapNew()
			echo(str(mapLen(m)))
		}
	`)
	if got := strings.TrimRight(runShell(t, sh), "\n"); got != "0" {
		t.Errorf("got %q want %q", got, "0")
	}
}

func TestMapSetGetMissingFound(t *testing.T) {
	sh := compile(t, `
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
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMapSetReplacesExisting(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let m0: map<string, number> = mapNew()
			let m1: map<string, number> = mapSet(m0, "k", 1)
			let m2: map<string, number> = mapSet(m1, "k", 99)
			echo(str(mapGet(m2, "k") ?? -1))
			echo(str(mapLen(m2)))
		}
	`)
	want := "99\n1\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMapHas(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let m0: map<string, number> = mapNew()
			let m: map<string, number> = mapSet(m0, "x", 1)
			if mapHas(m, "x") { echo("yes") } else { echo("no") }
			if mapHas(m, "y") { echo("yes") } else { echo("no") }
		}
	`)
	want := "yes\nno\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMapDelete(t *testing.T) {
	sh := compile(t, `
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
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMapKeysAndValuesSorted(t *testing.T) {
	sh := compile(t, `
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
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestMapWithNumberKeys(t *testing.T) {
	sh := compile(t, `
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
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// Record-valued maps are not supported on the sh backend. The compiler
// accepts the program (the type is valid) but emits a runtime stub that
// directs users to the native target.
func TestMapWithRecordValuesShRejection(t *testing.T) {
	sh := compile(t, `
		type Person = { name: string, age: number }
		func main(): void {
			let m0: map<string, Person> = mapNew()
			let m: map<string, Person> = mapSet(m0, "a", Person{name: "Alice", age: 30})
			echo(str(mapLen(m)))
		}
	`)
	out, code := runShellExpectFail(t, sh)
	if code == 0 {
		t.Fatalf("expected sh program to fail, got exit 0; output=%q", out)
	}
	if !strings.Contains(out, "requires --target=native") {
		t.Errorf("expected native-target hint in output, got %q", out)
	}
}

func TestMapValuesSurviveSpaces(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let m0: map<string, string> = mapNew()
			let m: map<string, string> = mapSet(m0, "msg", "hello world * $(echo NOPE)")
			echo(mapGet(m, "msg") ?? "?")
		}
	`)
	if got := strings.TrimRight(runShell(t, sh), "\n"); got != "hello world * $(echo NOPE)" {
		t.Errorf("got %q", got)
	}
}
