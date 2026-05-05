package verify_test

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/verify"
)

func requireShellcheck(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Skip("shellcheck not on PATH")
	}
}

func TestRun_CleanScriptHasNoFindings(t *testing.T) {
	requireShellcheck(t)
	// A trivial, fully-quoted POSIX-clean script.
	script := `#!/bin/sh
set -eu
x="hello"
printf '%s\n' "$x"
`
	findings, err := verify.Run(script)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %d:\n%s",
			len(findings), verify.FormatFindings(findings))
	}
}

func TestRun_CatchesUnquotedExpansion(t *testing.T) {
	requireShellcheck(t)
	// Classic word-splitting bug: $1 should be quoted.
	script := `#!/bin/sh
x=$1
echo $x
`
	findings, err := verify.Run(script)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(findings) == 0 {
		t.Fatal("expected SC2086 findings on unquoted expansion, got none")
	}
	got := false
	for _, f := range findings {
		if f.Code == 2086 {
			got = true
			break
		}
	}
	if !got {
		t.Fatalf("expected SC2086 in findings, got: %s", verify.FormatFindings(findings))
	}
}

func TestRun_SuppressesIntentionalCodegenPatterns(t *testing.T) {
	requireShellcheck(t)
	// Each suppressed code in a single script. This is the codegen's exact
	// shape; if any of these started failing, every emitted script would.
	script := `#!/bin/sh
set -eu
greet() {
  local who="$1"
  local n=1
  if [ "$((n + 1))" -gt 0 ]; then
    printf 'requires ` + "`" + `timeout` + "`" + ` on PATH\n' >&2
  fi
  local unused="x"
  printf '%s\n' "$who"
}
greet "world"
`
	findings, err := verify.Run(script)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, f := range findings {
		if _, suppressed := verify.Suppressed()[f.Code]; suppressed {
			t.Errorf("suppressed code SC%d leaked through: %s", f.Code, f)
		}
	}
}

func TestRun_MissingShellcheckSurfacesSentinel(t *testing.T) {
	// Trick exec.LookPath into not finding shellcheck by emptying PATH.
	t.Setenv("PATH", "")
	_, err := verify.Run("#!/bin/sh\n")
	if !errors.Is(err, verify.ErrShellcheckMissing) {
		t.Fatalf("want ErrShellcheckMissing, got %v", err)
	}
}

func TestFormatFindings_Empty(t *testing.T) {
	if got := verify.FormatFindings(nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
}

func TestFormatFindings_NonEmpty(t *testing.T) {
	out := verify.FormatFindings([]verify.Finding{
		{Line: 3, Column: 6, Level: "info", Code: 2086, Message: "Double quote to prevent globbing."},
	})
	if !strings.Contains(out, "SC2086") || !strings.Contains(out, "line 3:6") {
		t.Fatalf("unexpected format: %q", out)
	}
}
