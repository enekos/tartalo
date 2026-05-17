package codegen_test

import (
	"strings"
	"testing"
)

// --- fold (alias of reduce) ------------------------------------------------

func TestFoldIsAliasOfReduce(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3, 4, 5]
			let sum: number = fold(xs, 0, func(acc: number, x: number): number { return acc + x })
			echo(str(sum))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "15" {
		t.Errorf("got %q want %q", got, "15")
	}
}

func TestFoldString(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: string[] = ["a", "b", "c"]
			let s: string = fold(xs, "", func(acc: string, x: string): string { return acc + x })
			echo(s)
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "abc" {
		t.Errorf("got %q want %q", got, "abc")
	}
}

// --- zip --------------------------------------------------------------------

func TestZipNumbersWithAdd(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3]
			let ys: number[] = [10, 20, 30]
			let zs: number[] = zip(xs, ys, func(x: number, y: number): number { return x + y })
			for z in zs {
				echo(str(z))
			}
		}
	`)
	want := "11\n22\n33\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestZipStringsWithConcat(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: string[] = ["a", "b", "c"]
			let ys: string[] = ["1", "2", "3"]
			let zs: string[] = zip(xs, ys, func(a: string, b: string): string { return a + b })
			for z in zs {
				echo(z)
			}
		}
	`)
	want := "a1\nb2\nc3\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestZipShorterFirst(t *testing.T) {
	// zip stops at min(|xs|, |ys|).
	sh := compile(t, `
		func main(): void {
			let xs: number[] = [1, 2]
			let ys: number[] = [10, 20, 30, 40]
			let zs: number[] = zip(xs, ys, func(x: number, y: number): number { return x + y })
			echo(str(len(zs)))
			for z in zs { echo(str(z)) }
		}
	`)
	want := "2\n11\n22\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestZipShorterSecond(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3, 4]
			let ys: number[] = [10]
			let zs: number[] = zip(xs, ys, func(x: number, y: number): number { return x + y })
			echo(str(len(zs)))
			for z in zs { echo(str(z)) }
		}
	`)
	want := "1\n11\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestZipEmptyLeft(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: number[] = []
			let ys: number[] = [1, 2, 3]
			let zs: number[] = zip(xs, ys, func(x: number, y: number): number { return x + y })
			echo(str(len(zs)))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "0" {
		t.Errorf("got %q want %q", got, "0")
	}
}

func TestZipEmptyRight(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3]
			let ys: number[] = []
			let zs: number[] = zip(xs, ys, func(x: number, y: number): number { return x + y })
			echo(str(len(zs)))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "0" {
		t.Errorf("got %q want %q", got, "0")
	}
}

func TestZipResultTypeBool(t *testing.T) {
	// zip with a predicate result — produces bool[].
	sh := compile(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3, 4]
			let ys: number[] = [2, 2, 2, 2]
			let bs: bool[] = zip(xs, ys, func(x: number, y: number): bool { return x == y })
			for b in bs {
				if b { echo("t") } else { echo("f") }
			}
		}
	`)
	want := "f\nt\nf\nf\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestZipChainedInPipeline(t *testing.T) {
	// Exercise zip with |>, since the user explicitly asked for the
	// pipeline-as-expression form. Pipeline injects the LHS as the first
	// arg of the next call.
	sh := compile(t, `
		func add(x: number, y: number): number { return x + y }
		func dbl(x: number): number { return x * 2 }
		func main(): void {
			let xs: number[] = [1, 2, 3]
			let ys: number[] = [10, 20, 30]
			let zs: number[] = zip(xs, ys, add) |> map(dbl)
			for z in zs { echo(str(z)) }
		}
	`)
	want := "22\n44\n66\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// --- awk --------------------------------------------------------------------

func TestAwkSquareFloats(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: float[] = [1.0, 2.0, 3.0, 4.0]
			let ys: float[] = awk(xs, "x * x")
			for y in ys {
				echo(str(y))
			}
		}
	`)
	want := "1\n4\n9\n16\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestAwkOnNumberArray(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: number[] = [1, 2, 3]
			let ys: float[] = awk(xs, "x + 0.5")
			for y in ys {
				echo(str(y))
			}
		}
	`)
	want := "1.5\n2.5\n3.5\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestAwkEmpty(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: float[] = []
			let ys: float[] = awk(xs, "x * 2")
			echo(str(len(ys)))
		}
	`)
	got := strings.TrimRight(runShell(t, sh), "\n")
	if got != "0" {
		t.Errorf("got %q want %q", got, "0")
	}
}

func TestAwkUsesAwkBuiltins(t *testing.T) {
	// awk's sqrt is the kind of thing the escape hatch exists for —
	// stuff Tartalo doesn't expose directly.
	sh := compile(t, `
		func main(): void {
			let xs: float[] = [1.0, 4.0, 9.0, 16.0]
			let ys: float[] = awk(xs, "sqrt(x)")
			for y in ys {
				echo(str(y))
			}
		}
	`)
	want := "1\n2\n3\n4\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestAwkRejectsInterpolatedExpression(t *testing.T) {
	// The whole point of requiring a literal is that we embed `expr` verbatim
	// into the generated awk source. Allowing interpolation would either
	// permit code injection or require expensive escaping — neither belongs
	// in an escape hatch.
	src := `
		func main(): void {
			let xs: float[] = [1.0, 2.0]
			let expr: string = "x * 2"
			let ys: float[] = awk(xs, "${expr}")
			echo(str(len(ys)))
		}
	`
	_, errs := compileExpectError(t, src)
	if len(errs) == 0 {
		t.Fatalf("expected type error for interpolated awk expr, got none")
	}
	if !strings.Contains(strings.ToLower(joinErrs(errs)), "literal") {
		t.Errorf("expected error mentioning 'literal', got: %v", errs)
	}
}

func TestAwkRejectsStringArray(t *testing.T) {
	src := `
		func main(): void {
			let xs: string[] = ["a", "b"]
			let ys: float[] = awk(xs, "x")
		}
	`
	_, errs := compileExpectError(t, src)
	if len(errs) == 0 {
		t.Fatalf("expected type error for string[] input to awk, got none")
	}
}

func TestZipFunctionShapeMismatchErrors(t *testing.T) {
	src := `
		func main(): void {
			let xs: number[] = [1, 2]
			let ys: string[] = ["a", "b"]
			let zs = zip(xs, ys, func(x: number, y: number): number { return x })
			echo(str(len(zs)))
		}
	`
	_, errs := compileExpectError(t, src)
	if len(errs) == 0 {
		t.Fatalf("expected type error for mismatched zip function, got none")
	}
}

func TestAwkInPipeline(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs: float[] = [1.0, 2.0, 3.0]
			let ys: float[] = xs |> awk("x * x") |> awk("x + 1")
			for y in ys {
				echo(str(y))
			}
		}
	`)
	want := "2\n5\n10\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
