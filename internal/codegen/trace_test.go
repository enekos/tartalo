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

// compileAndRunTraced loads `src` from a temp file, compiles with trace
// enabled, runs the resulting sh, and returns combined output + exit code.
// The source-mapped runtime trace needs the on-disk file to render the
// source line in its frame, so this helper goes through the real loader
// (unlike the in-memory `compile` helper used elsewhere).
func compileAndRunTraced(t *testing.T, src string) (string, int, string) {
	t.Helper()
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "boom.tt")
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
	sh := codegen.New(info).WithTrace(true).EmitModules(modules)
	scriptPath := filepath.Join(tmp, "out.sh")
	if err := os.WriteFile(scriptPath, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", scriptPath)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("running %s: %v\noutput:\n%s\nscript:\n%s", scriptPath, err, out, sh)
		}
	}
	return string(out), code, srcPath
}

// TestTrace_RustStyleFrame is the headline check for source-mapped runtime
// errors: when an asInt failure aborts the script, the EXIT trap should print
// a Rust-style frame with the absolute file path, the failing source line,
// and a caret column marker.
func TestTrace_RustStyleFrame(t *testing.T) {
	src := `func main(): void {
  let x: string = "hello"
  let n: number = asInt(x)
  echo(str(n))
}
`
	out, code, srcPath := compileAndRunTraced(t, src)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0\noutput:\n%s", out)
	}
	wantSubstrings := []string{
		"tartalo: runtime error (exit 1)",
		"--> " + srcPath + ":3:",
		"3 |   let n: number = asInt(x)",
		"  | ", // pipe gutter on the caret line
		"^",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in trace output\n--- output ---\n%s", want, out)
		}
	}
}

// TestTrace_SuccessIsSilent ensures a clean exit produces no trace noise: the
// EXIT trap fires unconditionally but only prints when the exit code is
// non-zero, so happy-path scripts must still produce only their own stdout.
func TestTrace_SuccessIsSilent(t *testing.T) {
	src := `func main(): void {
  echo("hello")
}
`
	out, code, _ := compileAndRunTraced(t, src)
	if code != 0 {
		t.Fatalf("expected exit 0, got %d\noutput:\n%s", code, out)
	}
	if got := strings.TrimRight(out, "\n"); got != "hello" {
		t.Errorf("trace mode polluted stdout: got %q, want %q", got, "hello")
	}
}

// TestTrace_OffByDefault locks in that codegen.New(info).Emit(...) (no
// WithTrace) produces no __tt_loc / __tt_on_exit text. A regression here
// would silently double the size of every shipped .sh.
func TestTrace_OffByDefault(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo("hi")
		}
	`)
	for _, marker := range []string{"__tt_loc", "__tt_on_exit", "trap __tt_on_exit"} {
		if strings.Contains(sh, marker) {
			t.Errorf("trace marker %q leaked into non-traced output", marker)
		}
	}
}
