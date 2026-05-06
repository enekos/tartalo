package codegen_test

import (
	"strings"
	"testing"
)

func TestDeferRunsOnReturn(t *testing.T) {
	sh := compile(t, `
		func work(): string {
			defer { echo("cleanup") }
			return "done"
		}
		func main(): void {
			let r: string = work()
			echo(r)
		}
	`)
	out := runShell(t, sh)
	want := "cleanup\ndone\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestDeferLIFO(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			defer { echo("first") }
			defer { echo("second") }
			defer { echo("third") }
			echo("body")
		}
	`)
	out := runShell(t, sh)
	want := "body\nthird\nsecond\nfirst\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestDeferConditional(t *testing.T) {
	// A defer registered inside an if branch runs only when the branch
	// executed — matching Go's runtime defer registration.
	sh := compile(t, `
		func run(x: number): void {
			defer { echo("always") }
			if x > 0 {
				defer { echo("conditional") }
			}
			echo("body")
		}
		func main(): void {
			run(1)
			echo("---")
			run(0)
		}
	`)
	out := runShell(t, sh)
	want := "body\nconditional\nalways\n---\nbody\nalways\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestDeferSeesLocals(t *testing.T) {
	// Defer body reads the enclosing function's locals at the time it runs.
	sh := compile(t, `
		func work(): void {
			let n: number = 0
			defer { echo("n=" + str(n)) }
			n = 42
		}
		func main(): void { work() }
	`)
	out := runShell(t, sh)
	want := "n=42\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestDeferOnFallThrough(t *testing.T) {
	sh := compile(t, `
		func work(): void {
			defer { echo("cleanup") }
			echo("body")
		}
		func main(): void { work() }
	`)
	out := runShell(t, sh)
	want := "body\ncleanup\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestDeferDoesNotRunOnExit(t *testing.T) {
	// `exit()` aborts the script; defers attached to surrounding functions
	// don't fire (they only run on function return).
	sh := compile(t, `
		func main(): void {
			defer { echo("should-not-run") }
			echo("about-to-exit")
			exit(0)
		}
	`)
	out := runShell(t, sh)
	if !strings.Contains(out, "about-to-exit") {
		t.Errorf("expected body to run, got %q", out)
	}
	if strings.Contains(out, "should-not-run") {
		t.Errorf("defer ran on exit, output was %q", out)
	}
}

func TestDeferRejectReturnInBody(t *testing.T) {
	src := `
		func work(): number {
			defer {
				return 5
			}
			return 0
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, "return is not allowed inside a defer") {
		t.Fatalf("expected return-in-defer error, got: %v", errs)
	}
}

// Top-level `defer` is rejected by the parser (it's not a declaration form),
// so no checker-level test is necessary.
