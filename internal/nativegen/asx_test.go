package nativegen_test

import (
	"os/exec"
	"strings"
	"testing"
)

// runBinExpectFail runs the binary, ignoring the exit code, and returns
// combined output + exit code so tests can assert on runtime panics from
// boundary type assertions.
func runBinExpectFail(t *testing.T, bin string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("could not run binary: %v", err)
		}
	}
	return string(out), code
}

func TestNativeAsIntHappyPath(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let s: string = "42"
			let n: number = asInt(s)
			echo(str(n + 1))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "43" {
		t.Errorf("got %q", got)
	}
}

func TestNativeAsIntFailureCitesLocation(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let n: number = asInt("not-a-number")
			echo(str(n))
		}
	`)
	out, code := runBinExpectFail(t, bin)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got %q", out)
	}
	if !strings.Contains(out, "type error") || !strings.Contains(out, "expected int") {
		t.Errorf("missing diagnostic: %q", out)
	}
	if !strings.Contains(out, "not-a-number") {
		t.Errorf("expected offending value in error, got %q", out)
	}
	if !strings.Contains(out, "prog.tt:") {
		t.Errorf("expected file:line in error, got %q", out)
	}
}

func TestNativeAsFloatHappyPath(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let f: float = asFloat("3.14")
			echo(formatFloat(f, 2))
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "3.14" {
		t.Errorf("got %q", got)
	}
}

func TestNativeAsFloatFailure(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let f: float = asFloat("not-a-float")
			echo(formatFloat(f, 2))
		}
	`)
	out, code := runBinExpectFail(t, bin)
	if code == 0 {
		t.Fatalf("expected failure, got %q", out)
	}
	if !strings.Contains(out, "expected float") || !strings.Contains(out, "not-a-float") {
		t.Errorf("got %q", out)
	}
}

func TestNativeAsBoolBothValues(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let b1: bool = asBool("true")
			let b2: bool = asBool("false")
			if b1 { echo("b1=t") } else { echo("b1=f") }
			if b2 { echo("b2=t") } else { echo("b2=f") }
		}
	`)
	want := "b1=t\nb2=f\n"
	if got := runBin(t, bin); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNativeAsBoolFailure(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let b: bool = asBool("yes")
			if b { echo("ok") }
		}
	`)
	out, code := runBinExpectFail(t, bin)
	if code == 0 {
		t.Fatalf("expected failure, got %q", out)
	}
	if !strings.Contains(out, "expected bool") || !strings.Contains(out, "yes") {
		t.Errorf("got %q", out)
	}
}

func TestNativeAsStringIdentity(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let s: string = asString("hello world")
			echo(s)
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "hello world" {
		t.Errorf("got %q", got)
	}
}
