package codegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runShellExpectFail writes the script, runs it under /bin/sh, and returns
// the combined stdout/stderr along with the exit code. The test fails only
// if /bin/sh itself can't be invoked.
func runShellExpectFail(t *testing.T, sh string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.sh")
	if err := os.WriteFile(path, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", path)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("could not run sh: %v", err)
		}
	}
	return string(out), code
}

func TestAsIntHappyPath(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let s: string = "42"
			let n: number = asInt(s)
			echo(str(n + 1))
		}
	`)
	if got := strings.TrimRight(runShell(t, sh), "\n"); got != "43" {
		t.Errorf("got %q", got)
	}
}

func TestAsIntNegative(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let n: number = asInt("-7")
			echo(str(n))
		}
	`)
	if got := strings.TrimRight(runShell(t, sh), "\n"); got != "-7" {
		t.Errorf("got %q", got)
	}
}

func TestAsIntFailureCitesLocation(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let n: number = asInt("not-a-number")
			echo(str(n))
		}
	`)
	out, code := runShellExpectFail(t, sh)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got output %q", out)
	}
	if !strings.Contains(out, "type error") || !strings.Contains(out, "expected int") {
		t.Errorf("missing diagnostic: %q", out)
	}
	if !strings.Contains(out, "not-a-number") {
		t.Errorf("expected offending value in error, got %q", out)
	}
	if !strings.Contains(out, "test.tt:") {
		t.Errorf("expected file:line in error, got %q", out)
	}
}

func TestAsIntRejectsFloat(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let n: number = asInt("1.5")
			echo(str(n))
		}
	`)
	out, code := runShellExpectFail(t, sh)
	if code == 0 {
		t.Fatalf("expected failure, got %q", out)
	}
	if !strings.Contains(out, "expected int") {
		t.Errorf("got %q", out)
	}
}

func TestAsFloatHappyPath(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let f: float = asFloat("3.14")
			echo(formatFloat(f, 2))
		}
	`)
	if got := strings.TrimRight(runShell(t, sh), "\n"); got != "3.14" {
		t.Errorf("got %q", got)
	}
}

func TestAsFloatAcceptsScientific(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let f: float = asFloat("1.5e2")
			echo(formatFloat(f, 0))
		}
	`)
	if got := strings.TrimRight(runShell(t, sh), "\n"); got != "150" {
		t.Errorf("got %q", got)
	}
}

func TestAsFloatFailure(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let f: float = asFloat("not-a-float")
			echo(formatFloat(f, 2))
		}
	`)
	out, code := runShellExpectFail(t, sh)
	if code == 0 {
		t.Fatalf("expected failure, got %q", out)
	}
	if !strings.Contains(out, "expected float") || !strings.Contains(out, "not-a-float") {
		t.Errorf("got %q", out)
	}
}

func TestAsBoolHappyPath(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let b1: bool = asBool("true")
			let b2: bool = asBool("false")
			if b1 { echo("b1=t") } else { echo("b1=f") }
			if b2 { echo("b2=t") } else { echo("b2=f") }
		}
	`)
	want := "b1=t\nb2=f\n"
	if got := runShell(t, sh); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestAsBoolFailure(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let b: bool = asBool("yes")
			if b { echo("ok") }
		}
	`)
	out, code := runShellExpectFail(t, sh)
	if code == 0 {
		t.Fatalf("expected failure, got %q", out)
	}
	if !strings.Contains(out, "expected bool") || !strings.Contains(out, "yes") {
		t.Errorf("got %q", out)
	}
}

func TestAsStringIdentity(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let s: string = asString("hello world")
			echo(s)
		}
	`)
	if got := strings.TrimRight(runShell(t, sh), "\n"); got != "hello world" {
		t.Errorf("got %q", got)
	}
}

func TestAsIntFromExecOutput(t *testing.T) {
	// Realistic boundary: convert the exec output to a typed number.
	sh := compile(t, `
		func main(): void {
			let p = exec("printf 12")
			let n: number = asInt(trim(p.stdout))
			echo(str(n * 2))
		}
	`)
	if got := strings.TrimRight(runShell(t, sh), "\n"); got != "24" {
		t.Errorf("got %q", got)
	}
}
