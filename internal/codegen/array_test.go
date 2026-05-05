package codegen_test

import "testing"

func TestArrayLiteralAndIter(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs = ["alpha", "beta", "gamma"]
			for x in xs {
				echo("- " + x)
			}
		}
	`)
	out := runShell(t, sh)
	want := "- alpha\n- beta\n- gamma\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestArrayIndex(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs = ["zero", "one", "two", "three"]
			echo(xs[0])
			echo(xs[2])
		}
	`)
	out := runShell(t, sh)
	want := "zero\ntwo\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestArrayLen(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let xs = [10, 20, 30, 40]
			echo(str(len(xs)))
			let ys: string[] = []
			echo(str(len(ys)))
		}
	`)
	out := runShell(t, sh)
	want := "4\n0\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestArrayOfNumbers(t *testing.T) {
	// Indexing into a number[] should yield a value usable in arithmetic.
	sh := compile(t, `
		func main(): void {
			let ns = [3, 4, 5]
			let total = ns[0] + ns[1] + ns[2]
			echo(str(total))
		}
	`)
	out := runShell(t, sh)
	if out != "12\n" {
		t.Errorf("got %q", out)
	}
}

func TestStringLenStillWorks(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let s = "hello"
			echo(str(len(s)))
		}
	`)
	out := runShell(t, sh)
	if out != "5\n" {
		t.Errorf("got %q", out)
	}
}

func TestRejectArrayTypeMismatch(t *testing.T) {
	src := `
		func main(): void {
			let xs = ["a", 1]
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 {
		t.Fatal("expected element type mismatch error")
	}
}

func TestRejectIndexNonArray(t *testing.T) {
	src := `
		func main(): void {
			let s = "hi"
			echo(s[0])
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 {
		t.Fatal("expected indexing-non-array error")
	}
}
