package codegen_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/codegen"
	"github.com/enekos/tartalo/internal/loader"
	"github.com/enekos/tartalo/internal/verify"
)

// TestEmittedShPassesShellcheck is a guardrail regression test: every example
// program in examples/ must compile to sh that shellcheck accepts (after the
// codegen-pattern suppression list). If this fails, the codegen has started
// emitting something that shellcheck thinks is unsafe — investigate the new
// finding before silencing it.
func TestEmittedShPassesShellcheck(t *testing.T) {
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("shellcheck not on PATH")
	}
	repoRoot := findRepoRoot(t)
	examplesDir := filepath.Join(repoRoot, "examples")

	entries := []string{
		"hello.tt", "fizzbuzz.tt", "sum.tt", "lines.tt", "match.tt",
		"array.tt", "record.tt", "strings.tt", "git-summary.tt",
		"files.tt", "config.tt", "fetch.tt", "api.tt", "stats.tt",
		"modules/main.tt",
	}

	for _, entry := range entries {
		entry := entry
		t.Run(strings.ReplaceAll(entry, "/", "_"), func(t *testing.T) {
			path := filepath.Join(examplesDir, entry)
			modules, errs := loader.Load(path)
			if len(errs) > 0 {
				t.Fatalf("loader: %v", errs)
			}
			info, errs := checker.New().Check(modules)
			if len(errs) > 0 {
				t.Fatalf("checker: %v", errs)
			}
			sh := codegen.New(info).EmitModules(modules)
			findings, err := verify.Run(sh)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if len(findings) > 0 {
				t.Fatalf("shellcheck flagged %d issue(s):\n%s\n--script--\n%s",
					len(findings), verify.FormatFindings(findings), sh)
			}
		})
	}
}
