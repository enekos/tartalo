package codegen_test

import (
	"strings"
	"testing"
)

// TestSh_MockEnvOverride verifies that mockEnv-set names are returned by env()
// inside the test, while the override doesn't leak into the next test (the
// harness wraps each test in `( )`, which gives us reset-for-free).
func TestSh_MockEnvOverride(t *testing.T) {
	src := `
test "override sets value" {
  mockEnv("TT_X", "yes")
  assertEq(env("TT_X") ?? "<u>", "yes")
}
test "null override marks unset" {
  mockEnv("TT_HOME_TEST", null)
  if env("TT_HOME_TEST") == null { check(true) } else { fail("expected unset") }
}
test "non-mocked names fall through" {
  mockEnv("TT_MOCK_OTHER", "x")
  assertEq(env("TT_MOCK_OTHER") ?? "<u>", "x")
}
`
	out, code := compileAndRunTest(t, src)
	if code != 0 {
		t.Errorf("expected pass, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "3 passed") {
		t.Errorf("missing summary in:\n%s", out)
	}
}

func TestSh_MockNow(t *testing.T) {
	src := `
test "frozen time" {
  mockNow(1700000000)
  assertEq(now(), 1700000000)
}
`
	out, code := compileAndRunTest(t, src)
	if code != 0 {
		t.Errorf("expected pass, got exit %d:\n%s", code, out)
	}
}

func TestSh_MockArgs(t *testing.T) {
	src := `
test "args overridden" {
  mockArgs(["a", "b", "c"])
  let xs = args()
  assertEq(len(xs), 3)
  assertEq(xs[2], "c")
}
`
	out, code := compileAndRunTest(t, src)
	if code != 0 {
		t.Errorf("expected pass, got exit %d:\n%s", code, out)
	}
}

func TestSh_MockReadStdin(t *testing.T) {
	src := `
test "canned stdin" {
  mockReadStdin("hello world")
  assertEq(readStdin(), "hello world")
}
`
	out, code := compileAndRunTest(t, src)
	if code != 0 {
		t.Errorf("expected pass, got exit %d:\n%s", code, out)
	}
}

// The complex mocks (exec/fetch/readFile) are advertised as native-only on
// the sh backend. They abort with a clear message at runtime — at compile
// time they are valid (so `tartalo check` passes) but reaching the call in
// a test fails the test.
func TestSh_MockExecAbortsWithGuidance(t *testing.T) {
	src := `
test "exec mock not supported in sh" {
  mockExec("git", Process{code: 0, ok: true, stdout: "", stderr: ""})
}
`
	out, code := compileAndRunTest(t, src)
	if code == 0 {
		t.Errorf("expected non-zero exit, got 0\n%s", out)
	}
	if !strings.Contains(out, "mockExec requires --target=native") {
		t.Errorf("expected guidance message, got:\n%s", out)
	}
}
