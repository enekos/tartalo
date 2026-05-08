// Package format pretty-prints tartalo source code in a canonical style.
//
// The formatter is AST-driven: it lexes and parses the input, then walks the
// AST and re-emits source in a fixed style (2-space indent, no semicolons,
// canonical spacing around operators). Comments are preserved by capturing
// them from the lexer and replaying them at the corresponding source positions
// during emission. Blank lines are preserved as a single blank between
// constructs.
//
// The formatter is intentionally not configurable. There is one tartalo style
// and the formatter is its sole authoritative implementation.
package format

import (
	"errors"
	"fmt"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/parser"
	"github.com/enekos/tartalo/internal/token"
)

// Source returns the canonically-formatted source for the given file. If the
// input contains lex or parse errors the original src is returned unchanged
// alongside a non-nil error so callers can choose to skip in-place rewrites.
func Source(filename, src string) (string, error) {
	lx := lexer.New(filename, src)
	toks, lerrs := lx.Tokenize()
	if len(lerrs) > 0 {
		return src, joinErrs(lerrs)
	}
	file, perrs := parser.New(toks).Parse(filename)
	if len(perrs) > 0 {
		return src, joinErrs(perrs)
	}
	p := newPrinter(lx.Comments())
	p.printFile(file)
	return p.out.String(), nil
}

func joinErrs(errs []error) error {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return errors.New(strings.Join(parts, "\n"))
}

type printer struct {
	out      strings.Builder
	indent   int
	comments []token.Comment
	cmtIdx   int

	// lastSrcLine tracks the highest original-source line we've emitted output
	// for. Used to:
	//   - emit "above" comments before nodes whose source line is greater
	//   - reproduce a single blank line where the source had a gap
	//   - decide whether a comment is trailing (same line) or above (earlier)
	lastSrcLine int

	// onNewLine is true when the cursor is at the start of a fresh line and an
	// indent has not yet been written for it.
	onNewLine bool
}

func newPrinter(comments []token.Comment) *printer {
	return &printer{
		comments:    comments,
		onNewLine:   true,
		lastSrcLine: 0,
	}
}

// --- low-level emit ---------------------------------------------------------

func (p *printer) writeIndent() {
	if !p.onNewLine {
		return
	}
	for i := 0; i < p.indent; i++ {
		p.out.WriteString("  ")
	}
	p.onNewLine = false
}

// write emits raw text. If it begins a fresh line, the current indentation is
// written first.
func (p *printer) write(s string) {
	if s == "" {
		return
	}
	p.writeIndent()
	p.out.WriteString(s)
}

// nl writes a newline and marks the cursor as needing indentation.
func (p *printer) nl() {
	p.out.WriteByte('\n')
	p.onNewLine = true
}

// blankLine emits a blank line — but never two in a row, and never as the very
// first byte of output.
func (p *printer) blankLine() {
	out := p.out.String()
	if out == "" {
		return
	}
	if strings.HasSuffix(out, "\n\n") {
		return
	}
	if !strings.HasSuffix(out, "\n") {
		p.nl()
	}
	p.out.WriteByte('\n')
	p.onNewLine = true
}

// --- comment emission -------------------------------------------------------

// flushBefore emits any pending comments whose source line is strictly less
// than the given line, each on its own line. After flushing, lastSrcLine is
// advanced through any blank-line gap that originally separated the last
// emitted thing from the first comment in the run, but no further (the caller
// will set lastSrcLine to its own first line).
func (p *printer) flushBefore(line int) {
	for p.cmtIdx < len(p.comments) && p.comments[p.cmtIdx].Pos.Line < line {
		c := p.comments[p.cmtIdx]
		// If the comment is preceded in source by a blank line, mirror it.
		if p.lastSrcLine > 0 && c.Pos.Line > p.lastSrcLine+1 {
			p.blankLine()
		}
		if !p.onNewLine {
			p.nl()
		}
		p.write(c.Text)
		p.nl()
		p.lastSrcLine = c.Pos.Line
		p.cmtIdx++
	}
}

// trailingOn emits a single trailing comment matching `line` if one is next in
// the queue. Trailing comments are separated from the preceding token by two
// spaces, matching gofmt convention.
func (p *printer) trailingOn(line int) {
	if p.cmtIdx >= len(p.comments) {
		return
	}
	c := p.comments[p.cmtIdx]
	if c.Pos.Line != line {
		return
	}
	p.write("  ")
	p.write(c.Text)
	p.lastSrcLine = c.Pos.Line
	p.cmtIdx++
}

// flushRemaining emits any comments still queued (used at end of file/block).
func (p *printer) flushRemaining(maxLine int) {
	for p.cmtIdx < len(p.comments) && p.comments[p.cmtIdx].Pos.Line <= maxLine {
		c := p.comments[p.cmtIdx]
		if p.lastSrcLine > 0 && c.Pos.Line > p.lastSrcLine+1 {
			p.blankLine()
		}
		if !p.onNewLine {
			p.nl()
		}
		p.write(c.Text)
		p.nl()
		p.lastSrcLine = c.Pos.Line
		p.cmtIdx++
	}
}

// advanceTo is called before emitting a node that begins at the given source
// line. It flushes any preceding comments and reproduces a single blank line
// when the source had a gap between the previous emitted thing and this node.
func (p *printer) advanceTo(line int) {
	p.flushBefore(line)
	if p.lastSrcLine > 0 && line > p.lastSrcLine+1 {
		p.blankLine()
	}
	p.lastSrcLine = line
}

// --- file/decl level --------------------------------------------------------

func (p *printer) printFile(f *ast.File) {
	for _, imp := range f.Imports {
		p.advanceTo(imp.Pos().Line)
		p.printImport(imp)
		p.trailingOn(imp.Pos().Line)
		p.nl()
	}
	// Always separate the import block from the first declaration by a single
	// blank line, regardless of source layout. This is the canonical style.
	if len(f.Imports) > 0 && len(f.Decls) > 0 {
		p.blankLine()
	}
	for i, d := range f.Decls {
		// Always emit one blank line between top-level declarations. Source
		// gaps (multiple blank lines) collapse to one.
		if i > 0 {
			p.blankLine()
		}
		p.advanceTo(d.Pos().Line)
		p.printDecl(d)
	}
	// trailing comments past the last decl
	p.flushRemaining(1 << 30)
	// ensure file ends with exactly one newline
	out := p.out.String()
	out = strings.TrimRight(out, "\n") + "\n"
	p.out.Reset()
	p.out.WriteString(out)
}

func (p *printer) printImport(imp *ast.ImportDecl) {
	p.write("import { ")
	for i, n := range imp.Names {
		if i > 0 {
			p.write(", ")
		}
		p.write(n.Name)
	}
	p.write(" } from \"")
	p.write(escString(imp.Path))
	p.write("\"")
}

func (p *printer) printDecl(d ast.Decl) {
	switch x := d.(type) {
	case *ast.FuncDecl:
		p.printFunc(x)
	case *ast.VarDecl:
		p.printVarDecl(x)
		p.trailingOn(x.Pos().Line)
		p.nl()
	case *ast.TypeDecl:
		p.printTypeDecl(x)
	case *ast.TestDecl:
		p.printTest(x)
	case *ast.EvalDecl:
		p.printEval(x)
	default:
		p.write(fmt.Sprintf("/* unknown decl %T */", d))
		p.nl()
	}
}

func (p *printer) printFunc(fd *ast.FuncDecl) {
	if fd.IsExported {
		p.write("export ")
	}
	switch fd.Kind {
	case ast.FuncKindTool:
		p.write("tool ")
	case ast.FuncKindAgent:
		p.write("agent ")
	default:
		p.write("func ")
	}
	p.write(fd.Name)
	if len(fd.TypeParams) > 0 {
		p.write("<")
		for i, tp := range fd.TypeParams {
			if i > 0 {
				p.write(", ")
			}
			p.write(tp.Name)
		}
		p.write(">")
	}
	p.write("(")
	for i, par := range fd.Params {
		if i > 0 {
			p.write(", ")
		}
		p.write(par.Name)
		p.write(": ")
		p.printType(par.TypeAnn)
	}
	p.write(")")
	if len(fd.Tools) > 0 {
		p.write(" uses (")
		for i, t := range fd.Tools {
			if i > 0 {
				p.write(", ")
			}
			p.write(t)
		}
		p.write(")")
	}
	p.write(": ")
	p.printType(fd.Result)
	for _, eff := range fd.Effects {
		p.write(" !")
		p.write(eff)
	}
	p.write(" ")
	// Tool/agent metadata calls (desc/budget) were pulled off the body during
	// parsing — re-synthesise them at the top of the printed body so format
	// is round-trippable.
	if fd.Kind != ast.FuncKindPlain && (fd.Description != "" || fd.Budget > 0) {
		p.write("{")
		p.nl()
		p.indent++
		if fd.Description != "" {
			p.writeIndent()
			p.write("desc(\"")
			p.write(escString(fd.Description))
			p.write("\")")
			p.nl()
		}
		if fd.Budget > 0 {
			p.writeIndent()
			p.write("budget(")
			p.write(itoa64(fd.Budget))
			p.write(")")
			p.nl()
		}
		// Now print the (already-stripped) body's statements with the same
		// indent, then close the brace ourselves.
		for _, s := range fd.Body.Stmts {
			p.writeIndent()
			p.printStmt(s)
		}
		p.indent--
		p.writeIndent()
		p.write("}")
	} else {
		p.printBlock(fd.Body)
	}
	p.trailingOn(p.lastSrcLine)
	p.nl()
}

func itoa64(n int64) string {
	return fmt.Sprintf("%d", n)
}

func (p *printer) printVarDecl(vd *ast.VarDecl) {
	if vd.IsExported {
		p.write("export ")
	}
	if vd.IsConst {
		p.write("const ")
	} else {
		p.write("let ")
	}
	p.write(vd.Name)
	if vd.TypeAnn != nil {
		p.write(": ")
		p.printType(vd.TypeAnn)
	}
	p.write(" = ")
	p.printExpr(vd.Value, precLowest)
}

func (p *printer) printTypeDecl(td *ast.TypeDecl) {
	if td.IsExported {
		p.write("export ")
	}
	p.write("type ")
	p.write(td.Name)
	p.write(" = ")
	p.printType(td.Spec)
	p.trailingOn(p.lastSrcLine)
	p.nl()
}

func (p *printer) printTest(td *ast.TestDecl) {
	p.write("test \"")
	p.write(escString(td.Name))
	p.write("\" ")
	p.printBlock(td.Body)
	p.trailingOn(p.lastSrcLine)
	p.nl()
}

func (p *printer) printEval(ed *ast.EvalDecl) {
	p.write("eval \"")
	p.write(escString(ed.Name))
	p.write("\" ")
	p.printBlock(ed.Body)
	p.trailingOn(p.lastSrcLine)
	p.nl()
}

// --- types ------------------------------------------------------------------

func (p *printer) printType(t ast.TypeExpr) {
	switch x := t.(type) {
	case *ast.TypeName:
		p.write(x.Name)
	case *ast.ArrayType:
		p.printType(x.Elem)
		p.write("[]")
	case *ast.OptionalType:
		p.printType(x.Elem)
		p.write("?")
	case *ast.MapType:
		p.write("map<")
		p.printType(x.Key)
		p.write(", ")
		p.printType(x.Value)
		p.write(">")
	case *ast.FuncType:
		p.write("func(")
		for i, pt := range x.Params {
			if i > 0 {
				p.write(", ")
			}
			p.printType(pt)
		}
		p.write("): ")
		p.printType(x.Result)
	case *ast.RecordType:
		p.printRecordType(x)
	case *ast.SumType:
		p.printSumType(x)
	default:
		p.write(fmt.Sprintf("/* unknown type %T */", t))
	}
}

func (p *printer) printSumType(t *ast.SumType) {
	for i, v := range t.Variants {
		if i > 0 {
			p.write(" | ")
		}
		p.write(v.Name)
		if v.HasBraces {
			p.write("{")
			for j, f := range v.Fields {
				if j > 0 {
					p.write(", ")
				}
				p.write(f.Name)
				p.write(": ")
				p.printType(f.TypeAnn)
			}
			p.write("}")
		}
	}
}

func (p *printer) printRecordType(rt *ast.RecordType) {
	if len(rt.Fields) == 0 {
		p.write("{}")
		return
	}
	p.write("{")
	p.nl()
	p.indent++
	prevLine := rt.LBrace.Line
	for _, f := range rt.Fields {
		// preserve blank lines between fields
		if f.NamePos.Line > prevLine+1 && p.lastSrcLine > 0 {
			p.blankLine()
		}
		p.advanceTo(f.NamePos.Line)
		p.write(f.Name)
		p.write(": ")
		p.printType(f.TypeAnn)
		p.write(",")
		p.trailingOn(f.NamePos.Line)
		p.nl()
		prevLine = f.NamePos.Line
	}
	p.indent--
	p.write("}")
	p.lastSrcLine = rt.RBrace.Line
}

// --- statements -------------------------------------------------------------

func (p *printer) printBlock(b *ast.Block) {
	p.write("{")
	// Inside the block, source-line tracking starts from `{` so the first
	// statement's gap calculation is measured from the brace line, not from
	// whatever the caller had emitted before.
	p.lastSrcLine = b.LBrace.Line
	if len(b.Stmts) == 0 {
		// Drain any trapped comments between the braces — `}` may be on its
		// own line, in which case a comment between them belongs inside.
		if b.RBrace.Line > b.LBrace.Line {
			p.indent++
			p.flushBefore(b.RBrace.Line)
			p.indent--
			if !p.onNewLine {
				p.nl()
			}
		}
		p.write("}")
		p.lastSrcLine = b.RBrace.Line
		return
	}
	p.nl()
	p.indent++
	for _, s := range b.Stmts {
		p.advanceTo(s.Pos().Line)
		p.printStmt(s)
	}
	// Comments between the last statement and the closing brace belong inside.
	p.flushBefore(b.RBrace.Line)
	p.indent--
	if !p.onNewLine {
		p.nl()
	}
	p.write("}")
	p.lastSrcLine = b.RBrace.Line
}

func (p *printer) printStmt(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.DeclStmt:
		p.printVarDecl(x.Decl)
		p.trailingOn(x.Decl.NamePos.Line)
		p.nl()
	case *ast.ExprStmt:
		p.printExpr(x.X, precLowest)
		p.trailingOn(x.X.Pos().Line)
		p.nl()
	case *ast.AssignStmt:
		p.write(x.Name)
		p.write(" = ")
		p.printExpr(x.Value, precLowest)
		p.trailingOn(x.NamePos.Line)
		p.nl()
	case *ast.FieldAssignStmt:
		p.printExpr(x.Target, precCall)
		p.write(".")
		p.write(x.Name)
		p.write(" = ")
		p.printExpr(x.Value, precLowest)
		p.trailingOn(x.NamePos.Line)
		p.nl()
	case *ast.ReturnStmt:
		p.write("return")
		if x.Value != nil {
			p.write(" ")
			p.printExpr(x.Value, precLowest)
		}
		p.trailingOn(x.KwPos.Line)
		p.nl()
	case *ast.IfStmt:
		p.printIf(x)
	case *ast.ForStmt:
		p.printFor(x)
	case *ast.MatchStmt:
		p.printMatch(x)
	case *ast.ParallelStmt:
		p.printParallel(x)
	case *ast.TaskStmt:
		p.printTask(x)
	case *ast.Block:
		p.printBlock(x)
		p.nl()
	case *ast.DeferStmt:
		p.write("defer ")
		p.printBlock(x.Body)
		p.trailingOn(p.lastSrcLine)
		p.nl()
	default:
		p.write(fmt.Sprintf("/* unknown stmt %T */", s))
		p.nl()
	}
}

func (p *printer) printIf(s *ast.IfStmt) {
	p.write("if ")
	p.printExpr(s.Cond, precLowest)
	p.write(" ")
	p.printBlock(s.Then)
	if s.Else == nil {
		p.trailingOn(p.lastSrcLine)
		p.nl()
		return
	}
	p.write(" else ")
	// `else if` is represented by a Block containing a single IfStmt.
	if len(s.Else.Stmts) == 1 {
		if inner, ok := s.Else.Stmts[0].(*ast.IfStmt); ok {
			p.printIf(inner)
			return
		}
	}
	p.printBlock(s.Else)
	p.trailingOn(p.lastSrcLine)
	p.nl()
}

func (p *printer) printFor(s *ast.ForStmt) {
	p.write("for ")
	p.write(s.Var)
	p.write(" in ")
	p.printExpr(s.Iter, precLowest)
	p.write(" ")
	p.printBlock(s.Body)
	p.trailingOn(p.lastSrcLine)
	p.nl()
}

func (p *printer) printParallel(s *ast.ParallelStmt) {
	p.write("parallel ")
	p.printBlock(s.Body)
	p.trailingOn(p.lastSrcLine)
	p.nl()
}

func (p *printer) printTask(s *ast.TaskStmt) {
	p.write("task ")
	p.printBlock(s.Body)
	p.trailingOn(p.lastSrcLine)
	p.nl()
}

func (p *printer) printMatch(s *ast.MatchStmt) {
	p.write("match ")
	p.printExpr(s.Subject, precLowest)
	p.write(" {")
	p.nl()
	p.indent++
	for _, c := range s.Cases {
		// Determine the line of the first pattern for comment alignment.
		var startLine int
		if len(c.Patterns) > 0 {
			startLine = c.Patterns[0].Pos().Line
		} else {
			startLine = c.ArrowPos.Line
		}
		p.advanceTo(startLine)
		for i, pat := range c.Patterns {
			if i > 0 {
				p.write(" | ")
			}
			p.printPattern(pat)
		}
		p.write(" => ")
		// If the body is a single non-block statement, render inline.
		if len(c.Body.Stmts) == 1 {
			if _, isBlock := c.Body.Stmts[0].(*ast.Block); !isBlock {
				p.printStmtInline(c.Body.Stmts[0])
				p.trailingOn(p.lastSrcLine)
				p.nl()
				continue
			}
		}
		p.printBlock(c.Body)
		p.trailingOn(p.lastSrcLine)
		p.nl()
	}
	p.indent--
	p.write("}")
	p.lastSrcLine = s.RBrace.Line
	p.trailingOn(p.lastSrcLine)
	p.nl()
}

// printStmtInline emits a statement without a trailing newline (used for
// single-line match arm bodies).
func (p *printer) printStmtInline(s ast.Stmt) {
	switch x := s.(type) {
	case *ast.ExprStmt:
		p.printExpr(x.X, precLowest)
	case *ast.ReturnStmt:
		p.write("return")
		if x.Value != nil {
			p.write(" ")
			p.printExpr(x.Value, precLowest)
		}
	case *ast.AssignStmt:
		p.write(x.Name)
		p.write(" = ")
		p.printExpr(x.Value, precLowest)
	case *ast.FieldAssignStmt:
		p.printExpr(x.Target, precCall)
		p.write(".")
		p.write(x.Name)
		p.write(" = ")
		p.printExpr(x.Value, precLowest)
	case *ast.DeclStmt:
		p.printVarDecl(x.Decl)
	default:
		// Fallback: re-route through the regular path. The trailing newline
		// here is harmless because match-arm formatting tolerates it.
		p.printStmt(s)
	}
}

func (p *printer) printPattern(pat ast.Pattern) {
	switch x := pat.(type) {
	case *ast.LiteralPattern:
		p.printExpr(x.Lit, precLowest)
	case *ast.WildcardPattern:
		p.write("_")
	case *ast.VariantPattern:
		p.write(x.Name)
		if x.HasBraces {
			p.write("{")
			for i, b := range x.Bindings {
				if i > 0 {
					p.write(", ")
				}
				p.write(b.Name)
			}
			p.write("}")
		}
	default:
		p.write(fmt.Sprintf("/* unknown pattern %T */", pat))
	}
}

// --- expressions ------------------------------------------------------------

const (
	precLowest   = 0
	precCoalesce = 1
	precOr       = 2
	precAnd      = 3
	precEq       = 4
	precCmp      = 5
	precRange    = 6
	precAdd      = 7
	precMul      = 8
	precUnary    = 9
	precCall     = 10
	precPrimary  = 11
)

func opPrec(k token.Kind) int {
	switch k {
	case token.OrOr:
		return precOr
	case token.AndAnd:
		return precAnd
	case token.Eq, token.Neq:
		return precEq
	case token.Lt, token.Lte, token.Gt, token.Gte:
		return precCmp
	case token.Plus, token.Minus:
		return precAdd
	case token.Star, token.Slash, token.Percent:
		return precMul
	}
	return precLowest
}

func opString(k token.Kind) string {
	switch k {
	case token.Plus:
		return "+"
	case token.Minus:
		return "-"
	case token.Star:
		return "*"
	case token.Slash:
		return "/"
	case token.Percent:
		return "%"
	case token.Eq:
		return "=="
	case token.Neq:
		return "!="
	case token.Lt:
		return "<"
	case token.Lte:
		return "<="
	case token.Gt:
		return ">"
	case token.Gte:
		return ">="
	case token.AndAnd:
		return "&&"
	case token.OrOr:
		return "||"
	case token.Bang:
		return "!"
	}
	return k.String()
}

// exprPrec returns the binding strength of e for the parens decision when e is
// a child of some parent expression. Higher = binds tighter.
func exprPrec(e ast.Expr) int {
	switch x := e.(type) {
	case *ast.BinaryExpr:
		return opPrec(x.Op)
	case *ast.RangeExpr:
		return precRange
	case *ast.CoalesceExpr:
		return precCoalesce
	case *ast.UnaryExpr:
		return precUnary
	case *ast.UnwrapExpr, *ast.CallExpr, *ast.IndexExpr, *ast.FieldExpr, *ast.TryExpr:
		return precCall
	default:
		return precPrimary
	}
}

// printExpr emits e, parenthesising when its precedence is below `min`.
func (p *printer) printExpr(e ast.Expr, min int) {
	if exprPrec(e) < min {
		p.write("(")
		p.printExprNoParens(e)
		p.write(")")
		return
	}
	p.printExprNoParens(e)
}

func (p *printer) printExprNoParens(e ast.Expr) {
	switch x := e.(type) {
	case *ast.Ident:
		p.write(x.Name)
	case *ast.IntLit:
		p.write(fmt.Sprintf("%d", x.Value))
	case *ast.FloatLit:
		p.write(x.Text)
	case *ast.BoolLit:
		if x.Value {
			p.write("true")
		} else {
			p.write("false")
		}
	case *ast.NullLit:
		p.write("null")
	case *ast.StringLit:
		p.printString(x)
	case *ast.CmdLit:
		p.printCmd(x)
	case *ast.BinaryExpr:
		prec := opPrec(x.Op)
		// LHS: paren when child < prec; same prec is fine due to left-assoc.
		p.printExpr(x.Lhs, prec)
		p.write(" ")
		p.write(opString(x.Op))
		p.write(" ")
		// RHS: paren when child <= prec to preserve left-assoc grouping.
		p.printExpr(x.Rhs, prec+1)
	case *ast.RangeExpr:
		p.printExpr(x.Start, precRange+1)
		p.write("..")
		p.printExpr(x.End, precRange+1)
	case *ast.CoalesceExpr:
		// `??` is right-associative in spirit (chained nulls flow rightward).
		p.printExpr(x.Lhs, precCoalesce+1)
		p.write(" ?? ")
		p.printExpr(x.Rhs, precCoalesce)
	case *ast.UnaryExpr:
		p.write(opString(x.Op))
		p.printExpr(x.Operand, precUnary)
	case *ast.CallExpr:
		p.printExpr(x.Callee, precCall)
		p.write("(")
		for i, a := range x.Args {
			if i > 0 {
				p.write(", ")
			}
			p.printExpr(a, precLowest)
		}
		p.write(")")
	case *ast.IndexExpr:
		p.printExpr(x.Target, precCall)
		p.write("[")
		p.printExpr(x.Index, precLowest)
		p.write("]")
	case *ast.FieldExpr:
		p.printExpr(x.Target, precCall)
		p.write(".")
		p.write(x.Name)
	case *ast.UnwrapExpr:
		p.printExpr(x.Operand, precCall)
		p.write("!")
	case *ast.TryExpr:
		p.printExpr(x.Operand, precCall)
		p.write("?")
	case *ast.CastExpr:
		p.printExpr(x.Operand, precCall)
		p.write(" as ")
		p.printType(x.TypeAnn)
	case *ast.FuncLit:
		p.write("func(")
		for i, param := range x.Params {
			if i > 0 {
				p.write(", ")
			}
			p.write(param.Name)
			p.write(": ")
			p.printType(param.TypeAnn)
		}
		p.write("): ")
		p.printType(x.Result)
		p.write(" ")
		p.printBlock(x.Body)
	case *ast.ArrayLit:
		p.printArrayLit(x)
	case *ast.RecordLit:
		p.printRecordLit(x)
	default:
		p.write(fmt.Sprintf("/* unknown expr %T */", e))
	}
}

func (p *printer) printString(s *ast.StringLit) {
	p.write("\"")
	for _, part := range s.Parts {
		switch x := part.(type) {
		case *ast.StringChunk:
			p.write(escString(x.Value))
		default:
			p.write("${")
			p.printExpr(part, precLowest)
			p.write("}")
		}
	}
	p.write("\"")
}

func (p *printer) printCmd(c *ast.CmdLit) {
	p.write("`")
	for _, part := range c.Parts {
		switch x := part.(type) {
		case *ast.StringChunk:
			p.write(escCmd(x.Value))
		default:
			p.write("${")
			p.printExpr(part, precLowest)
			p.write("}")
		}
	}
	p.write("`")
}

// printArrayLit chooses inline or multiline based on the source layout: if the
// elements span multiple original lines we emit one element per line with a
// trailing comma; otherwise inline with `, ` separators.
func (p *printer) printArrayLit(a *ast.ArrayLit) {
	if len(a.Elems) == 0 {
		p.write("[]")
		return
	}
	if !spansMultipleLines(a.LBracket.Line, exprLines(a.Elems)) {
		p.write("[")
		for i, e := range a.Elems {
			if i > 0 {
				p.write(", ")
			}
			p.printExpr(e, precLowest)
		}
		p.write("]")
		return
	}
	p.write("[")
	p.nl()
	p.indent++
	prevLine := a.LBracket.Line
	for _, e := range a.Elems {
		if e.Pos().Line > prevLine+1 && p.lastSrcLine > 0 {
			p.blankLine()
		}
		p.advanceTo(e.Pos().Line)
		p.printExpr(e, precLowest)
		p.write(",")
		p.trailingOn(e.Pos().Line)
		p.nl()
		prevLine = e.Pos().Line
	}
	p.indent--
	p.write("]")
}

func (p *printer) printRecordLit(r *ast.RecordLit) {
	if len(r.Fields) == 0 && r.Spread == nil {
		p.write(r.TypeName)
		p.write("{}")
		return
	}
	fieldLines := make([]int, 0, len(r.Fields)+1)
	if r.Spread != nil {
		fieldLines = append(fieldLines, r.Spread.Pos().Line)
	}
	for _, f := range r.Fields {
		fieldLines = append(fieldLines, f.NamePos.Line)
	}
	if !spansMultipleLines(r.LBrace.Line, fieldLines) {
		p.write(r.TypeName)
		p.write("{")
		first := true
		if r.Spread != nil {
			p.write("...")
			p.printExpr(r.Spread, precLowest)
			first = false
		}
		for _, f := range r.Fields {
			if !first {
				p.write(", ")
			}
			first = false
			p.write(f.Name)
			p.write(": ")
			p.printExpr(f.Value, precLowest)
		}
		p.write("}")
		return
	}
	p.write(r.TypeName)
	p.write("{")
	p.nl()
	p.indent++
	prevLine := r.LBrace.Line
	if r.Spread != nil {
		p.advanceTo(r.Spread.Pos().Line)
		p.write("...")
		p.printExpr(r.Spread, precLowest)
		p.write(",")
		p.trailingOn(r.Spread.Pos().Line)
		p.nl()
		prevLine = r.Spread.Pos().Line
	}
	for _, f := range r.Fields {
		if f.NamePos.Line > prevLine+1 && p.lastSrcLine > 0 {
			p.blankLine()
		}
		p.advanceTo(f.NamePos.Line)
		p.write(f.Name)
		p.write(": ")
		p.printExpr(f.Value, precLowest)
		p.write(",")
		p.trailingOn(f.NamePos.Line)
		p.nl()
		prevLine = f.NamePos.Line
	}
	p.indent--
	p.write("}")
	p.lastSrcLine = r.RBrace.Line
}

// --- helpers ----------------------------------------------------------------

func exprLines(es []ast.Expr) []int {
	out := make([]int, len(es))
	for i, e := range es {
		out[i] = e.Pos().Line
	}
	return out
}

// spansMultipleLines returns true iff any of `lines` is on a line different
// from `openLine`. Used to detect user-formatted multiline composite literals.
func spansMultipleLines(openLine int, lines []int) bool {
	for _, l := range lines {
		if l != openLine {
			return true
		}
	}
	return false
}

func escString(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		case '\r':
			b.WriteString(`\r`)
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '$':
			b.WriteString(`\$`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func escCmd(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\':
			b.WriteString(`\\`)
		case '`':
			b.WriteString("\\`")
		case '$':
			b.WriteString(`\$`)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
