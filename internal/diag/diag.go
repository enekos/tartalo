// Package diag carries structured compiler diagnostics: the position, the
// message, and optional hints/suggestions/notes that turn a one-line error
// into a useful, debuggable explanation.
//
// A *Diag also satisfies the error interface, returning the legacy
// "file:line:col: message" form so callers that still treat compiler output
// as a flat error list (LSP regex, existing tests) keep working.
package diag

import (
	"fmt"
	"strings"

	"github.com/enekos/tartalo/internal/token"
)

type Severity int

const (
	Error Severity = iota
	Warning
)

// Note is a secondary location the diagnostic wants to point at — for
// example, the previous declaration in a "duplicate name" error.
type Note struct {
	Pos token.Pos
	Msg string
}

// Diag is one structured compiler error.
type Diag struct {
	Pos      token.Pos
	End      token.Pos // optional; zero End means a single caret at Pos
	Severity Severity
	Code     string // optional short identifier, e.g. "E0001"
	Msg      string
	Hint     string // a "= help: ..." line
	// Suggest, when non-empty, is text the renderer can present as a
	// concrete replacement ("did you mean `xs.length`?"). Pair with Hint
	// when you want both a longer prose hint and a copyable replacement.
	Suggest string
	Notes   []Note
}

// Error returns the legacy "file:line:col: msg" string. Stable so existing
// tests that grep error output keep matching.
func (d *Diag) Error() string {
	return fmt.Sprintf("%s: %s", d.Pos, d.Msg)
}

// New builds a basic error diag at the given position.
func New(pos token.Pos, msg string) *Diag {
	return &Diag{Pos: pos, Msg: msg}
}

// Newf is the printf-style constructor.
func Newf(pos token.Pos, format string, args ...any) *Diag {
	return &Diag{Pos: pos, Msg: fmt.Sprintf(format, args...)}
}

func (d *Diag) WithEnd(end token.Pos) *Diag   { d.End = end; return d }
func (d *Diag) WithCode(code string) *Diag    { d.Code = code; return d }
func (d *Diag) WithHint(hint string) *Diag    { d.Hint = hint; return d }
func (d *Diag) WithSuggest(text string) *Diag { d.Suggest = text; return d }
func (d *Diag) WithSeverity(s Severity) *Diag { d.Severity = s; return d }
func (d *Diag) WithNote(p token.Pos, m string) *Diag {
	d.Notes = append(d.Notes, Note{Pos: p, Msg: m})
	return d
}

// As returns the *Diag if e is one (directly), nil otherwise. Plain helper
// — callers can also use errors.As.
func As(e error) *Diag {
	if d, ok := e.(*Diag); ok {
		return d
	}
	return nil
}

// FromErrors converts a mixed []error into []*Diag, falling back to a
// position-less diag for any error that isn't already structured. Used by
// the CLI to render uniformly without requiring every producer to be
// converted at once.
func FromErrors(errs []error) []*Diag {
	out := make([]*Diag, 0, len(errs))
	for _, e := range errs {
		if d := As(e); d != nil {
			out = append(out, d)
			continue
		}
		out = append(out, &Diag{Msg: e.Error()})
	}
	return out
}

// SeverityString gives the lowercase prefix used in rendered output.
func (s Severity) String() string {
	switch s {
	case Warning:
		return "warning"
	default:
		return "error"
	}
}

// EndOrPos returns End if set (Line>0), else Pos. Useful for renderers that
// want a single position for the squiggle's right edge.
func (d *Diag) EndOrPos() token.Pos {
	if d.End.Line > 0 {
		return d.End
	}
	return d.Pos
}

// Suggest returns a copy-pastable suggestion line, or "" if none.
func (d *Diag) suggestLine() string {
	if d.Suggest == "" {
		return ""
	}
	return strings.TrimSpace(d.Suggest)
}
