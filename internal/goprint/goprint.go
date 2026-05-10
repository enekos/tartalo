// Package goprint helps emitters produce readable Go source. It is used by
// the native backend (internal/nativegen) so the generated `.go` file looks
// idiomatic when opened in an editor: imports collapse into one sorted block,
// struct fields line up in columns, and binary expressions only wrap in
// parentheses when operator precedence actually requires it.
//
// The package is intentionally small. It does not try to be a full Go AST
// printer — `go/printer` already exists for that — it just provides the
// few primitives nativegen needs to build readable output dynamically.
package goprint

import (
	"sort"
	"strings"
)

// Buf is an indent-aware string builder. Indent levels widen by one tab.
// Callers may write raw text via WriteString / WriteByte or whole lines
// via Line, which prepends the current indent and appends a newline.
type Buf struct {
	b      strings.Builder
	indent int
}

// NewBuf returns a Buf preallocated with capacity hint n bytes.
func NewBuf(n int) *Buf {
	var buf Buf
	buf.b.Grow(n)
	return &buf
}

// String returns the accumulated text.
func (b *Buf) String() string { return b.b.String() }

// Len returns the byte length of the accumulated text.
func (b *Buf) Len() int { return b.b.Len() }

// Indent / Dedent change the current indent depth by one.
func (b *Buf) Indent()        { b.indent++ }
func (b *Buf) Dedent()        { b.indent-- }
func (b *Buf) Depth() int     { return b.indent }
func (b *Buf) SetDepth(n int) { b.indent = n }

// WriteString appends s verbatim without indentation handling.
func (b *Buf) WriteString(s string) { b.b.WriteString(s) }

// WriteByte appends one byte. The error return matches io.ByteWriter so a
// Buf can be passed where a `*strings.Builder` would also satisfy that
// interface; it is always nil.
func (b *Buf) WriteByte(c byte) error { return b.b.WriteByte(c) }

// Newline appends a single '\n'.
func (b *Buf) Newline() { b.b.WriteByte('\n') }

// writeIndentTabs writes the leading indent for the current depth.
func (b *Buf) writeIndentTabs() {
	for i := 0; i < b.indent; i++ {
		b.b.WriteByte('\t')
	}
}

// Line writes one indented line plus a trailing newline.
func (b *Buf) Line(s string) {
	b.writeIndentTabs()
	b.b.WriteString(s)
	b.b.WriteByte('\n')
}

// Linef writes one indented line built by joining parts with no separator.
// Cheaper than Sprintf for the common "literal + identifier" case.
func (b *Buf) Linef(parts ...string) {
	b.writeIndentTabs()
	for _, p := range parts {
		b.b.WriteString(p)
	}
	b.b.WriteByte('\n')
}

// Imports writes a single grouped, sorted, deduplicated `import (...)` block.
// Empty input writes nothing. A single import is rendered as `import "x"`.
func Imports(out *strings.Builder, pkgs []string) {
	if len(pkgs) == 0 {
		return
	}
	uniq := make([]string, 0, len(pkgs))
	seen := make(map[string]struct{}, len(pkgs))
	for _, p := range pkgs {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		uniq = append(uniq, p)
	}
	sort.Strings(uniq)
	if len(uniq) == 1 {
		out.WriteString("import \"")
		out.WriteString(uniq[0])
		out.WriteString("\"\n\n")
		return
	}
	out.WriteString("import (\n")
	for _, p := range uniq {
		out.WriteString("\t\"")
		out.WriteString(p)
		out.WriteString("\"\n")
	}
	out.WriteString(")\n\n")
}

// StructField is one entry in a Go struct definition. Empty Type signals a
// blank-line separator (renders nothing for that row but keeps groupings
// visually distinct in the output).
type StructField struct {
	Name string
	Type string
}

// Struct writes a `type Name struct { ... }` block with field names and
// types padded so the type column lines up across rows. The output ends
// with a newline after the closing brace plus one blank separator line,
// matching the rest of nativegen's emission style.
func Struct(b *Buf, name string, fields []StructField) {
	b.Linef("type ", name, " struct {")
	b.Indent()
	maxName := 0
	for _, f := range fields {
		if len(f.Name) > maxName {
			maxName = len(f.Name)
		}
	}
	for _, f := range fields {
		if f.Type == "" {
			b.Newline()
			continue
		}
		pad := maxName - len(f.Name)
		var line strings.Builder
		line.Grow(len(f.Name) + 1 + pad + len(f.Type))
		line.WriteString(f.Name)
		for i := 0; i < pad; i++ {
			line.WriteByte(' ')
		}
		line.WriteByte(' ')
		line.WriteString(f.Type)
		b.Line(line.String())
	}
	b.Dedent()
	b.Line("}")
	b.Newline()
}

// Operator precedence levels used by Bin/Un parenthesization, mirroring the
// Go spec: higher number = tighter binding. Atoms (identifiers, literals,
// parenthesised expressions, calls, indexing, selectors) are PrecAtom and
// never need wrapping.
const (
	PrecOrOr   = 1 // ||
	PrecAndAnd = 2 // &&
	PrecCmp    = 3 // == != < <= > >=
	PrecAdd    = 4 // + - | ^
	PrecMul    = 5 // * / % << >> & &^
	PrecUnary  = 6 // -x !x *x &x
	PrecAtom   = 7 // ident, literal, call, index, selector, paren
)

// PrecOf returns the Go precedence of the binary operator op. Unknown ops
// fall back to PrecMul so they bind tightly — caller adds parens if unsure.
func PrecOf(op string) int {
	switch op {
	case "||":
		return PrecOrOr
	case "&&":
		return PrecAndAnd
	case "==", "!=", "<", "<=", ">", ">=":
		return PrecCmp
	case "+", "-", "|", "^":
		return PrecAdd
	case "*", "/", "%", "<<", ">>", "&", "&^":
		return PrecMul
	}
	return PrecMul
}

// Expr is a Go expression that knows its own precedence. Render writes the
// expression text into the supplied builder. Implementations are responsible
// for parenthesizing their own operands as needed.
type Expr interface {
	Prec() int
	Render(out *strings.Builder)
}

// RenderString returns e rendered to a string.
func RenderString(e Expr) string {
	var b strings.Builder
	b.Grow(32)
	e.Render(&b)
	return b.String()
}

// Atom is a pre-rendered expression treated as having atom precedence: it
// never needs surrounding parentheses. Use it to wrap identifiers, literals,
// function calls, indexing, selector chains, etc. that the upstream emitter
// produced as raw text.
type Atom struct{ Text string }

func (a Atom) Prec() int                   { return PrecAtom }
func (a Atom) Render(out *strings.Builder) { out.WriteString(a.Text) }

// Raw is a pre-rendered expression with explicit precedence. Use this when
// the upstream text is itself a binary or unary expression and the parent
// needs to know its precedence to decide on parentheses. Compared to Atom,
// Raw can declare itself "loose" (lower precedence) and so the parent will
// wrap it appropriately.
type Raw struct {
	Text string
	P    int
}

func (r Raw) Prec() int                   { return r.P }
func (r Raw) Render(out *strings.Builder) { out.WriteString(r.Text) }

// Bin is a binary expression. It wraps each side in parens only when that
// side's precedence is strictly less than the parent's (left side) or less-
// than-or-equal (right side, since Go's binary ops are all left-associative).
type Bin struct {
	Op  string
	Lhs Expr
	Rhs Expr
}

func (e Bin) Prec() int { return PrecOf(e.Op) }

func (e Bin) Render(out *strings.Builder) {
	parent := PrecOf(e.Op)
	renderChild(out, e.Lhs, parent, false)
	out.WriteByte(' ')
	out.WriteString(e.Op)
	out.WriteByte(' ')
	renderChild(out, e.Rhs, parent, true)
}

// Un is a unary prefix expression. The operand is wrapped when it is a
// binary (lower precedence — e.g. `!(a == b)`) or another unary, since a
// bare juxtaposition like `--x` or `&&x` would be re-tokenised by Go's
// lexer into `DEC x` / `&& x` and produce a parse error. Atoms (`!ok`,
// `-1`, `*p`, `&x`) are emitted without parens.
type Un struct {
	Op      string
	Operand Expr
}

func (e Un) Prec() int { return PrecUnary }

func (e Un) Render(out *strings.Builder) {
	out.WriteString(e.Op)
	if e.Operand.Prec() <= PrecUnary {
		out.WriteByte('(')
		e.Operand.Render(out)
		out.WriteByte(')')
		return
	}
	e.Operand.Render(out)
}

// IIFE renders a Go immediately-invoked function expression (a small inline
// closure used by nativegen to thread temporaries through builtin lowerings).
// The result format is multi-line — body statements sit one per line under a
// single tab — so the generated source reads cleanly when the IIFE shows up
// inside a larger statement. The caller (typically a generator's writeLine)
// is responsible for re-indenting non-first lines under the surrounding
// statement's depth; see nativegen.Generator.writeLine for that reflow.
//
// ReturnType is the Go type of the IIFE's value, or empty for `func() {...}`.
// Each Body string is one statement minus its trailing terminator.
type IIFE struct {
	ReturnType string
	Body       []string
}

// String renders the IIFE. Output ends with `}()` (no trailing newline).
func (i IIFE) String() string {
	var b strings.Builder
	b.Grow(32 + len(i.ReturnType) + 16*len(i.Body))
	b.WriteString("func()")
	if i.ReturnType != "" {
		b.WriteByte(' ')
		b.WriteString(i.ReturnType)
	}
	b.WriteString(" {\n")
	for _, stmt := range i.Body {
		b.WriteByte('\t')
		b.WriteString(stmt)
		b.WriteByte('\n')
	}
	b.WriteString("}()")
	return b.String()
}

// renderChild writes c into out, wrapping it in parens if precedence rules
// require. isRight selects right-associative-side conservatism so e.g.
// `a - (b - c)` retains its grouping where the writer wants it.
func renderChild(out *strings.Builder, c Expr, parent int, isRight bool) {
	cp := c.Prec()
	wrap := cp < parent || (isRight && cp == parent)
	if wrap {
		out.WriteByte('(')
		c.Render(out)
		out.WriteByte(')')
		return
	}
	c.Render(out)
}
