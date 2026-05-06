package nativegen

import (
	"strconv"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/types"
)

// emitFunc writes a single Tartalo function as a Go function. The signature
// is reconstructed from the checker's symbol so we know parameter and result
// types without re-walking the type annotations.
func (g *Generator) emitFunc(fd *ast.FuncDecl) {
	sym := g.info.Decls[checker.MangledName(g.currentModule, fd.Name)]
	var ft *types.Func
	if sym != nil {
		ft, _ = sym.Type.(*types.Func)
	}
	prevRet := g.currentReturnType
	if ft != nil {
		g.currentReturnType = ft.Result
	}
	g.out.WriteString("func ")
	g.out.WriteString(g.goFuncName(g.currentModule, fd.Name))
	g.out.WriteString("(")
	switch len(fd.Params) {
	case 0:
		// nothing
	case 1:
		p := fd.Params[0]
		var pt types.Type
		if ft != nil && len(ft.Params) > 0 {
			pt = ft.Params[0]
		} else {
			pt = g.typeFromAnn(p.TypeAnn)
		}
		g.out.WriteString(g.goLocalName(p.Name))
		g.out.WriteString(" ")
		g.out.WriteString(g.goType(pt))
	default:
		for i, p := range fd.Params {
			if i > 0 {
				g.out.WriteString(", ")
			}
			g.out.WriteString(g.goLocalName(p.Name))
			g.out.WriteString(" ")
			var pt types.Type
			if ft != nil && i < len(ft.Params) {
				pt = ft.Params[i]
			} else {
				pt = g.typeFromAnn(p.TypeAnn)
			}
			g.out.WriteString(g.goType(pt))
		}
	}
	g.out.WriteString(")")
	// If this function returns a Result-shaped sum AND the body uses `?`,
	// emit a named return so the deferred recover trampoline below can
	// rewrite it on panic. Otherwise stick with a positional return for
	// the cleanest Go output.
	useNamedRet := false
	if ft != nil && ft.Result != types.Void {
		if retSum, ok := ft.Result.(*types.Sum); ok && hasTryIn(fd.Body) {
			if errV := retSum.LookupVariant("Err"); errV != nil && len(errV.Fields) == 1 {
				useNamedRet = true
				_ = errV
			}
		}
		if useNamedRet {
			g.out.WriteString(" (_tt_ret " + g.goType(ft.Result) + ")")
		} else {
			g.out.WriteString(" " + g.goType(ft.Result))
		}
	}
	g.out.WriteString(" {\n")
	g.indent++
	prevAgent := g.currentAgent
	if fd.Kind == ast.FuncKindAgent {
		g.currentAgent = fd
	} else {
		g.currentAgent = nil
	}
	defer func() { g.currentAgent = prevAgent }()
	if fd.Kind == ast.FuncKindAgent && fd.Budget > 0 {
		g.writeLine("_tt_budget := int64(" + itoa64(fd.Budget) + ")")
		g.writeLine("_ = _tt_budget")
	}
	if useNamedRet {
		g.usesRuntimeTry = true
		retSum := ft.Result.(*types.Sum)
		g.writeLine("defer func() {")
		g.indent++
		g.writeLine("if r := recover(); r != nil {")
		g.indent++
		g.writeLine("if te, ok := r.(_tt_tryErr); ok {")
		g.indent++
		g.writeLine("_tt_ret = " + goTypeName(retSum.Name) +
			"{Tag: \"Err\", F_Err_error: te.err}")
		g.writeLine("return")
		g.indent--
		g.writeLine("}")
		g.writeLine("panic(r)")
		g.indent--
		g.writeIndent()
		g.out.WriteString("}\n")
		g.indent--
		g.writeIndent()
		g.out.WriteString("}()\n")
	}
	for _, s := range fd.Body.Stmts {
		g.emitStmt(s)
	}
	// Functions with a non-void return that don't end in `return` need a
	// safe trailing zero-value to satisfy Go's flow analysis. Tartalo's
	// checker doesn't enforce this on every path, so we belt-and-braces it.
	if ft != nil && ft.Result != types.Void && !endsWithReturn(fd.Body.Stmts) {
		g.writeLine("var _tt_zero " + g.goType(ft.Result))
		g.writeLine("return _tt_zero")
	}
	g.indent--
	g.writeIndent()
	g.out.WriteString("}\n")
	g.currentReturnType = prevRet
}

// hasTryIn reports whether the block tree transitively contains a TryExpr.
// Used to decide whether to inject the recover trampoline that turns the
// runtime panic into a typed Err return.
func hasTryIn(b *ast.Block) bool {
	if b == nil {
		return false
	}
	for _, s := range b.Stmts {
		if hasTryInStmt(s) {
			return true
		}
	}
	return false
}

func hasTryInStmt(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.ReturnStmt:
		return s.Value != nil && hasTryInExpr(s.Value)
	case *ast.DeclStmt:
		return s.Decl.Value != nil && hasTryInExpr(s.Decl.Value)
	case *ast.AssignStmt:
		return hasTryInExpr(s.Value)
	case *ast.FieldAssignStmt:
		return hasTryInExpr(s.Target) || hasTryInExpr(s.Value)
	case *ast.ExprStmt:
		return hasTryInExpr(s.X)
	case *ast.IfStmt:
		return hasTryInExpr(s.Cond) || hasTryIn(s.Then) || hasTryIn(s.Else)
	case *ast.ForStmt:
		return hasTryInExpr(s.Iter) || hasTryIn(s.Body)
	case *ast.MatchStmt:
		if hasTryInExpr(s.Subject) {
			return true
		}
		for _, c := range s.Cases {
			if hasTryIn(c.Body) {
				return true
			}
		}
	case *ast.DeferStmt:
		return hasTryIn(s.Body)
	case *ast.Block:
		return hasTryIn(s)
	}
	return false
}

func hasTryInExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.TryExpr:
		return true
	case *ast.UnaryExpr:
		return hasTryInExpr(e.Operand)
	case *ast.BinaryExpr:
		return hasTryInExpr(e.Lhs) || hasTryInExpr(e.Rhs)
	case *ast.CallExpr:
		if hasTryInExpr(e.Callee) {
			return true
		}
		for _, a := range e.Args {
			if hasTryInExpr(a) {
				return true
			}
		}
	case *ast.IndexExpr:
		return hasTryInExpr(e.Target) || hasTryInExpr(e.Index)
	case *ast.FieldExpr:
		return hasTryInExpr(e.Target)
	case *ast.RangeExpr:
		return hasTryInExpr(e.Start) || hasTryInExpr(e.End)
	case *ast.ArrayLit:
		for _, el := range e.Elems {
			if hasTryInExpr(el) {
				return true
			}
		}
	case *ast.RecordLit:
		for _, f := range e.Fields {
			if hasTryInExpr(f.Value) {
				return true
			}
		}
	case *ast.CoalesceExpr:
		return hasTryInExpr(e.Lhs) || hasTryInExpr(e.Rhs)
	case *ast.UnwrapExpr:
		return hasTryInExpr(e.Operand)
	case *ast.StringLit:
		for _, p := range e.Parts {
			if _, ok := p.(*ast.StringChunk); !ok {
				if hasTryInExpr(p) {
					return true
				}
			}
		}
	case *ast.CmdLit:
		for _, p := range e.Parts {
			if _, ok := p.(*ast.StringChunk); !ok {
				if hasTryInExpr(p) {
					return true
				}
			}
		}
	}
	return false
}

func endsWithReturn(stmts []ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	_, ok := stmts[len(stmts)-1].(*ast.ReturnStmt)
	return ok
}

func (g *Generator) emitStmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.DeclStmt:
		g.emitVarDecl(s.Decl)
	case *ast.ExprStmt:
		g.emitExprStmt(s.X)
	case *ast.AssignStmt:
		g.emitAssign(s)
	case *ast.FieldAssignStmt:
		g.emitFieldAssign(s)
	case *ast.ReturnStmt:
		g.emitReturn(s)
	case *ast.IfStmt:
		g.emitIf(s)
	case *ast.ForStmt:
		g.emitFor(s)
	case *ast.MatchStmt:
		g.emitMatch(s)
	case *ast.DeferStmt:
		g.emitDefer(s)
	case *ast.ParallelStmt:
		g.emitParallel(s)
	case *ast.Block:
		for _, st := range s.Stmts {
			g.emitStmt(st)
		}
	}
}

// emitParallel lowers `parallel { task { ... } ... }` to a sync.WaitGroup
// driving one goroutine per task. We wrap the whole thing in a Go block
// so the WaitGroup name stays scoped — that lets sibling parallel blocks
// in the same function reuse the obvious local name without collision.
// The checker forbids writes to outer locals from inside a task body, so
// closure capture by reference here is safe at the language level.
func (g *Generator) emitParallel(s *ast.ParallelStmt) {
	tasks := make([]*ast.TaskStmt, 0, len(s.Body.Stmts))
	for _, st := range s.Body.Stmts {
		if ts, ok := st.(*ast.TaskStmt); ok {
			tasks = append(tasks, ts)
		}
	}
	if len(tasks) == 0 {
		return
	}
	g.addImport("sync")
	wg := g.tmp("wg")
	g.writeLine("{")
	g.indent++
	g.writeLine("var " + wg + " sync.WaitGroup")
	g.writeLine(wg + ".Add(" + itoa(len(tasks)) + ")")
	for _, ts := range tasks {
		g.writeLine("go func() {")
		g.indent++
		g.writeLine("defer " + wg + ".Done()")
		for _, bs := range ts.Body.Stmts {
			g.emitStmt(bs)
		}
		g.indent--
		g.writeLine("}()")
	}
	g.writeLine(wg + ".Wait()")
	g.indent--
	g.writeLine("}")
}

// emitDefer maps a Tartalo defer block to a Go `defer func() { ... }()`.
// Captures by reference (the body sees the enclosing function's local
// vars at the time the defer fires), matching the sh backend's behaviour.
func (g *Generator) emitDefer(s *ast.DeferStmt) {
	g.writeLine("defer func() {")
	g.indent++
	for _, st := range s.Body.Stmts {
		g.emitStmt(st)
	}
	g.indent--
	g.writeLine("}()")
}

// emitVarDecl handles a `let`/`const` inside a function body. At module
// scope we go through emitGlobalInit instead.
//
// We always emit a trailing `_ = tt_<name>` so Go's flow analysis is happy
// with declarations whose subsequent reference is dominated by a panicking
// statement (e.g. `let r = exec(...); fail("unreachable")`). Tartalo's
// checker doesn't try to prove dead-code liveness — easier to silence Go
// here than to thread the analysis everywhere. The discard compiles to
// nothing.
func (g *Generator) emitVarDecl(d *ast.VarDecl) {
	rhs := g.compileExpr(d.Value)
	from := g.info.Types[d.Value]
	to := from
	if d.TypeAnn != nil {
		if at := g.typeFromAnn(d.TypeAnn); at != nil {
			to = at
		}
	}
	if from != to {
		rhs = g.coerce(rhs, from, to)
	}
	g.writeIndent()
	if d.TypeAnn != nil && !types.Equal(from, to) {
		g.out.WriteString("var tt_")
		g.out.WriteString(d.Name)
		g.out.WriteString(" ")
		g.out.WriteString(g.goType(to))
		g.out.WriteString(" = ")
		g.out.WriteString(rhs)
	} else {
		g.out.WriteString("tt_")
		g.out.WriteString(d.Name)
		g.out.WriteString(" := ")
		g.out.WriteString(rhs)
	}
	g.out.WriteString("; _ = tt_")
	g.out.WriteString(d.Name)
	g.out.WriteByte('\n')
}

func (g *Generator) emitAssign(s *ast.AssignStmt) {
	sym := g.info.Assigns[s]
	rhs := g.compileExpr(s.Value)
	if sym != nil && g.info.Types[s.Value] != sym.Type {
		rhs = g.coerce(rhs, g.info.Types[s.Value], sym.Type)
	}
	g.writeIndent()
	if sym != nil && sym.Module != nil {
		g.out.WriteString("tt_")
		g.out.WriteString(checker.MangledName(sym.Module, s.Name))
	} else {
		g.out.WriteString("tt_")
		g.out.WriteString(s.Name)
	}
	g.out.WriteString(" = ")
	g.out.WriteString(rhs)
	g.out.WriteByte('\n')
}

func (g *Generator) emitFieldAssign(s *ast.FieldAssignStmt) {
	rhs := g.compileExpr(s.Value)
	// Coerce to the field's declared type (auto-wrap optionals, widen number→float).
	var fieldTy types.Type
	if rec, ok := g.info.Types[s.Target].(*types.Record); ok {
		if fld := rec.Lookup(s.Name); fld != nil {
			fieldTy = fld.Type
		}
	}
	rhs = g.coerce(rhs, g.info.Types[s.Value], fieldTy)
	g.writeLine(g.compileExpr(s.Target) + "." + goFieldName(s.Name) + " = " + rhs)
}

func (g *Generator) emitReturn(s *ast.ReturnStmt) {
	if s.Value == nil {
		g.writeLine("return")
		return
	}
	rhs := g.compileExpr(s.Value)
	from := g.info.Types[s.Value]
	if from != g.currentReturnType {
		rhs = g.coerce(rhs, from, g.currentReturnType)
	}
	g.writeIndent()
	g.out.WriteString("return ")
	g.out.WriteString(rhs)
	g.out.WriteByte('\n')
}

func (g *Generator) emitIf(s *ast.IfStmt) {
	g.writeIndent()
	g.out.WriteString("if ")
	g.out.WriteString(g.compileExpr(s.Cond))
	g.out.WriteString(" {")
	g.out.WriteByte('\n')
	g.indent++
	for _, st := range s.Then.Stmts {
		g.emitStmt(st)
	}
	g.indent--
	if s.Else != nil {
		// Detect `else if` by checking for a single nested IfStmt — emit it
		// inline so the resulting Go reads `else if cond {...}` instead of
		// `else { if cond {...} }`.
		if len(s.Else.Stmts) == 1 {
			if inner, ok := s.Else.Stmts[0].(*ast.IfStmt); ok {
				g.writeIndent()
				g.out.WriteString("} else ")
				g.emitIfTail(inner)
				return
			}
		}
		g.writeLine("} else {")
		g.indent++
		for _, st := range s.Else.Stmts {
			g.emitStmt(st)
		}
		g.indent--
	}
	g.writeIndent()
	g.out.WriteString("}\n")
}

// emitIfTail writes "if cond { ... } [else ...]" without the leading
// indentation — the caller is responsible for placement (used by `else if`).
func (g *Generator) emitIfTail(s *ast.IfStmt) {
	g.out.WriteString("if " + g.compileExpr(s.Cond) + " {\n")
	g.indent++
	for _, st := range s.Then.Stmts {
		g.emitStmt(st)
	}
	g.indent--
	if s.Else != nil {
		if len(s.Else.Stmts) == 1 {
			if inner, ok := s.Else.Stmts[0].(*ast.IfStmt); ok {
				g.writeIndent()
				g.out.WriteString("} else ")
				g.emitIfTail(inner)
				return
			}
		}
		g.writeLine("} else {")
		g.indent++
		for _, st := range s.Else.Stmts {
			g.emitStmt(st)
		}
		g.indent--
	}
	g.writeIndent()
	g.out.WriteString("}\n")
}

func (g *Generator) emitFor(s *ast.ForStmt) {
	switch iter := s.Iter.(type) {
	case *ast.RangeExpr:
		start := g.compileExpr(iter.Start)
		end := g.compileExpr(iter.End)
		v := g.goLocalName(s.Var)
		g.writeIndent()
		g.out.WriteString("for ")
		g.out.WriteString(v)
		g.out.WriteString(" := ")
		g.out.WriteString(start)
		g.out.WriteString("; ")
		g.out.WriteString(v)
		g.out.WriteString(" < ")
		g.out.WriteString(end)
		g.out.WriteString("; ")
		g.out.WriteString(v)
		g.out.WriteString("++ {")
		g.out.WriteByte('\n')
		g.indent++
		for _, st := range s.Body.Stmts {
			g.emitStmt(st)
		}
		g.indent--
		g.writeLine("}")
	default:
		// Iteration over an array/slice or a string-of-lines.
		// Distinguish by the iterator's type: array → range slice, string → split.
		t := g.info.Types[s.Iter]
		v := g.goLocalName(s.Var)
		switch t.(type) {
		case *types.Array:
			g.writeIndent()
			g.out.WriteString("for _, ")
			g.out.WriteString(v)
			g.out.WriteString(" := range ")
			g.out.WriteString(g.compileExpr(s.Iter))
			g.out.WriteString(" {")
			g.out.WriteByte('\n')
		default:
			// Treat as newline-delimited string. Empty string yields zero
			// iterations (matches the sh backend's `if [ -n ... ]` guard).
			g.addImport("strings")
			tmp := g.tmp("lines")
			g.writeLine(tmp + " := " + g.compileExpr(s.Iter))
			g.writeLine("if " + tmp + " != \"\" {")
			g.indent++
			g.writeLine("for _, " + v + " := range strings.Split(" + tmp + ", \"\\n\") {")
		}
		g.indent++
		for _, st := range s.Body.Stmts {
			g.emitStmt(st)
		}
		g.indent--
		g.writeIndent()
		g.out.WriteString("}\n")
		// Close the outer guard for the lines case.
		if _, ok := t.(*types.Array); !ok {
			g.indent--
			g.writeIndent()
			g.out.WriteString("}\n")
		}
	}
}

func (g *Generator) emitMatch(s *ast.MatchStmt) {
	if sum, ok := g.info.Types[s.Subject].(*types.Sum); ok {
		g.emitMatchSum(s, sum)
		return
	}
	subj := g.tmp("subj")
	g.writeLine(subj + " := " + g.compileExpr(s.Subject))
	g.writeLine("switch " + subj + " {")
	for _, arm := range s.Cases {
		// Wildcards collapse into `default:`; explicit literals into `case ...:`.
		hasWild := false
		var lits []string
		for _, p := range arm.Patterns {
			if _, ok := p.(*ast.WildcardPattern); ok {
				hasWild = true
				continue
			}
			if lp, ok := p.(*ast.LiteralPattern); ok {
				lits = append(lits, patternLiteral(lp))
			}
		}
		if hasWild {
			g.writeLine("default:")
		} else {
			g.writeLine("case " + joinComma(lits) + ":")
		}
		g.indent++
		for _, st := range arm.Body.Stmts {
			g.emitStmt(st)
		}
		g.indent--
	}
	g.writeLine("}")
}

// emitMatchSum lowers a match on a sum value to a `switch` on the value's
// Tag field. Each arm copies the bound payload fields into locals so the
// arm body can reference them by plain name, matching the sh backend.
func (g *Generator) emitMatchSum(s *ast.MatchStmt, sum *types.Sum) {
	subj := g.tmp("subj")
	g.writeLine(subj + " := " + g.compileExpr(s.Subject))
	g.writeLine("switch " + subj + ".Tag {")
	for _, arm := range s.Cases {
		hasWild := false
		var tagNames []string
		var bindings []ast.VariantBinding
		var bindVariant string
		for _, p := range arm.Patterns {
			switch p := p.(type) {
			case *ast.WildcardPattern:
				hasWild = true
			case *ast.VariantPattern:
				tagNames = append(tagNames, strconv.Quote(p.Name))
				if bindVariant == "" {
					bindVariant = p.Name
					bindings = p.Bindings
				}
			}
		}
		if hasWild {
			g.writeLine("default:")
		} else {
			g.writeLine("case " + joinComma(tagNames) + ":")
		}
		g.indent++
		if bindVariant != "" && len(bindings) > 0 {
			variant := sum.LookupVariant(bindVariant)
			if variant != nil {
				for _, b := range bindings {
					var fld *types.Field
					for i := range variant.Fields {
						if variant.Fields[i].Name == b.Name {
							fld = &variant.Fields[i]
							break
						}
					}
					if fld == nil {
						continue
					}
					g.writeLine("tt_" + b.Name + " := " + subj + ".F_" + variant.Name + "_" + b.Name)
					g.writeLine("_ = tt_" + b.Name)
				}
			}
		}
		for _, st := range arm.Body.Stmts {
			g.emitStmt(st)
		}
		g.indent--
	}
	g.writeLine("}")
}

func joinComma(ss []string) string {
	if len(ss) == 0 {
		return ""
	}
	if len(ss) == 1 {
		return ss[0]
	}
	var b strings.Builder
	for i, s := range ss {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(s)
	}
	return b.String()
}

func patternLiteral(p *ast.LiteralPattern) string {
	switch lit := p.Lit.(type) {
	case *ast.IntLit:
		return int64Lit(lit.Value)
	case *ast.BoolLit:
		if lit.Value {
			return "true"
		}
		return "false"
	case *ast.StringLit:
		// Match arms only allow literal-only strings (no interpolation), so
		// concatenating chunks reconstructs the original text.
		var b strings.Builder
		for _, p := range lit.Parts {
			if c, ok := p.(*ast.StringChunk); ok {
				b.WriteString(c.Value)
			}
		}
		return fastQuote(b.String())
	}
	return "/* unknown pattern */"
}

func (g *Generator) emitExprStmt(x ast.Expr) {
	switch x := x.(type) {
	case *ast.CallExpr:
		// Run for side-effects; discard the result if any.
		expr := g.compileCall(x)
		// Void calls compile to a bare statement; non-void calls need an
		// underscore receiver so Go doesn't complain about unused values.
		t := g.info.Types[x]
		if t == nil || t == types.Void {
			g.writeIndent()
			g.out.WriteString(expr)
			g.out.WriteByte('\n')
			return
		}
		g.writeIndent()
		g.out.WriteString("_ = ")
		g.out.WriteString(expr)
		g.out.WriteByte('\n')
	case *ast.CmdLit:
		// Discard the output but still execute the command.
		g.writeIndent()
		g.out.WriteString("_ = ")
		g.out.WriteString(g.compileCmdLit(x))
		g.out.WriteByte('\n')
	default:
		g.writeIndent()
		g.out.WriteString("_ = ")
		g.out.WriteString(g.compileExpr(x))
		g.out.WriteByte('\n')
	}
}
