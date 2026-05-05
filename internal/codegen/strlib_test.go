package codegen_test

import (
	"strings"
	"testing"
)

func TestUpperLower(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo(upper("Mix3d Case!"))
			echo(lower("Mix3d Case!"))
		}
	`)
	out := runShell(t, sh)
	want := "MIX3D CASE!\nmix3d case!\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestTrim(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo("[" + trim("   hi  ") + "]")
			echo("[" + trim("\n\tspaced\t\n") + "]")
		}
	`)
	out := runShell(t, sh)
	want := "[hi]\n[spaced]\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestReplaceLiteral(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo(replace("a.b.c.d", ".", "/"))
			// Replace must be literal — the regex char "*" should not match anything.
			echo(replace("abc", "*", "X"))
		}
	`)
	out := runShell(t, sh)
	want := "a/b/c/d\nabc\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestContainsStartsEndsWith(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let s = "hello world"
			if contains(s, "lo wo") { echo("contains:y") } else { echo("contains:n") }
			if contains(s, "nope") { echo("contains2:y") } else { echo("contains2:n") }
			if startsWith(s, "hello") { echo("starts:y") } else { echo("starts:n") }
			if endsWith(s, "world") { echo("ends:y") } else { echo("ends:n") }
			if endsWith(s, "hello") { echo("ends2:y") } else { echo("ends2:n") }
		}
	`)
	out := runShell(t, sh)
	want := "contains:y\ncontains2:n\nstarts:y\nends:y\nends2:n\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestSlice(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let s = "abcdefgh"
			echo(slice(s, 0, 3))
			echo(slice(s, 2, 5))
			echo(slice(s, 4, 100))
			echo("[" + slice(s, 5, 5) + "]")
		}
	`)
	out := runShell(t, sh)
	want := "abc\ncde\nefgh\n[]\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestSplitJoinRoundtrip(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let parts = split("alpha,beta,gamma", ",")
			echo(str(len(parts)))
			for p in parts { echo("- " + p) }
			echo(join(parts, "|"))
		}
	`)
	out := runShell(t, sh)
	want := "3\n- alpha\n- beta\n- gamma\nalpha|beta|gamma\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestSplitWithMultiCharSep(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let parts = split("foo--bar--baz", "--")
			echo(str(len(parts)))
			echo(join(parts, "/"))
		}
	`)
	out := runShell(t, sh)
	want := "3\nfoo/bar/baz\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestExecOk(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let r = exec("printf hello")
			if r.ok { echo("ok") } else { echo("fail") }
			echo("code=" + str(r.code))
			echo("out=[" + r.stdout + "]")
		}
	`)
	out := runShell(t, sh)
	want := "ok\ncode=0\nout=[hello]\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestExecCapturesStderr(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let r = exec("printf oops 1>&2; exit 7")
			echo("code=" + str(r.code))
			if r.ok { echo("ok") } else { echo("fail") }
			echo("stderr=[" + r.stderr + "]")
		}
	`)
	out := runShell(t, sh)
	if !strings.Contains(out, "code=7\nfail\nstderr=[oops]") {
		t.Errorf("got %q", out)
	}
}

func TestExecPipelineIntoStringStdlib(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let r = exec("printf 'a:1\nb:2\nc:3'")
			let lines = split(r.stdout, "\n")
			echo(str(len(lines)))
			for line in lines {
				let parts = split(line, ":")
				echo(parts[0] + "=" + parts[1])
			}
		}
	`)
	out := runShell(t, sh)
	want := "3\na=1\nb=2\nc=3\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestProcessRedeclareErrors(t *testing.T) {
	src := `
		type Process = { foo: string }
		func main(): void {}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, `redeclare predeclared type "Process"`) {
		t.Fatalf("expected redeclaration error, got: %v", errs)
	}
}
