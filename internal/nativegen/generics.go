package nativegen

import (
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/loader"
	"github.com/enekos/tartalo/internal/types"
)

// nativeGenericInst describes one monomorphisation of a generic FuncDecl. The
// emitter writes one Go function per distinct instantiation, named GoName,
// with currentSubst set so all type lookups resolve through Subst.
type nativeGenericInst struct {
	Subst  map[*types.TypeVar]types.Type
	Args   []types.Type
	GoName string // already includes the tt_ prefix
	Module *loader.Module
}

// typeOf returns the static type of e, applying the current monomorphisation
// substitution if any. Equivalent to g.info.Types[e] when not emitting a
// generic instantiation.
func (g *Generator) typeOf(e ast.Expr) types.Type {
	t := g.info.Types[e]
	if t == nil || g.currentSubst == nil {
		return t
	}
	return types.Substitute(t, g.currentSubst)
}

// substType applies the current monomorphisation substitution to t. Used for
// types pulled from a Symbol (function param/return types) or computed from a
// type annotation rather than from the per-expression TypeInfo map.
func (g *Generator) substType(t types.Type) types.Type {
	if t == nil || g.currentSubst == nil {
		return t
	}
	return types.Substitute(t, g.currentSubst)
}

// nativeMangleTypeArg renders a type as an identifier-safe string for use in
// monomorphised function names. Primitives keep their name, records and sums
// use their nominal name, and array/optional/func types compose recursively
// via short, collision-free suffixes.
func nativeMangleTypeArg(t types.Type) string {
	switch tt := t.(type) {
	case *types.Primitive:
		return tt.Name
	case *types.Record:
		return tt.Name
	case *types.Sum:
		return tt.Name
	case *types.Array:
		return nativeMangleTypeArg(tt.Elem) + "_arr"
	case *types.Optional:
		return nativeMangleTypeArg(tt.Elem) + "_opt"
	case *types.Func:
		var b strings.Builder
		b.WriteString("fn")
		for _, p := range tt.Params {
			b.WriteByte('_')
			b.WriteString(nativeMangleTypeArg(p))
		}
		b.WriteString("_to_")
		b.WriteString(nativeMangleTypeArg(tt.Result))
		return b.String()
	case *types.TypeVar:
		return tt.Name
	}
	return "X"
}

// nativeGenericInstName builds the Go name of one generic instantiation.
// Mirrors the sh-side genericInstName scheme so cross-target test fixtures
// can rely on a stable instance-name suffix.
func nativeGenericInstName(base string, args []types.Type) string {
	var b strings.Builder
	b.Grow(len(base) + 8 + 8*len(args))
	b.WriteString(base)
	b.WriteString("__gen")
	for _, a := range args {
		b.WriteString("__")
		b.WriteString(nativeMangleTypeArg(a))
	}
	return b.String()
}

// nativeInstKey is a stable string derived from a tuple of type args, used to
// deduplicate distinct monomorphisations of the same generic FuncDecl.
func nativeInstKey(args []types.Type) string {
	var b strings.Builder
	for i, a := range args {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(nativeMangleTypeArg(a))
	}
	return b.String()
}

// collectGenericInsts walks every CallExpr across the supplied modules and
// builds the monomorphisation plan: for each generic FuncDecl, the unique
// instantiations discovered at call sites plus the mangled Go name each will
// be emitted under. Iterates to fixed point so generic-calls-within-generics
// produce all transitively required instantiations.
func (g *Generator) collectGenericInsts(modules []*loader.Module) {
	g.genericInsts = map[*ast.FuncDecl][]nativeGenericInst{}
	if g.info.GenericInsts == nil {
		return
	}
	type funcRec struct {
		fd     *ast.FuncDecl
		module *loader.Module
	}
	byKey := map[string]funcRec{}
	for _, m := range modules {
		for _, d := range m.File.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || len(fd.TypeParams) == 0 {
				continue
			}
			byKey[checker.MangledName(m, fd.Name)] = funcRec{fd: fd, module: m}
		}
	}
	if len(byKey) == 0 {
		return
	}
	seen := map[*ast.FuncDecl]map[string]bool{}
	addInst := func(fd *ast.FuncDecl, m *loader.Module, args []types.Type) bool {
		key := nativeInstKey(args)
		if seen[fd] == nil {
			seen[fd] = map[string]bool{}
		}
		if seen[fd][key] {
			return false
		}
		seen[fd][key] = true
		sym := g.info.Decls[checker.MangledName(m, fd.Name)]
		if sym == nil {
			return false
		}
		ft, _ := sym.Type.(*types.Func)
		if ft == nil || len(ft.TypeParams) != len(args) {
			return false
		}
		subst := make(map[*types.TypeVar]types.Type, len(args))
		for i, tv := range ft.TypeParams {
			subst[tv] = args[i]
		}
		var goBase string
		if m == nil || m.IsEntry {
			goBase = "tt_" + fd.Name
		} else {
			goBase = "tt_" + checker.MangledName(m, fd.Name)
		}
		g.genericInsts[fd] = append(g.genericInsts[fd], nativeGenericInst{
			Subst:  subst,
			Args:   args,
			GoName: nativeGenericInstName(goBase, args),
			Module: m,
		})
		return true
	}
	for changed := true; changed; {
		changed = false
		for callExpr, args := range g.info.GenericInsts {
			id, _ := callExpr.Callee.(*ast.Ident)
			if id == nil {
				continue
			}
			sym := g.info.Uses[id]
			if sym == nil || sym.Module == nil {
				continue
			}
			rec, ok := byKey[checker.MangledName(sym.Module, sym.Name)]
			if !ok {
				continue
			}
			containsTV := false
			for _, a := range args {
				if types.ContainsTypeVar(a) {
					containsTV = true
					break
				}
			}
			if !containsTV {
				if addInst(rec.fd, rec.module, args) {
					changed = true
				}
				continue
			}
			outerFD, _ := g.findEnclosingGenericFunc(callExpr, modules)
			if outerFD == nil {
				continue
			}
			for _, outerInst := range g.genericInsts[outerFD] {
				resolved := make([]types.Type, len(args))
				for i, a := range args {
					resolved[i] = types.Substitute(a, outerInst.Subst)
				}
				if addInst(rec.fd, rec.module, resolved) {
					changed = true
				}
			}
		}
	}
}

func (g *Generator) findEnclosingGenericFunc(call *ast.CallExpr, modules []*loader.Module) (*ast.FuncDecl, *loader.Module) {
	for _, m := range modules {
		for _, d := range m.File.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || len(fd.TypeParams) == 0 {
				continue
			}
			if blockContainsCall(fd.Body, call) {
				return fd, m
			}
		}
	}
	return nil, nil
}

func blockContainsCall(b *ast.Block, target *ast.CallExpr) bool {
	if b == nil {
		return false
	}
	for _, s := range b.Stmts {
		if stmtContainsCall(s, target) {
			return true
		}
	}
	return false
}

func stmtContainsCall(s ast.Stmt, target *ast.CallExpr) bool {
	switch s := s.(type) {
	case *ast.DeclStmt:
		return s.Decl != nil && exprContainsCall(s.Decl.Value, target)
	case *ast.ExprStmt:
		return exprContainsCall(s.X, target)
	case *ast.AssignStmt:
		return exprContainsCall(s.Value, target)
	case *ast.FieldAssignStmt:
		return exprContainsCall(s.Target, target) || exprContainsCall(s.Value, target)
	case *ast.ReturnStmt:
		return exprContainsCall(s.Value, target)
	case *ast.IfStmt:
		return exprContainsCall(s.Cond, target) || blockContainsCall(s.Then, target) || blockContainsCall(s.Else, target)
	case *ast.ForStmt:
		return exprContainsCall(s.Iter, target) || blockContainsCall(s.Body, target)
	case *ast.MatchStmt:
		if exprContainsCall(s.Subject, target) {
			return true
		}
		for _, c := range s.Cases {
			if blockContainsCall(c.Body, target) {
				return true
			}
		}
	case *ast.DeferStmt:
		return blockContainsCall(s.Body, target)
	case *ast.ParallelStmt:
		return blockContainsCall(s.Body, target)
	case *ast.TaskStmt:
		return blockContainsCall(s.Body, target)
	case *ast.Block:
		return blockContainsCall(s, target)
	}
	return false
}

func exprContainsCall(e ast.Expr, target *ast.CallExpr) bool {
	if e == nil {
		return false
	}
	switch e := e.(type) {
	case *ast.CallExpr:
		if e == target {
			return true
		}
		if exprContainsCall(e.Callee, target) {
			return true
		}
		for _, a := range e.Args {
			if exprContainsCall(a, target) {
				return true
			}
		}
	case *ast.BinaryExpr:
		return exprContainsCall(e.Lhs, target) || exprContainsCall(e.Rhs, target)
	case *ast.UnaryExpr:
		return exprContainsCall(e.Operand, target)
	case *ast.IndexExpr:
		return exprContainsCall(e.Target, target) || exprContainsCall(e.Index, target)
	case *ast.FieldExpr:
		return exprContainsCall(e.Target, target)
	case *ast.CoalesceExpr:
		return exprContainsCall(e.Lhs, target) || exprContainsCall(e.Rhs, target)
	case *ast.UnwrapExpr:
		return exprContainsCall(e.Operand, target)
	case *ast.TryExpr:
		return exprContainsCall(e.Operand, target)
	case *ast.RangeExpr:
		return exprContainsCall(e.Start, target) || exprContainsCall(e.End, target)
	case *ast.ArrayLit:
		for _, x := range e.Elems {
			if exprContainsCall(x, target) {
				return true
			}
		}
	case *ast.RecordLit:
		if exprContainsCall(e.Spread, target) {
			return true
		}
		for _, f := range e.Fields {
			if exprContainsCall(f.Value, target) {
				return true
			}
		}
	case *ast.CastExpr:
		return exprContainsCall(e.Operand, target)
	case *ast.StringLit:
		for _, p := range e.Parts {
			if exprContainsCall(p, target) {
				return true
			}
		}
	case *ast.CmdLit:
		for _, p := range e.Parts {
			if exprContainsCall(p, target) {
				return true
			}
		}
	case *ast.FuncLit:
		return blockContainsCall(e.Body, target)
	}
	return false
}
