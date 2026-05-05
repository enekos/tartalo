// Package verify runs shellcheck on emitted sh to catch bugs the codegen
// shouldn't ship. It's the last line of defence between the compiler's output
// and someone running it as `sh script.sh`.
//
// The verifier shells out to the `shellcheck` binary and parses its JSON
// output. A small set of warning codes that fire on intentional codegen
// patterns are suppressed (see suppressed); everything else surfaces as a
// finding.
package verify

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// ErrShellcheckMissing is returned by Run when the `shellcheck` binary cannot
// be found on PATH. Callers should treat this as a configuration error rather
// than a verification failure: the script may be fine, we just can't tell.
var ErrShellcheckMissing = errors.New("shellcheck not found on PATH")

// Finding is one shellcheck diagnostic, after suppression.
type Finding struct {
	Line    int
	Column  int
	Level   string // "error" | "warning" | "info" | "style"
	Code    int    // shellcheck code, e.g. 2086
	Message string
}

// String formats a finding in a compact, grep-friendly form.
func (f Finding) String() string {
	return fmt.Sprintf("line %d:%d %s SC%d: %s", f.Line, f.Column, f.Level, f.Code, f.Message)
}

// suppressed lists shellcheck codes the tartalo codegen knowingly violates.
// Each entry must be justified — adding to this map widens the blast radius
// of "we won't catch X anymore."
var suppressed = map[int]string{
	// `local` is undefined in strict POSIX, but every modern /bin/sh (dash,
	// bash, busybox ash) implements it. The codegen package documents this
	// trade-off explicitly in its package comment.
	3043: "codegen uses `local` intentionally; supported by every real /bin/sh",
	// Record returns are emitted as a fan of `__ret__field` variables; the
	// caller copies the ones it needs into its own scope. Fields the caller
	// doesn't read look unused to shellcheck.
	2034: "record return codegen emits a fan of __ret__field vars; some are unused per call",
	// `${x}` inside `$(( ))` is harmless; the codegen prefers it for visual
	// consistency with non-arithmetic expansions.
	2004: "codegen uses ${x} uniformly inside arithmetic for consistency",
	// Single-quoted runtime error messages contain literal backticks (e.g.,
	// "requires `timeout` on PATH") that shellcheck mistakes for unintended
	// expansion attempts.
	2016: "runtime error messages use single-quoted text with literal backticks",
}

// Suppressed returns the set of shellcheck codes that verify silences. Useful
// for documentation and tests.
func Suppressed() map[int]string {
	out := make(map[int]string, len(suppressed))
	for k, v := range suppressed {
		out[k] = v
	}
	return out
}

// Available reports whether `shellcheck` is on PATH. Use this to decide
// whether to surface a friendly "install shellcheck" error before invoking
// Run, or to gate tests on its presence.
func Available() bool {
	_, err := exec.LookPath("shellcheck")
	return err == nil
}

// Run pipes script into shellcheck (treating it as POSIX sh) and returns the
// findings that survive suppression. A nil error with an empty slice means
// the script passed.
//
// Returns ErrShellcheckMissing if shellcheck is not on PATH.
func Run(script string) ([]Finding, error) {
	if !Available() {
		return nil, ErrShellcheckMissing
	}
	// -s sh        : assume the script is POSIX sh (matches the shebang we emit)
	// -f json1     : one-line JSON output, easy to parse
	// -            : read script from stdin
	cmd := exec.Command("shellcheck", "-s", "sh", "-f", "json1", "-")
	cmd.Stdin = strings.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	// shellcheck exits 1 when it has findings — that's a normal outcome, not
	// an invocation failure. Anything else (exit 2+, signal, etc.) means we
	// couldn't actually run the check.
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() >= 2 {
			return nil, fmt.Errorf("shellcheck invocation failed: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
		}
	}
	var raw struct {
		Comments []struct {
			Line    int    `json:"line"`
			Column  int    `json:"column"`
			Level   string `json:"level"`
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return nil, fmt.Errorf("parse shellcheck output: %w", err)
	}
	findings := make([]Finding, 0, len(raw.Comments))
	for _, c := range raw.Comments {
		if _, skip := suppressed[c.Code]; skip {
			continue
		}
		findings = append(findings, Finding{
			Line:    c.Line,
			Column:  c.Column,
			Level:   c.Level,
			Code:    c.Code,
			Message: c.Message,
		})
	}
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Line != findings[j].Line {
			return findings[i].Line < findings[j].Line
		}
		return findings[i].Column < findings[j].Column
	})
	return findings, nil
}

// FormatFindings renders findings as a multi-line, human-readable block
// suitable for printing to stderr. Returns "" when there are no findings.
func FormatFindings(findings []Finding) string {
	if len(findings) == 0 {
		return ""
	}
	var b strings.Builder
	for _, f := range findings {
		fmt.Fprintln(&b, "  "+f.String())
	}
	return b.String()
}
