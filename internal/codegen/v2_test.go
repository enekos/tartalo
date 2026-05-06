package codegen_test

import (
	"os/exec"
	"strings"
	"testing"
)

// --- execTimeout -----------------------------------------------------------

func TestExecTimeoutKillsLongRunner(t *testing.T) {
	if _, err := exec.LookPath("timeout"); err != nil {
		if _, err := exec.LookPath("gtimeout"); err != nil {
			t.Skip("no timeout/gtimeout on PATH")
		}
	}
	sh := compile(t, `
		func main(): void {
			let r = execTimeout("printf hi; sleep 5", 1)
			echo("code=" + str(r.code))
			echo("stdout=" + r.stdout)
		}
	`)
	out := runShell(t, sh)
	// `timeout` exits 124 when it kills the child.
	if !strings.Contains(out, "code=124") {
		t.Errorf("expected timeout exit (124), got:\n%s", out)
	}
	if !strings.Contains(out, "stdout=hi") {
		t.Errorf("partial stdout not captured:\n%s", out)
	}
}

func TestExecTimeoutCompletesWithinBudget(t *testing.T) {
	if _, err := exec.LookPath("timeout"); err != nil {
		if _, err := exec.LookPath("gtimeout"); err != nil {
			t.Skip("no timeout/gtimeout on PATH")
		}
	}
	sh := compile(t, `
		func main(): void {
			let r = execTimeout("printf done", 5)
			echo("code=" + str(r.code))
			echo("stdout=" + r.stdout)
		}
	`)
	out := runShell(t, sh)
	want := "code=0\nstdout=done\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// --- regex -----------------------------------------------------------------

func TestRegexMatch(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			if regexMatch("file42.txt", "[0-9]+") { echo("digits") }
			if !regexMatch("nothing", "[0-9]+") { echo("no digits") }
			if regexMatch("Foo Bar", "^[A-Z][a-z]+ [A-Z][a-z]+$") { echo("titlecase") }
		}
	`)
	out := runShell(t, sh)
	want := "digits\nno digits\ntitlecase\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestRegexFindAndAll(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let first = regexFind("a1 b22 c333", "[0-9]+")
			echo(first ?? "none")
			let all = regexFindAll("a1 b22 c333", "[0-9]+")
			echo("count=" + str(len(all)))
			for n in all { echo("- " + n) }
			let none = regexFind("only letters", "[0-9]+")
			if none == null { echo("no match") }
		}
	`)
	out := runShell(t, sh)
	want := "1\ncount=3\n- 1\n- 22\n- 333\nno match\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestRegexReplace(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo(regexReplace("hello world", "[aeiou]", "_"))
			echo(regexReplace("phone: 555-1234", "[0-9]", "X"))
		}
	`)
	out := runShell(t, sh)
	want := "h_ll_ w_rld\nphone: XXX-XXXX\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// --- floats ----------------------------------------------------------------

func TestFloatLiteralAndArith(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let pi: float = 3.14
			let r: float = 2.0
			echo(formatFloat(pi * r * r, 4))
			echo(formatFloat(pi - 0.14, 2))
			echo(formatFloat(1.0e2 + 3, 1))
		}
	`)
	out := runShell(t, sh)
	want := "12.5600\n3.00\n103.0\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestFloatComparisons(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			if 1.5 < 2.0 { echo("lt") }
			if 1.5 == 1.5 { echo("eq") }
			if 2.0 > 1 { echo("gt-mixed") }
			if 2 == 2.0 { echo("eq-mixed") }
		}
	`)
	out := runShell(t, sh)
	want := "lt\neq\ngt-mixed\neq-mixed\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestFloorCeilRound(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo(str(floor(3.7)) + "/" + str(ceil(3.2)) + "/" + str(round(3.5)))
			echo(str(floor(-1.5)) + "/" + str(ceil(-1.5)) + "/" + str(round(-1.5)))
			echo(str(intOf(2.99)) + "/" + str(intOf(-2.99)))
		}
	`)
	out := runShell(t, sh)
	want := "3/4/4\n-2/-1/-2\n2/-2\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestParseFloat(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let a = parseFloat("3.14")
			if a == null { echo("null") } else { echo(formatFloat(a, 2)) }
			let b = parseFloat("not a number")
			if b == null { echo("b-null") } else { echo("b-bad") }
			let c = parseFloat("1e3")
			if c == null { echo("c-null") } else { echo(formatFloat(c, 0)) }
		}
	`)
	out := runShell(t, sh)
	want := "3.14\nb-null\n1000\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestFloatModuloRejected(t *testing.T) {
	src := `
		func main(): void {
			let x = 3.5 % 2
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, "% requires number operands") {
		t.Fatalf("expected modulo error, got %v", errs)
	}
}

// --- first-class functions / HOF -------------------------------------------

func TestMapFilterReduce(t *testing.T) {
	sh := compile(t, `
		func dbl(n: number): number { return n * 2 }
		func odd(n: number): bool { return n % 2 == 1 }
		func add(a: number, b: number): number { return a + b }
		func main(): void {
			let xs = [1, 2, 3, 4, 5]
			let ys = map(xs, dbl)
			for y in ys { echo("d=" + str(y)) }
			let os = filter(xs, odd)
			for o in os { echo("o=" + str(o)) }
			echo("total=" + str(reduce(xs, 0, add)))
		}
	`)
	out := runShell(t, sh)
	want := "d=2\nd=4\nd=6\nd=8\nd=10\no=1\no=3\no=5\ntotal=15\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestMapWithStrings(t *testing.T) {
	sh := compile(t, `
		func wrap(s: string): string { return "[" + s + "]" }
		func main(): void {
			let out = map(["a", "b", "c"], wrap)
			for x in out { echo(x) }
		}
	`)
	out := runShell(t, sh)
	want := "[a]\n[b]\n[c]\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestFunctionRefVariable(t *testing.T) {
	sh := compile(t, `
		func square(n: number): number { return n * n }
		func main(): void {
			let f: func(number): number = square
			echo(str(f(7)))
			echo(str(f(11)))
		}
	`)
	out := runShell(t, sh)
	want := "49\n121\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestRejectMapBadFunctionArity(t *testing.T) {
	src := `
		func two(a: number, b: number): number { return a + b }
		func main(): void { let x = map([1, 2], two) }
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, "function must take one parameter") {
		t.Fatalf("expected arity error, got %v", errs)
	}
}

func TestRejectFilterNonBoolReturn(t *testing.T) {
	src := `
		func not_pred(n: number): number { return n + 1 }
		func main(): void { let x = filter([1, 2, 3], not_pred) }
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, "predicate must return bool") {
		t.Fatalf("expected predicate-bool error, got %v", errs)
	}
}

func TestReduceWithStringConcat(t *testing.T) {
	sh := compile(t, `
		func cat(acc: string, s: string): string { return acc + s + "/" }
		func main(): void {
			echo(reduce(["a", "b", "c"], "", cat))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "a/b/c/" {
		t.Errorf("got %q", got)
	}
}
