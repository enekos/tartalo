package nativegen

import (
	"strconv"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/token"
	"github.com/enekos/tartalo/internal/types"
)

// compileExpr returns a Go expression text for the Tartalo expression `e`.
// Unlike the sh backend (which builds prologues plus a value reference), Go
// has real expressions, so most compileExpr cases are one-liners. The
// exception is iteration constructs and side-effecting statements, which
// lower at the statement level.
func (g *Generator) compileExpr(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.IntLit:
		return int64Lit(e.Value)
	case *ast.FloatLit:
		return e.Text
	case *ast.BoolLit:
		if e.Value {
			return "true"
		}
		return "false"
	case *ast.NullLit:
		return "nil"
	case *ast.Ident:
		return g.compileIdent(e)
	case *ast.StringLit:
		return g.compileStringLit(e)
	case *ast.CmdLit:
		return g.compileCmdLit(e)
	case *ast.UnaryExpr:
		return g.compileUnary(e)
	case *ast.BinaryExpr:
		return g.compileBinary(e)
	case *ast.CallExpr:
		return g.compileCall(e)
	case *ast.ArrayLit:
		return g.compileArrayLit(e)
	case *ast.IndexExpr:
		return g.compileIndex(e)
	case *ast.RecordLit:
		return g.compileRecordLit(e)
	case *ast.FieldExpr:
		return g.compileField(e)
	case *ast.CoalesceExpr:
		return g.compileCoalesce(e)
	case *ast.UnwrapExpr:
		return g.compileUnwrap(e)
	case *ast.TryExpr:
		return g.compileTry(e)
	}
	return "/* unsupported expr */ nil"
}

// compileTry renders `expr?` as a Go IIFE: when the operand's Tag is "Err",
// the IIFE writes through to the enclosing function via a panic-style exit
// — except Go has no goto-out-of-IIFE, so we emit a function-local helper
// that returns (T, bool) and let the caller decide. Simpler: the operand is
// evaluated to a temp; if Err, the enclosing function returns with the Err
// re-tagged; otherwise the value is the Ok payload. That requires this be
// rewritten as a statement, but `?` may appear in any expression context.
//
// Workaround: we generate a closure that takes ownership of the unwrap and
// uses Go's named-return-with-defer to short-circuit. The cleanest version
// uses panic + recover, but that adds runtime cost. Instead: we lift the
// operand into a local at statement level via a helper, then mark the
// enclosing function for early-return — not portable for arbitrary
// expression positions in Go without further plumbing. For v0 we restrict
// `?` to simple statement contexts where we can desugar at the statement
// emitter; here we emit a runtime helper that panics on Err and let the
// surrounding native test harness translate that into a typed return.
//
// In practice all sh-side compatible programs use `?` in a let/return
// context; the native codegen below relies on a runtime helper that
// re-throws the Err via a sentinel error value the function's caller
// recovers in main(). Sufficient for our test surface; full parity with
// the sh backend's flow control would need a deeper rewrite.
func (g *Generator) compileTry(e *ast.TryExpr) string {
	g.usesRuntimeTry = true
	sum, _ := g.info.Types[e.Operand].(*types.Sum)
	if sum == nil {
		return g.compileExpr(e.Operand)
	}
	retSum, _ := g.currentReturnType.(*types.Sum)
	if retSum == nil {
		return g.compileExpr(e.Operand)
	}
	// Inline body: evaluate operand to a temp, panic with the Err carrier
	// when Tag=="Err"; otherwise yield the Ok payload. The enclosing
	// function's deferred recover (emitted by emitFunc when usesRuntimeTry
	// fires) translates the panic into the function's typed Err return.
	op := g.compileExpr(e.Operand)
	return "func() " + g.goType(sum.Variants[0].Fields[0].Type) +
		" { _v := " + op +
		"; if _v.Tag == \"Err\" { panic(_tt_tryErr{err: _v.F_Err_error}) }" +
		"; return _v.F_Ok_value }()"
}

func (g *Generator) compileIdent(e *ast.Ident) string {
	if sym := g.info.Uses[e]; sym != nil {
		// Unit-variant constructor: synthesise a fresh value of the parent
		// sum type with the matching tag set; payload slots stay zero.
		if sym.IsVariant {
			if sum, ok := sym.Type.(*types.Sum); ok {
				return goTypeName(sum.Name) + "{Tag: " + strconv.Quote(sym.Name) + "}"
			}
		}
		// Top-level (function or global): use the module-mangled form.
		if sym.Module != nil {
			return "tt_" + checker.MangledName(sym.Module, sym.Name)
		}
	}
	// Locals and params keep the bare name with a `tt_` prefix.
	var buf [32]byte
	n := copy(buf[:], "tt_")
	n += copy(buf[n:], e.Name)
	return string(buf[:n])
}

func (g *Generator) compileStringLit(e *ast.StringLit) string {
	if len(e.Parts) == 0 {
		return `""`
	}
	// Single literal chunk → emit one quoted string.
	if len(e.Parts) == 1 {
		if c, ok := e.Parts[0].(*ast.StringChunk); ok {
			return strconv.Quote(c.Value)
		}
	}
	// Mixed: stitch with `+`. Each interpolated expression is converted to
	// string via `_ttStr<T>` if needed; for raw strings we drop them in.
	var b strings.Builder
	for i, p := range e.Parts {
		if i > 0 {
			b.WriteString(" + ")
		}
		switch p := p.(type) {
		case *ast.StringChunk:
			b.WriteString(strconv.Quote(p.Value))
		default:
			t := g.info.Types[p]
			b.WriteString(g.toString(g.compileExpr(p), t))
		}
	}
	return b.String()
}

// toString returns a Go expression of type string for a value `expr` of
// Tartalo type `t`. For string types it's the identity; for number/float/
// bool we go through strconv with the same rules `str()` uses.
func (g *Generator) toString(expr string, t types.Type) string {
	switch t {
	case types.String:
		return expr
	case types.Number:
		g.addImport("strconv")
		return "strconv.FormatInt(" + expr + ", 10)"
	case types.Float:
		g.addImport("strconv")
		return "strconv.FormatFloat(" + expr + ", 'g', -1, 64)"
	case types.Bool:
		// Match the sh backend: bools stringify as "1"/"0".
		return "func() string { if " + expr + " { return \"1\" } ; return \"0\" }()"
	}
	if _, ok := t.(*types.Optional); ok {
		// The checker forbids implicit string-of-optional in arith contexts,
		// but interpolation accepts optional-of-string. Best effort: deref or
		// fall back to the empty string.
		if opt, _ := t.(*types.Optional); opt != nil && opt.Elem == types.String {
			return "func() string { if " + expr + " == nil { return \"\" } ; return *(" + expr + ") }()"
		}
	}
	return expr
}

func (g *Generator) compileCmdLit(e *ast.CmdLit) string {
	// Build a string for the command, then run it through /bin/sh -c (or
	// cmd /c on Windows). The runtime helper trims a trailing newline to
	// match the sh backend.
	g.usesRuntimeShellOut = true
	g.addImport("os/exec")
	g.addImport("strings")
	g.addImport("runtime")
	if len(e.Parts) == 0 {
		return `_tt_shellOut("")`
	}
	if len(e.Parts) == 1 {
		if c, ok := e.Parts[0].(*ast.StringChunk); ok {
			return "_tt_shellOut(" + strconv.Quote(c.Value) + ")"
		}
	}
	var b strings.Builder
	b.WriteString("_tt_shellOut(")
	for i, p := range e.Parts {
		if i > 0 {
			b.WriteString(" + ")
		}
		switch p := p.(type) {
		case *ast.StringChunk:
			b.WriteString(strconv.Quote(p.Value))
		default:
			t := g.info.Types[p]
			b.WriteString(g.toString(g.compileExpr(p), t))
		}
	}
	b.WriteString(")")
	return b.String()
}

func (g *Generator) compileUnary(e *ast.UnaryExpr) string {
	op := ""
	switch e.Op {
	case token.Minus:
		op = "-"
	case token.Bang:
		op = "!"
	}
	return "(" + op + g.compileExpr(e.Operand) + ")"
}

func (g *Generator) compileBinary(e *ast.BinaryExpr) string {
	lhs := g.compileExpr(e.Lhs)
	rhs := g.compileExpr(e.Rhs)
	lt := g.info.Types[e.Lhs]
	rt := g.info.Types[e.Rhs]
	switch e.Op {
	case token.Plus:
		// String concat in Tartalo only; numeric add otherwise.
		return "(" + lhs + " + " + rhs + ")"
	case token.Minus:
		return "(" + lhs + " - " + rhs + ")"
	case token.Star:
		return "(" + lhs + " * " + rhs + ")"
	case token.Slash:
		return "(" + lhs + " / " + rhs + ")"
	case token.Percent:
		return "(" + lhs + " % " + rhs + ")"
	case token.Eq:
		return g.compileEq(lhs, rhs, lt, rt, false)
	case token.Neq:
		return g.compileEq(lhs, rhs, lt, rt, true)
	case token.Lt:
		return "(" + lhs + " < " + rhs + ")"
	case token.Lte:
		return "(" + lhs + " <= " + rhs + ")"
	case token.Gt:
		return "(" + lhs + " > " + rhs + ")"
	case token.Gte:
		return "(" + lhs + " >= " + rhs + ")"
	case token.AndAnd:
		return "(" + lhs + " && " + rhs + ")"
	case token.OrOr:
		return "(" + lhs + " || " + rhs + ")"
	}
	return "/* unsupported binary op */ false"
}

// compileEq handles `==` and `!=`. Most cases are direct, but optional vs.
// null needs to compare against nil rather than dereference, and number-vs-
// float widens with float64(...).
func (g *Generator) compileEq(lhs, rhs string, lt, rt types.Type, neg bool) string {
	op := "=="
	if neg {
		op = "!="
	}
	// One side is the null literal: comparison is against nil.
	if lt == types.Null {
		return "(" + rhs + " " + op + " nil)"
	}
	if rt == types.Null {
		return "(" + lhs + " " + op + " nil)"
	}
	// Optional vs T (auto-wrap): unwrap with `*x` and equate to the bare value.
	// The checker only allows this when both sides have compatible underlying
	// types, but we still need to be careful about nil dereference at runtime.
	if _, ok := lt.(*types.Optional); ok {
		if _, ok := rt.(*types.Optional); !ok {
			return "(" + lhs + " != nil && *(" + lhs + ") " + op + " " + rhs + ")"
		}
	}
	if _, ok := rt.(*types.Optional); ok {
		if _, ok := lt.(*types.Optional); !ok {
			return "(" + rhs + " != nil && *(" + rhs + ") " + op + " " + lhs + ")"
		}
	}
	// Number / float widening.
	if lt == types.Number && rt == types.Float {
		return "(float64(" + lhs + ") " + op + " " + rhs + ")"
	}
	if lt == types.Float && rt == types.Number {
		return "(" + lhs + " " + op + " float64(" + rhs + "))"
	}
	return "(" + lhs + " " + op + " " + rhs + ")"
}

func (g *Generator) compileArrayLit(e *ast.ArrayLit) string {
	// We need the element type to emit a typed slice. Prefer the checker's
	// view; fall back to inferring from the first element.
	t := g.info.Types[e]
	var elemTy types.Type
	if arr, ok := t.(*types.Array); ok {
		elemTy = arr.Elem
	}
	if elemTy == nil && len(e.Elems) > 0 {
		elemTy = g.info.Types[e.Elems[0]]
	}
	if elemTy == nil {
		// The checker would have rejected this in user code; emit something
		// that at least type-checks under Go.
		elemTy = types.String
	}
	var b strings.Builder
	b.WriteString("[]" + g.goType(elemTy) + "{")
	for i, el := range e.Elems {
		if i > 0 {
			b.WriteString(", ")
		}
		expr := g.compileExpr(el)
		if g.info.Types[el] != elemTy {
			expr = g.coerce(expr, g.info.Types[el], elemTy)
		}
		b.WriteString(expr)
	}
	b.WriteString("}")
	return b.String()
}

func (g *Generator) compileIndex(e *ast.IndexExpr) string {
	// Tartalo arrays index by int64; Go slices want `int`, so we cast.
	return "(" + g.compileExpr(e.Target) + ")[int(" + g.compileExpr(e.Index) + ")]"
}

func (g *Generator) compileRecordLit(e *ast.RecordLit) string {
	// Variant literal: the literal type the checker assigned is the parent
	// sum, not a record. Render as a struct literal with the tag set and
	// only the active variant's payload slots populated.
	if sum, ok := g.info.Types[e].(*types.Sum); ok {
		return g.compileVariantLit(e, sum)
	}
	var b strings.Builder
	b.WriteString(goTypeName(e.TypeName))
	b.WriteString("{")
	for i, f := range e.Fields {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(goFieldName(f.Name))
		b.WriteString(": ")
		// Auto-wrap to optional / widen to float when needed.
		var fieldTy types.Type
		if rec, ok := g.info.Types[e].(*types.Record); ok {
			if fld := rec.Lookup(f.Name); fld != nil {
				fieldTy = fld.Type
			}
		}
		b.WriteString(g.coerce(g.compileExpr(f.Value), g.info.Types[f.Value], fieldTy))
	}
	b.WriteString("}")
	return b.String()
}

func (g *Generator) compileVariantLit(e *ast.RecordLit, sum *types.Sum) string {
	v := sum.LookupVariant(e.TypeName)
	if v == nil {
		return goTypeName(sum.Name) + "{Tag: " + strconv.Quote(e.TypeName) + "}"
	}
	var b strings.Builder
	b.WriteString(goTypeName(sum.Name))
	b.WriteString("{Tag: ")
	b.WriteString(strconv.Quote(v.Name))
	for _, f := range v.Fields {
		var init *ast.FieldInit
		for i := range e.Fields {
			if e.Fields[i].Name == f.Name {
				init = &e.Fields[i]
				break
			}
		}
		if init == nil {
			continue
		}
		b.WriteString(", F_")
		b.WriteString(v.Name)
		b.WriteString("_")
		b.WriteString(f.Name)
		b.WriteString(": ")
		b.WriteString(g.coerce(g.compileExpr(init.Value), g.info.Types[init.Value], f.Type))
	}
	b.WriteString("}")
	return b.String()
}

func (g *Generator) compileField(e *ast.FieldExpr) string {
	return "(" + g.compileExpr(e.Target) + ")." + goFieldName(e.Name)
}

func (g *Generator) compileCoalesce(e *ast.CoalesceExpr) string {
	g.usesRuntimeCoalesce = true
	// The RHS type is the unwrapped element type T; the LHS is T?. We use
	// the generic helper so the compiler infers T.
	return "_tt_coalesce(" + g.compileExpr(e.Lhs) + ", " + g.compileExpr(e.Rhs) + ")"
}

func (g *Generator) compileUnwrap(e *ast.UnwrapExpr) string {
	g.usesRuntimeUnwrap = true
	return "_tt_unwrap(" + g.compileExpr(e.Operand) + ")"
}

// coerce wraps `expr` (whose Tartalo type is `from`) so it satisfies the
// Tartalo target type `to`. Handles auto-wrap to optional, number→float
// widening, and a no-op for matching types.
func (g *Generator) coerce(expr string, from, to types.Type) string {
	if from == to {
		return expr
	}
	if to == nil || from == nil {
		return expr
	}
	if types.Equal(from, to) {
		return expr
	}
	// Null → optional: just nil. The checker has already verified this is an
	// optional context; emit `(*T)(nil)` when we can't infer the type.
	if from == types.Null {
		if opt, ok := to.(*types.Optional); ok {
			return "(*" + g.goType(opt.Elem) + ")(nil)"
		}
		return "nil"
	}
	// Auto-wrap T → T? via the runtime _tt_ptr helper. Required even for
	// pointer-typed inputs because Tartalo's optional is always `*T`, not
	// `**T`.
	if opt, ok := to.(*types.Optional); ok {
		if types.Equal(from, opt.Elem) {
			g.usesRuntimePtr = true
			return "_tt_ptr(" + expr + ")"
		}
		// number → float? widening: convert then wrap.
		if from == types.Number && opt.Elem == types.Float {
			g.usesRuntimePtr = true
			return "_tt_ptr(float64(" + expr + "))"
		}
	}
	// number → float widening.
	if from == types.Number && to == types.Float {
		return "float64(" + expr + ")"
	}
	return expr
}

// smallInt64Lit caches the emitted form of the most common integer
// literals so we avoid strconv.FormatInt and its allocation overhead.
var smallInt64Lit = [20]string{
	"int64(0)", "int64(1)", "int64(2)", "int64(3)", "int64(4)",
	"int64(5)", "int64(6)", "int64(7)", "int64(8)", "int64(9)",
	"int64(10)", "int64(11)", "int64(12)", "int64(13)", "int64(14)",
	"int64(15)", "int64(16)", "int64(17)", "int64(18)", "int64(19)",
}

func int64Lit(v int64) string {
	if v >= 0 && v < int64(len(smallInt64Lit)) {
		return smallInt64Lit[v]
	}
	return "int64(" + strconv.FormatInt(v, 10) + ")"
}
