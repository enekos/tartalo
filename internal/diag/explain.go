package diag

import (
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed explain/*.md
var explainFS embed.FS

// Explain returns the long-form explanation for a stable diagnostic code, or
// ("", false) when no explanation is bundled for that code. The caller picks
// the format — the returned string is markdown-flavoured plain text suitable
// for terminal display.
func Explain(code string) (string, bool) {
	code = strings.TrimSpace(code)
	if code == "" {
		return "", false
	}
	data, err := explainFS.ReadFile("explain/" + code + ".md")
	if err != nil {
		return "", false
	}
	return string(data), true
}

// ListExplained returns every code that has a bundled explanation, sorted.
func ListExplained() []string {
	entries, err := fs.ReadDir(explainFS, "explain")
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".md") {
			out = append(out, strings.TrimSuffix(name, ".md"))
		}
	}
	sort.Strings(out)
	return out
}

// FormatExplainList returns a printable table of every documented code with
// its first non-blank line as a short summary.
func FormatExplainList() string {
	codes := ListExplained()
	var b strings.Builder
	for _, c := range codes {
		body, ok := Explain(c)
		if !ok {
			continue
		}
		summary := firstNonBlankLine(body)
		summary = strings.TrimPrefix(summary, "# ")
		fmt.Fprintf(&b, "%-10s  %s\n", c, summary)
	}
	return b.String()
}

func firstNonBlankLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			return t
		}
	}
	return ""
}
