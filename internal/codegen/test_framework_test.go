package codegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/codegen"
	"github.com/enekos/tartalo/internal/loader"
)

// compileAndRunTest mirrors what the `tartalo test` CLI does: load + check +
// emit a test-mode script, write it to a temp file, run it under /bin/sh, and
// return the captured combined output and exit code.
func compileAndRunTest(t *testing.T, src string) (string, int) {
	t.Helper()
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "suite_test.tt")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	modules, errs := loader.Load(srcPath)
	if len(errs) > 0 {
		t.Fatalf("loader: %v", errs)
	}
	info, errs := checker.New().Check(modules)
	if len(errs) > 0 {
		t.Fatalf("checker: %v", errs)
	}
	sh := codegen.New(info).EmitModulesTest(modules)
	scriptPath := filepath.Join(tmp, "out.sh")
	if err := os.WriteFile(scriptPath, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", scriptPath)
	// Force-disable color so substring assertions are stable.
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if e, ok := err.(*exec.ExitError); ok {
			ee = e
			code = ee.ExitCode()
		} else {
			t.Fatalf("running %s: %v\noutput:\n%s\nscript:\n%s", scriptPath, err, out, sh)
		}
	}
	return string(out), code
}

// TestFramework_AllPass exercises the happy path: every assertion passes,
// exit code is 0, and the summary line counts everything.
func TestFramework_AllPass(t *testing.T) {
	src := `
test "addition" {
  assertEq(1 + 1, 2)
}

test "string ops" {
  assertEq(upper("hi"), "HI")
  check(contains("hello, world", "world"))
}

test "len" {
  let xs: number[] = [1, 2, 3]
  assertEq(len(xs), 3)
}
`
	out, code := compileAndRunTest(t, src)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\noutput:\n%s", code, out)
	}
	for _, want := range []string{"addition", "string ops", "len", "3 passed"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "failed") && !strings.Contains(out, "0 failed") {
		t.Errorf("unexpected failure mention:\n%s", out)
	}
}

// TestFramework_FailsAreReported checks that a deliberately-failing test
// produces a non-zero exit and a clean diff in the output.
func TestFramework_FailsAreReported(t *testing.T) {
	src := `
test "this passes" {
  assertEq("a", "a")
}

test "this fails" {
  assertEq(upper("hi"), "HEY")
}

test "this also fails" {
  check(false)
}
`
	out, code := compileAndRunTest(t, src)
	if code != 1 {
		t.Fatalf("expected exit 1 on failure, got %d\noutput:\n%s", code, out)
	}
	for _, want := range []string{
		"this passes",
		"this fails",
		"assertEq failed",
		"expected: HEY",
		"actual:   HI",
		"check failed",
		"2 failed",
		"1 passed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output:\n%s", want, out)
		}
	}
}

// TestFramework_Skip verifies that skip() yields a "-" line, doesn't count as
// a failure, and exits 0 even when it's the only test.
func TestFramework_Skip(t *testing.T) {
	src := `
test "skipped" {
  skip("not yet")
  assertEq(1, 2)
}

test "passing" {
  assertEq(1, 1)
}
`
	out, code := compileAndRunTest(t, src)
	if code != 0 {
		t.Fatalf("expected exit 0 with only skip+pass, got %d\noutput:\n%s", code, out)
	}
	if !strings.Contains(out, "skipped: not yet") {
		t.Errorf("missing skip reason in output:\n%s", out)
	}
	if !strings.Contains(out, "1 passed") || !strings.Contains(out, "1 skipped") {
		t.Errorf("missing summary counts in output:\n%s", out)
	}
}

// TestFramework_FailMessage verifies that fail() includes the user-supplied
// message in the failure output, with interpolation working as expected.
func TestFramework_FailMessage(t *testing.T) {
	src := `
test "kaboom" {
  let who: string = "alice"
  fail("the floor caved in for ${who}")
}
`
	out, code := compileAndRunTest(t, src)
	if code != 1 {
		t.Fatalf("expected exit 1, got %d\noutput:\n%s", code, out)
	}
	if !strings.Contains(out, "the floor caved in for alice") {
		t.Errorf("expected interpolated fail message in output:\n%s", out)
	}
}

// TestFramework_AssertEqOutsideTest must error at compile time — assertion
// builtins are restricted to test bodies.
func TestFramework_AssertEqOutsideTest(t *testing.T) {
	src := `
func main(): void {
  assertEq(1, 1)
}
`
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "bad.tt")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	modules, errs := loader.Load(srcPath)
	if len(errs) > 0 {
		t.Fatalf("loader unexpectedly errored: %v", errs)
	}
	_, errs = checker.New().Check(modules)
	if len(errs) == 0 {
		t.Fatal("expected a checker error for assertEq outside a test body")
	}
	combined := ""
	for _, e := range errs {
		combined += e.Error() + "\n"
	}
	if !strings.Contains(combined, "test") {
		t.Errorf("expected error to mention test bodies, got: %s", combined)
	}
}

// TestFramework_DuplicateTestName surfaces a clean checker error rather than
// silently overwriting one of the two test functions in codegen.
func TestFramework_DuplicateTestName(t *testing.T) {
	src := `
test "shared" {
  assertEq(1, 1)
}

test "shared" {
  assertEq(2, 2)
}
`
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "dup.tt")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	modules, errs := loader.Load(srcPath)
	if len(errs) > 0 {
		t.Fatalf("loader unexpectedly errored: %v", errs)
	}
	_, errs = checker.New().Check(modules)
	if len(errs) == 0 {
		t.Fatal("expected duplicate-name error")
	}
}
