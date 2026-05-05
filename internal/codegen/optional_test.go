package codegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestOptionalLetNull(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let x: string? = null
			let y: string? = "hi"
			echo(x ?? "x-default")
			echo(y ?? "y-default")
		}
	`)
	out := runShell(t, sh)
	want := "x-default\nhi\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestOptionalNullCheck(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let a: string? = "hello"
			let b: string? = null
			if a == null { echo("a-null") } else { echo("a-set") }
			if b == null { echo("b-null") } else { echo("b-set") }
			if a != null { echo("a-not-null") }
			if b != null { echo("b-not-null") }
		}
	`)
	out := runShell(t, sh)
	want := "a-set\nb-null\na-not-null\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestOptionalUnwrap(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let a: string? = "non-null"
			echo(a!)
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "non-null" {
		t.Errorf("got %q", got)
	}
}

func TestOptionalUnwrapPanics(t *testing.T) {
	// Forced unwrap of a null value should abort the script with a diagnostic.
	sh := compile(t, `
		func main(): void {
			let a: string? = null
			echo(a!)
			echo("should not print")
		}
	`)
	dir := t.TempDir()
	path := filepath.Join(dir, "out.sh")
	if err := os.WriteFile(path, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", path)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(string(out), "forced unwrap of null") {
		t.Errorf("expected diagnostic, got %q", out)
	}
	if strings.Contains(string(out), "should not print") {
		t.Error("script continued past failed unwrap")
	}
}

func TestOptionalNumberAndBool(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let n: number? = 42
			let m: number? = null
			echo(str(n ?? -1))
			echo(str(m ?? -1))
			let b: bool? = true
			let c: bool? = null
			if (b ?? false) { echo("b-true") } else { echo("b-false") }
			if (c ?? false) { echo("c-true") } else { echo("c-false") }
		}
	`)
	out := runShell(t, sh)
	want := "42\n-1\nb-true\nc-false\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestEnvReturnsOptional(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let h = env("TARTALO_TEST_VAR")
			echo(h ?? "<unset>")
		}
	`)
	// Run with var unset
	dir := t.TempDir()
	path := filepath.Join(dir, "out.sh")
	if err := os.WriteFile(path, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", path)
	cmd.Env = []string{} // empty env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "<unset>" {
		t.Errorf("unset case: got %q", got)
	}

	// Run with var set
	cmd = exec.Command("/bin/sh", path)
	cmd.Env = []string{"TARTALO_TEST_VAR=hello"}
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	if got := strings.TrimRight(string(out), "\n"); got != "hello" {
		t.Errorf("set case: got %q", got)
	}

	// Run with var set to empty (must NOT be null)
	cmd = exec.Command("/bin/sh", path)
	cmd.Env = []string{"TARTALO_TEST_VAR="}
	out, err = cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	if got := string(out); got != "\n" {
		t.Errorf("empty case: expected single newline, got %q", got)
	}
}

func TestOptionalArgAndReturn(t *testing.T) {
	sh := compile(t, `
		func describe(name: string?): string {
			return "name=" + (name ?? "anon")
		}
		func makeMaybe(b: bool): string? {
			if b { return "real" }
			return null
		}
		func main(): void {
			echo(describe("alice"))
			echo(describe(null))
			echo(describe(makeMaybe(true)))
			echo(describe(makeMaybe(false)))
		}
	`)
	out := runShell(t, sh)
	want := "name=alice\nname=anon\nname=real\nname=anon\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestOptionalRecordField(t *testing.T) {
	sh := compile(t, `
		type User = {
			name: string,
			nickname: string?,
		}
		func display(u: User): string {
			return u.name + " (" + (u.nickname ?? u.name) + ")"
		}
		func main(): void {
			let a: User = User{name: "alice", nickname: "ace"}
			let b: User = User{name: "bob", nickname: null}
			echo(display(a))
			echo(display(b))
		}
	`)
	out := runShell(t, sh)
	want := "alice (ace)\nbob (bob)\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestOptionalAssign(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let x: string? = null
			x = "filled"
			echo(x ?? "empty")
			x = null
			echo(x ?? "empty")
		}
	`)
	out := runShell(t, sh)
	want := "filled\nempty\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestRejectBareNullNoAnnotation(t *testing.T) {
	src := `
		func main(): void {
			let x = null
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, "cannot infer type") {
		t.Fatalf("expected infer error, got %v", errs)
	}
}

func TestRejectAssignTToTNotOptional(t *testing.T) {
	src := `
		func main(): void {
			let x: string? = "hi"
			let y: string = x
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, "type mismatch") {
		t.Fatalf("expected type mismatch, got %v", errs)
	}
}

func TestRejectCoalesceOnNonOptional(t *testing.T) {
	src := `
		func main(): void {
			let x = "hi"
			echo(x ?? "default")
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, "?? requires an optional") {
		t.Fatalf("expected ?? error, got %v", errs)
	}
}

func TestRejectUnwrapOnNonOptional(t *testing.T) {
	src := `
		func main(): void {
			let x = "hi"
			echo(x!)
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, "! requires an optional") {
		t.Fatalf("expected ! error, got %v", errs)
	}
}

func TestRejectCompareOptionalToValue(t *testing.T) {
	src := `
		func main(): void {
			let x: string? = "hi"
			if x == "hi" { echo("yes") }
		}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, "optional values directly") {
		t.Fatalf("expected error, got %v", errs)
	}
}
