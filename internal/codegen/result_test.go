package codegen_test

import (
	"testing"
)

func TestResultOkAndErr(t *testing.T) {
	sh := compile(t, `
		type StrResult = Ok{value: string} | Err{error: string}

		func parseName(input: string): StrResult {
			if input == "" { return Err{error: "empty input"} }
			return Ok{value: "hi " + input}
		}

		func main(): void {
			match parseName("alice") {
				Ok{value} => echo("ok:" + value)
				Err{error} => echo("err:" + error)
			}
			match parseName("") {
				Ok{value} => echo("ok:" + value)
				Err{error} => echo("err:" + error)
			}
		}
	`)
	out := runShell(t, sh)
	want := "ok:hi alice\nerr:empty input\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestResultTryPropagatesErr(t *testing.T) {
	// `?` short-circuits to the enclosing function's matching Err.
	sh := compile(t, `
		type IntResult = Ok{value: number} | Err{error: string}

		func parseInt(s: string): IntResult {
			if s == "bad" { return Err{error: "bad input " + s} }
			return Ok{value: 1}
		}

		func double(s: string): IntResult {
			let n: number = parseInt(s)?
			return Ok{value: n + n}
		}

		func main(): void {
			match double("ok") {
				Ok{value} => echo("ok:" + str(value))
				Err{error} => echo("err:" + error)
			}
			match double("bad") {
				Ok{value} => echo("ok:" + str(value))
				Err{error} => echo("err:" + error)
			}
		}
	`)
	out := runShell(t, sh)
	want := "ok:2\nerr:bad input bad\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestResultTryRunsDefer(t *testing.T) {
	// Defer must fire on early-return via `?`, just like an explicit return.
	sh := compile(t, `
		type R = Ok{value: number} | Err{error: string}

		func failing(): R { return Err{error: "boom"} }

		func work(): R {
			defer { echo("cleanup") }
			let v: number = failing()?
			return Ok{value: v}
		}

		func main(): void {
			match work() {
				Ok{value} => echo("ok:" + str(value))
				Err{error} => echo("err:" + error)
			}
		}
	`)
	out := runShell(t, sh)
	want := "cleanup\nerr:boom\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestResultTryRejectOutsideFunction(t *testing.T) {
	// `?` is only valid where the enclosing function returns a Result-shaped
	// sum; using it elsewhere is a compile error.
	src := `
		type R = Ok{value: number} | Err{error: string}
		func bad(): number {
			let r: R = Ok{value: 1}
			let v: number = r?
			return v
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, "Result-shaped") {
		t.Fatalf("expected Result-shape error, got: %v", errs)
	}
}
