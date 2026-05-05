package codegen_test

import (
	"strings"
	"testing"
)

func TestNegativeNumbers(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let a = -5
			let b = a + 10
			echo(str(a))
			echo(str(b))
			echo(str(-a))
			echo(str(0 - 7))
		}
	`)
	out := runShell(t, sh)
	want := "-5\n5\n5\n-7\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestEmptyArrayIteration(t *testing.T) {
	// An empty array must produce zero loop iterations and not crash.
	sh := compile(t, `
		func main(): void {
			let xs: string[] = []
			echo("before")
			for x in xs { echo("should not see: " + x) }
			echo("after")
		}
	`)
	out := runShell(t, sh)
	want := "before\nafter\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestNestedForLoops(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			for i in 1..4 {
				for j in 1..3 {
					echo(str(i) + "x" + str(j))
				}
			}
		}
	`)
	out := runShell(t, sh)
	want := "1x1\n1x2\n2x1\n2x2\n3x1\n3x2\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestLargeNumberArithmetic(t *testing.T) {
	// Stay well within int64 but exercise multi-digit numbers.
	sh := compile(t, `
		func main(): void {
			let a = 1000000
			let b = 1000000
			echo(str(a * b))
			echo(str(a + b - 1))
		}
	`)
	out := runShell(t, sh)
	want := "1000000000000\n1999999\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestEmptyStringComparison(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let s = ""
			if s == "" { echo("empty") } else { echo("not empty") }
			let t = "x"
			if t == "" { echo("empty") } else { echo("not empty") }
		}
	`)
	out := runShell(t, sh)
	want := "empty\nnot empty\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestTrimOfWhitespaceOnly(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo("[" + trim("   ") + "]")
			echo("[" + trim("\t\t\n\n") + "]")
			echo("[" + trim("") + "]")
		}
	`)
	out := runShell(t, sh)
	want := "[]\n[]\n[]\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestArrayOfBoolAndNumber(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let bs: bool[] = [true, false, true, true]
			let ns = [10, 20, 30]
			let trues = 0
			for b in bs {
				if b { trues = trues + 1 }
			}
			echo(str(trues))
			let total = 0
			for n in ns { total = total + n }
			echo(str(total))
			echo(str(ns[1]))
		}
	`)
	out := runShell(t, sh)
	want := "3\n60\n20\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestRecordReassignThroughField(t *testing.T) {
	// Mutating a single field through repeated assignments must persist
	// across calls and unrelated assignments.
	sh := compile(t, `
		type C = { v: number }
		func main(): void {
			let c: C = C{v: 0}
			for i in 1..6 {
				c.v = c.v + i
			}
			echo(str(c.v))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "15" {
		t.Errorf("got %q", got)
	}
}

func TestMatchFallsThroughToWildcard(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			for i in 0..5 {
				match i {
					0 => echo("zero")
					2 | 3 => echo("two-or-three")
					_ => echo("other:" + str(i))
				}
			}
		}
	`)
	out := runShell(t, sh)
	want := "zero\nother:1\ntwo-or-three\ntwo-or-three\nother:4\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestOptionalRecordInOptionalReturn(t *testing.T) {
	// A function that returns string? where the body conditionally returns
	// "" (non-null) vs. null. The result must round-trip through both branches.
	sh := compile(t, `
		func maybe(b: bool): string? {
			if b { return "yes" }
			return null
		}
		func main(): void {
			let a = maybe(true) ?? "<none>"
			let b = maybe(false) ?? "<none>"
			echo(a)
			echo(b)
			if maybe(true) == null { echo("a-null") }
			if maybe(false) == null { echo("b-null") }
		}
	`)
	out := runShell(t, sh)
	want := "yes\n<none>\nb-null\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestExecOutputCapturesNewlines(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let r = exec("printf 'a\nb\nc\n'")
			let lines = split(r.stdout, "\n")
			echo("count=" + str(len(lines)))
			for l in lines { echo("[" + l + "]") }
		}
	`)
	out := runShell(t, sh)
	// Trailing newline gets stripped by command substitution; the output is "a\nb\nc"
	// so split yields 3 elements.
	want := "count=3\n[a]\n[b]\n[c]\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestStringBuilderPattern(t *testing.T) {
	// Append-style string building inside a loop. Common pattern; catches any
	// codegen bug around mutable string locals.
	sh := compile(t, `
		func main(): void {
			let s = ""
			for i in 0..5 {
				s = s + str(i)
			}
			echo(s)
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "01234" {
		t.Errorf("got %q", got)
	}
}

// TestFallthroughOptionalReturn: a function with an optional return type
// that has a path falling off the end (no explicit `return`) must yield
// null to its caller, not crash with an unbound-variable error.
func TestFallthroughOptionalReturn(t *testing.T) {
	sh := compile(t, `
		func first(b: bool): string? {
			if b { return "yes" }
			// intentionally falls through
		}
		func main(): void {
			let r = first(false)
			if r == null { echo("null") } else { echo("got: " + r!) }
			echo(first(true) ?? "<none>")
		}
	`)
	out := runShell(t, sh)
	want := "null\nyes\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestFallthroughNonOptionalReturn: even a non-optional return shouldn't
// leak stale state from previous calls when the function falls through.
func TestFallthroughNonOptionalReturn(t *testing.T) {
	sh := compile(t, `
		func twice(s: string): string { return s + s }
		func wrong(b: bool): string {
			if b { return "yes" }
			// falls through; result is implementation-defined but must be a string
		}
		func main(): void {
			echo(twice("ab"))
			let r = wrong(false)
			echo("[" + r + "]")
			echo(twice("cd"))
		}
	`)
	out := runShell(t, sh)
	// We don't pin down what `wrong(false)` returns — just that the surrounding
	// calls aren't corrupted by stale __ret state.
	if !strings.Contains(out, "abab") || !strings.Contains(out, "cdcd") {
		t.Errorf("stale __ret leaked into other calls: %q", out)
	}
}

func TestBangAroundComparison(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let x = 5
			if !(x > 10) { echo("not gt 10") }
			if !(x == 5) { echo("not 5") } else { echo("is 5") }
			if !!(x > 0) { echo("positive") }
		}
	`)
	out := runShell(t, sh)
	want := "not gt 10\nis 5\npositive\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestBoolNegationChain(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let t = true
			if !!t { echo("y1") }
			if !!!t { echo("y2") } else { echo("n2") }
		}
	`)
	out := runShell(t, sh)
	want := "y1\nn2\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}
