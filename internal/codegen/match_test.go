package codegen_test

import "testing"

func TestMatchInts(t *testing.T) {
	sh := compile(t, `
		func describe(n: number): string {
			match n {
				0 => return "zero"
				1 | 2 | 3 => return "small"
				_ => return "many"
			}
			return "unreachable"
		}
		func main(): void {
			echo(describe(0))
			echo(describe(2))
			echo(describe(99))
		}
	`)
	out := runShell(t, sh)
	want := "zero\nsmall\nmany\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestMatchStrings(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let cmd = "build"
			match cmd {
				"build" | "compile" => echo("compiling")
				"run" => echo("running")
				_ => echo("unknown")
			}
		}
	`)
	out := runShell(t, sh)
	if out != "compiling\n" {
		t.Errorf("got %q", out)
	}
}

func TestMatchEscapesGlob(t *testing.T) {
	// A pattern containing a `*` must match literally, not as a glob.
	sh := compile(t, `
		func main(): void {
			let s = "abc"
			match s {
				"a*" => echo("globbed")
				_ => echo("literal")
			}
		}
	`)
	out := runShell(t, sh)
	if out != "literal\n" {
		t.Errorf("got %q", out)
	}
}

func TestMatchBool(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let b = true
			match b {
				true => echo("yes")
				false => echo("no")
			}
		}
	`)
	out := runShell(t, sh)
	if out != "yes\n" {
		t.Errorf("got %q", out)
	}
}
