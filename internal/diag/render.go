package diag

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/enekos/tartalo/internal/token"
)

// Sources is anything that can hand back the raw text of a file the
// diagnostic is pointing at. Returning ("", false) is fine — the renderer
// falls back to a position-only line when source isn't available.
type Sources interface {
	Source(file string) (string, bool)
}

// MapSources is the trivial in-memory implementation: a map keyed by the
// same `File` field that lex/parse stamp into token.Pos.
type MapSources map[string]string

func (m MapSources) Source(f string) (string, bool) {
	s, ok := m[f]
	return s, ok
}

// Render writes a Rust-style frame for each diag to w. If color is true the
// renderer emits ANSI escapes; otherwise plain text.
func Render(w io.Writer, diags []*Diag, src Sources, color bool) {
	st := newStyle(color)
	for i, d := range diags {
		if i > 0 {
			fmt.Fprintln(w)
		}
		renderOne(w, d, src, st)
	}
}

func renderOne(w io.Writer, d *Diag, src Sources, st style) {
	header := d.Severity.String()
	if d.Code != "" {
		header = fmt.Sprintf("%s[%s]", header, d.Code)
	}
	fmt.Fprintf(w, "%s%s%s: %s%s%s\n",
		st.severityColor(d.Severity), header, st.reset,
		st.bold, d.Msg, st.reset)

	if d.Pos.Line > 0 {
		fmt.Fprintf(w, "%s --> %s%s\n", st.dim, d.Pos, st.reset)
	}

	if d.Pos.Line > 0 && src != nil {
		if line, ok := lineOf(src, d.Pos); ok {
			renderFrame(w, d, line, st)
		}
	}

	if d.Hint != "" {
		fmt.Fprintf(w, "  %s=%s %shelp:%s %s\n",
			st.dim, st.reset, st.cyan, st.reset, d.Hint)
	}
	if d.Suggest != "" {
		fmt.Fprintf(w, "  %s=%s %ssuggestion:%s %s\n",
			st.dim, st.reset, st.cyan, st.reset, d.Suggest)
	}
	for _, n := range d.Notes {
		if n.Pos.Line > 0 {
			fmt.Fprintf(w, "  %s=%s %snote:%s %s (%s)\n",
				st.dim, st.reset, st.cyan, st.reset, n.Msg, n.Pos)
		} else {
			fmt.Fprintf(w, "  %s=%s %snote:%s %s\n",
				st.dim, st.reset, st.cyan, st.reset, n.Msg)
		}
	}
}

// renderFrame produces:
//
//	   |
//	12 |     return x
//	   |             ^
func renderFrame(w io.Writer, d *Diag, line string, st style) {
	lineNo := fmt.Sprintf("%d", d.Pos.Line)
	gutter := strings.Repeat(" ", len(lineNo))

	// Strip the trailing newline so the frame doesn't double-space.
	line = strings.TrimRight(line, "\n")

	fmt.Fprintf(w, "  %s%s |%s\n", st.dim, gutter, st.reset)
	fmt.Fprintf(w, "  %s%s |%s %s\n", st.dim, lineNo, st.reset, line)

	caretCol := d.Pos.Col
	if caretCol < 1 {
		caretCol = 1
	}
	caretLen := caretSpan(d, line, caretCol)

	pad := strings.Repeat(" ", caretCol-1)
	carets := strings.Repeat("^", caretLen)
	fmt.Fprintf(w, "  %s%s |%s %s%s%s%s\n",
		st.dim, gutter, st.reset,
		pad, st.severityColor(d.Severity), carets, st.reset)
}

// caretSpan figures out how wide the squiggle should be. If End is on the
// same line as Pos, use the column delta; otherwise default to 1.
func caretSpan(d *Diag, line string, caretCol int) int {
	if d.End.Line == d.Pos.Line && d.End.Col > d.Pos.Col {
		span := d.End.Col - d.Pos.Col
		if span > 0 {
			return span
		}
	}
	// Reasonable fallback: highlight the identifier-like run starting at
	// caretCol so a single-token error gets a visible underline.
	r, w := runeAt(line, caretCol-1)
	if w == 0 {
		return 1
	}
	if isIdentRune(r) {
		end := caretCol - 1 + w
		for end < len(line) {
			rr, ww := runeAt(line, end)
			if ww == 0 || !isIdentRune(rr) {
				break
			}
			end += ww
		}
		// caretSpan is rune-count, not byte count — count runes between
		// caretCol-1 and end.
		return utf8.RuneCountInString(line[caretCol-1 : end])
	}
	return 1
}

func runeAt(s string, i int) (rune, int) {
	if i < 0 || i >= len(s) {
		return 0, 0
	}
	r, w := utf8.DecodeRuneInString(s[i:])
	return r, w
}

func isIdentRune(r rune) bool {
	return r == '_' ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9')
}

// lineOf pulls the 1-based line number out of src for the given Pos.
func lineOf(src Sources, p token.Pos) (string, bool) {
	body, ok := src.Source(p.File)
	if !ok {
		return "", false
	}
	// Walk to the start of line p.Line.
	line := 1
	start := 0
	for i := 0; i < len(body); i++ {
		if line == p.Line {
			break
		}
		if body[i] == '\n' {
			line++
			start = i + 1
		}
	}
	if line != p.Line {
		return "", false
	}
	end := strings.IndexByte(body[start:], '\n')
	if end < 0 {
		return body[start:], true
	}
	return body[start : start+end], true
}

type style struct {
	reset  string
	bold   string
	dim    string
	red    string
	yellow string
	cyan   string
}

func newStyle(color bool) style {
	if !color {
		return style{}
	}
	return style{
		reset:  "\x1b[0m",
		bold:   "\x1b[1m",
		dim:    "\x1b[2m",
		red:    "\x1b[31;1m",
		yellow: "\x1b[33;1m",
		cyan:   "\x1b[36;1m",
	}
}

func (s style) severityColor(sev Severity) string {
	switch sev {
	case Warning:
		return s.yellow
	default:
		return s.red
	}
}
