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

func TestFramework_DashHarness(t *testing.T) {
	if _, err := exec.LookPath("dash"); err != nil {
		t.Skip("no dash on PATH")
	}
	src := `
test "p1" { assertEq(1, 1) }
test "p2" { check(true) }
test "p3" { assertEq("a", "a") }
test "skip" { skip("nope") }
test "fail" { fail("kaboom") }
`
	tmp := t.TempDir()
	srcPath := filepath.Join(tmp, "x.tt")
	os.WriteFile(srcPath, []byte(src), 0o644)
	modules, _ := loader.Load(srcPath)
	info, _ := checker.New().Check(modules)
	sh := codegen.New(info).EmitModulesTest(modules)
	scriptPath := filepath.Join(tmp, "out.sh")
	os.WriteFile(scriptPath, []byte(sh), 0o755)
	cmd := exec.Command("dash", scriptPath)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("dash should have exited 1 (one test fails); got nil. out:\n%s", out)
	}
	for _, want := range []string{"p1", "p2", "p3", "skip", "fail", "kaboom", "1 failed", "3 passed", "1 skipped"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q. output:\n%s", want, string(out))
		}
	}
}
