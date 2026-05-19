// Doctor audits the host PATH for the external tools tartalo's emitted
// scripts and native build pipeline depend on. The check is conservative:
// the presence of a binary on PATH is reported as a pass even if the version
// is too old; the goal is to surface "command not found" failures early
// rather than during a real run.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
)

// doctorReport is the structured shape behind `tartalo doctor --json`.
type doctorReport struct {
	Ok        bool          `json:"ok"`
	Host      doctorHost    `json:"host"`
	Tools     []doctorEntry `json:"tools"`
	MissingOK []string      `json:"missingOk,omitempty"`
}

type doctorHost struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
}

type doctorEntry struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	UsedBy   string `json:"usedBy"`
	Found    bool   `json:"found"`
	Path     string `json:"path,omitempty"`
	Version  string `json:"version,omitempty"`
	Hint     string `json:"hint,omitempty"`
}

// toolCheck describes a single tool we probe for. `versionArg` is the flag we
// invoke to extract a version string; the first non-blank line of combined
// stdout/stderr is reported. When `optional` alternates are present, finding
// any of them on PATH satisfies the check.
type toolCheck struct {
	name        string
	usedBy      string
	required    bool
	alternates  []string // additional binary names that also satisfy this check
	versionArg  string
	missingHint string
}

func doctorChecks() []toolCheck {
	return []toolCheck{
		{
			name:        "sh",
			usedBy:      "running scripts emitted by --target=sh",
			required:    true,
			missingHint: "every POSIX system ships /bin/sh; this should never be missing",
		},
		{
			name:        "awk",
			usedBy:      "arithmetic on float, regex builtins, slice/byteLen, vSum/vMean, etc.",
			required:    true,
			versionArg:  "--version",
			missingHint: "install with apt/brew/pkg: `gawk` (Linux) or pre-installed on macOS",
		},
		{
			name:        "jq",
			usedBy:      "jsonGet, jsonHas, jsonArray (the JSON helpers)",
			required:    false,
			versionArg:  "--version",
			missingHint: "install with `brew install jq` or your distro package manager",
		},
		{
			name:        "curl",
			usedBy:      "fetch, fetchTimeout, fetchHeaders, postJson, postForm, request",
			required:    false,
			versionArg:  "--version",
			missingHint: "install with `brew install curl` or your distro package manager",
		},
		{
			name:        "shellcheck",
			usedBy:      "the default sh verification gate (skip with --no-verify)",
			required:    false,
			versionArg:  "--version",
			missingHint: "install with `brew install shellcheck` or `apt install shellcheck`",
		},
		{
			name:        "timeout",
			usedBy:      "execTimeout and fetchTimeout",
			required:    false,
			alternates:  []string{"gtimeout"},
			versionArg:  "--version",
			missingHint: "macOS: `brew install coreutils` provides `gtimeout`",
		},
		{
			name:        "go",
			usedBy:      "--target=native builds (compiling the emitted Go program)",
			required:    false,
			versionArg:  "version",
			missingHint: "install from https://go.dev/dl/ (only needed for --target=native)",
		},
	}
}

func buildDoctorReport() doctorReport {
	checks := doctorChecks()
	out := doctorReport{
		Ok:    true,
		Host:  doctorHost{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH},
		Tools: make([]doctorEntry, 0, len(checks)),
	}
	for _, c := range checks {
		entry := probeTool(c)
		out.Tools = append(out.Tools, entry)
		if !entry.Found && c.required {
			out.Ok = false
		}
		if !entry.Found && !c.required {
			out.MissingOK = append(out.MissingOK, c.name)
		}
	}
	return out
}

func probeTool(c toolCheck) doctorEntry {
	candidates := append([]string{c.name}, c.alternates...)
	for _, bin := range candidates {
		path, err := exec.LookPath(bin)
		if err != nil {
			continue
		}
		entry := doctorEntry{
			Name:     bin,
			Required: c.required,
			UsedBy:   c.usedBy,
			Found:    true,
			Path:     path,
		}
		if c.versionArg != "" {
			entry.Version = firstVersionLine(bin, c.versionArg)
		}
		return entry
	}
	return doctorEntry{
		Name:     c.name,
		Required: c.required,
		UsedBy:   c.usedBy,
		Found:    false,
		Hint:     c.missingHint,
	}
}

// firstVersionLine runs `bin <versionArg>` and returns the first non-blank
// line of combined output, trimmed. We split the version arg on spaces so
// callers can pass e.g. `-c 'echo X'` if needed (`shell-string` style — we
// don't currently use that form but it's harmless).
func firstVersionLine(bin, versionArg string) string {
	args := splitArgs(versionArg)
	cmd := exec.Command(bin, args...)
	output, _ := cmd.CombinedOutput()
	for _, line := range strings.Split(string(output), "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			return truncate(t, 80)
		}
	}
	return ""
}

func splitArgs(s string) []string {
	return strings.Fields(s)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// renderDoctorReport writes a terminal-friendly table to w. Color is opt-in
// based on stdout being a TTY (same rule as the diagnostic renderer).
func renderDoctorReport(w io.Writer, r doctorReport) {
	fmt.Fprintf(w, "host: %s/%s\n\n", r.Host.GOOS, r.Host.GOARCH)
	for _, t := range r.Tools {
		var mark, label string
		switch {
		case t.Found:
			mark = "✓"
		case t.Required:
			mark = "✗"
		default:
			mark = "—"
		}
		label = t.Name
		if t.Required {
			label += " (required)"
		}
		fmt.Fprintf(w, "  %s %-22s ", mark, label)
		if t.Found {
			if t.Version != "" {
				fmt.Fprintf(w, "%s\n", t.Version)
			} else {
				fmt.Fprintf(w, "%s\n", t.Path)
			}
		} else {
			fmt.Fprintln(w, "not found on PATH")
		}
		fmt.Fprintf(w, "      used for: %s\n", t.UsedBy)
		if !t.Found && t.Hint != "" {
			fmt.Fprintf(w, "      hint:     %s\n", t.Hint)
		}
	}
	fmt.Fprintln(w)
	if r.Ok {
		fmt.Fprintln(w, "doctor: all required tools present")
		if len(r.MissingOK) > 0 {
			fmt.Fprintf(w, "        optional tools missing: %s\n", strings.Join(r.MissingOK, ", "))
		}
	} else {
		fmt.Fprintln(w, "doctor: one or more required tools missing — install the marked ✗ entries before running tartalo")
	}
}

// newJSONEncoder is a tiny wrapper so cmdDoctor can be json-only without
// pulling encoding/json into main.go. (main.go already keeps its imports tight.)
func newJSONEncoder(w io.Writer) *json.Encoder {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc
}
