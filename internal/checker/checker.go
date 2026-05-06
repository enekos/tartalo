// Package checker resolves names, validates type annotations, and infers a
// type for every expression in the AST. It produces a TypeInfo side table that
// the code generator consults when emitting shell.
//
// With modules, each input file has its own value and type-name namespaces.
// Imports inject specific symbols from one module's namespace into another.
// Predeclared types (Response, Process) and builtin functions live in shared
// scopes that each module's namespaces parent into.
package checker

import (
	"fmt"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/loader"
	"github.com/enekos/tartalo/internal/token"
	"github.com/enekos/tartalo/internal/types"
)

// Symbol represents a named binding (variable, parameter, function).
//
// Top-level symbols (functions and globals) carry the *Module they were
// declared in so the codegen can mangle their shell name to avoid cross-module
// collisions. Locals and params have a nil Module — they don't need mangling.
type Symbol struct {
	Name      string
	Type      types.Type
	IsConst   bool
	IsParam   bool
	IsFunc    bool
	IsBuiltin bool
	IsExport  bool
	// IsVariant flags symbols that exist solely as unit-variant constructors
	// of a sum type. The type-checker registers one per unit variant; the
	// codegen consults the flag to materialise the value (set tag, init the
	// other variants' slots) instead of emitting a plain identifier.
	IsVariant bool
	Module    *loader.Module
	DeclPos   token.Pos
}

// TypeInfo records the result of checking. Other passes consult it.
//
// Decls indexes top-level functions and globals across ALL modules. The map
// key is the module-mangled name (`__mN__name` for non-entry modules) so the
// codegen can do simple lookups; for the single-file convenience case the
// mangling reduces to just `name`.
//
// Assigns maps each `name = expr` and `target.field = expr` statement to the
// resolved symbol of its left-hand side, so the codegen can pick the right
// shell-name (mangling, `__null` sidecar, etc.) without re-walking scopes.
type TypeInfo struct {
	Types   map[ast.Expr]types.Type
	Uses    map[*ast.Ident]*Symbol
	Decls   map[string]*Symbol
	Assigns map[*ast.AssignStmt]*Symbol
}

func newTypeInfo() *TypeInfo {
	return &TypeInfo{
		Types:   map[ast.Expr]types.Type{},
		Uses:    map[*ast.Ident]*Symbol{},
		Decls:   map[string]*Symbol{},
		Assigns: map[*ast.AssignStmt]*Symbol{},
	}
}

// scope is a lexical name lookup chain.
type scope struct {
	parent *scope
	syms   map[string]*Symbol
}

func newScope(parent *scope) *scope {
	return &scope{parent: parent, syms: map[string]*Symbol{}}
}

func (s *scope) define(sym *Symbol) bool {
	if _, exists := s.syms[sym.Name]; exists {
		return false
	}
	s.syms[sym.Name] = sym
	return true
}

func (s *scope) resolve(name string) *Symbol {
	for cur := s; cur != nil; cur = cur.parent {
		if sym, ok := cur.syms[name]; ok {
			return sym
		}
	}
	return nil
}

// moduleEnv is the per-module checker state: its top-level value scope, its
// type-name namespace (own + imported + predeclared via parent chain), and a
// link back to the loader module.
type moduleEnv struct {
	module    *loader.Module
	scope     *scope
	typeNames map[string]types.Type
	// variantOf maps a sum-variant's bare name (e.g. "Circle") to its parent
	// Sum. Built during type-decl resolution; used by checkRecordLit to route
	// `Circle{r:5}` to variant construction and by the codegen to look up the
	// owning sum by variant name.
	variantOf map[string]*types.Sum
}

// Checker is the type-checker driver.
type Checker struct {
	info *TypeInfo
	errs []error

	// Shared, populated once at construction.
	predeclTypes map[string]types.Type
	builtinScope *scope

	// Per-module state, keyed by *loader.Module pointer.
	envs map[*loader.Module]*moduleEnv

	// Current-function state.
	current    *scope
	currentMod *loader.Module
	currentRet types.Type

	// inTest is true while checking the body of a `test "..." { ... }` decl.
	// Assertion builtins (assertEq, fail, skip, ...) require this to be true.
	// Helpers called from tests cannot use them directly — pass a bool back
	// and use `check(...)` at the call site.
	inTest bool
}

func New() *Checker {
	c := &Checker{
		info:         newTypeInfo(),
		predeclTypes: map[string]types.Type{},
		builtinScope: newScope(nil),
		envs:         map[*loader.Module]*moduleEnv{},
	}
	for _, r := range builtinTypes() {
		c.predeclTypes[r.Name] = r
	}
	for _, b := range builtins() {
		c.builtinScope.define(b)
	}
	for _, b := range builtinsWithTypes(c.predeclTypes) {
		c.builtinScope.define(b)
	}
	return c
}

// CheckFile is a convenience wrapper for the single-file case (used by tests
// and by callers that don't need module resolution). It synthesises a Module
// with the file as its only content.
func (c *Checker) CheckFile(f *ast.File) (*TypeInfo, []error) {
	m := &loader.Module{File: f, IsEntry: true}
	return c.Check([]*loader.Module{m})
}

// Check accepts modules in topological order (dependencies before
// dependents) and returns the populated TypeInfo plus accumulated errors.
func (c *Checker) Check(modules []*loader.Module) (*TypeInfo, []error) {
	if len(modules) == 0 {
		return c.info, nil
	}

	// Pass 0: build the per-module env for each module.
	for _, m := range modules {
		env := &moduleEnv{
			module:    m,
			scope:     newScope(c.builtinScope),
			typeNames: map[string]types.Type{},
			variantOf: map[string]*types.Sum{},
		}
		c.envs[m] = env
	}

	// Pass 1a: register placeholder Records for every type decl in every module.
	// This lets a record's fields reference other records declared later.
	for _, m := range modules {
		env := c.envs[m]
		for _, d := range m.File.Decls {
			td, ok := d.(*ast.TypeDecl)
			if !ok {
				continue
			}
			if _, predeclared := c.predeclTypes[td.Name]; predeclared {
				c.errorf(td.NamePos, "cannot redeclare predeclared type %q", td.Name)
				continue
			}
			if _, exists := env.typeNames[td.Name]; exists {
				c.errorf(td.NamePos, "redeclaration of type %q in %s", td.Name, moduleLabel(m))
				continue
			}
			switch td.Spec.(type) {
			case *ast.SumType:
				env.typeNames[td.Name] = &types.Sum{Name: td.Name}
			default:
				env.typeNames[td.Name] = &types.Record{Name: td.Name}
			}
		}
	}

	// Pass 1b: import TYPE names from deps. Deps' placeholder records are
	// already registered (pass 1a), and types-only imports unblock pass 1c.
	for _, m := range modules {
		c.resolveTypeImports(m)
	}

	// Pass 1c: fill in record fields now that all type names (own + imported)
	// are visible.
	for _, m := range modules {
		c.currentMod = m
		for _, d := range m.File.Decls {
			if td, ok := d.(*ast.TypeDecl); ok {
				c.resolveTypeDecl(td)
			}
		}
	}

	// Pass 1c.5: reject record types that would expand infinitely at codegen.
	c.detectRecordCycles(modules)

	// Pass 1d: declare functions in their owning module's scope. Signatures
	// reference fully-resolved types from pass 1c.
	for _, m := range modules {
		c.currentMod = m
		for _, d := range m.File.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				c.declareFunc(fd, m)
			}
		}
	}

	// Pass 1e: import VALUE symbols (functions and exported globals). Globals
	// won't be in the scope yet — only functions and that's enough for body
	// type-checking; value globals get resolved on use via the dep's scope
	// after pass 2 populates them. We refresh after pass 2 below where needed.
	for _, m := range modules {
		c.resolveValueImports(m)
	}

	// Pass 2: globals + function bodies + test bodies, per module in
	// topological order. Tests share the function-body machinery: they're
	// effectively void parameterless functions, just not addressable as
	// callable values.
	seenTestNames := map[*loader.Module]map[string]token.Pos{}
	for _, m := range modules {
		c.currentMod = m
		c.current = c.envs[m].scope
		for _, d := range m.File.Decls {
			switch d := d.(type) {
			case *ast.VarDecl:
				c.checkVarDecl(d, true)
			case *ast.FuncDecl:
				c.checkFuncBody(d, m)
			case *ast.TestDecl:
				if seenTestNames[m] == nil {
					seenTestNames[m] = map[string]token.Pos{}
				}
				if prev, ok := seenTestNames[m][d.Name]; ok {
					c.errorf(d.NamePos, "duplicate test name %q (previous at %s)", d.Name, prev)
				} else {
					seenTestNames[m][d.Name] = d.NamePos
				}
				c.checkTestBody(d, m)
			}
		}
	}
	return c.info, c.errs
}

// checkTestBody walks a test declaration body in module scope. Tests have no
// parameters and no return value, so the body looks like a void function with
// an empty parameter list. The lexical scope chain is module → fresh body.
func (c *Checker) checkTestBody(td *ast.TestDecl, m *loader.Module) {
	env := c.envs[m]
	saved := c.current
	savedRet := c.currentRet
	savedInTest := c.inTest
	c.current = newScope(env.scope)
	c.currentRet = types.Void
	c.inTest = true
	c.checkBlock(td.Body)
	c.current = saved
	c.currentRet = savedRet
	c.inTest = savedInTest
}

// resolveTypeImports brings exported type names from dep modules into m's
// typeNames. Names that aren't types are silently skipped here — they'll be
// picked up by resolveValueImports after function declarations exist.
func (c *Checker) resolveTypeImports(m *loader.Module) {
	env := c.envs[m]
	for _, ri := range m.Imports {
		dep := ri.Module
		if dep == nil {
			continue
		}
		depEnv := c.envs[dep]
		for _, n := range ri.Decl.Names {
			if t := importTypeIfExported(depEnv, dep, n.Name); t != nil {
				if _, exists := env.typeNames[n.Name]; exists {
					c.errorf(n.NamePos, "duplicate type name %q in module scope", n.Name)
					continue
				}
				env.typeNames[n.Name] = t
			}
		}
	}
}

// resolveValueImports brings exported functions and globals into m's scope.
// Names that didn't resolve as types AND don't resolve as values here are
// reported as unknown.
func (c *Checker) resolveValueImports(m *loader.Module) {
	env := c.envs[m]
	for _, ri := range m.Imports {
		dep := ri.Module
		if dep == nil {
			continue
		}
		depEnv := c.envs[dep]
		for _, n := range ri.Decl.Names {
			// If we already imported this as a type, fine — skip the value path.
			if _, ok := env.typeNames[n.Name]; ok {
				if isExportedType(dep, n.Name) {
					continue
				}
			}
			if found := importValueIfExported(depEnv, dep, n.Name); found != nil {
				if !env.scope.define(found) {
					c.errorf(n.NamePos, "duplicate name %q in module scope (already imported or declared)", n.Name)
				}
				continue
			}
			// Nothing matched: report unknown. We only emit this error in the
			// value pass so that a name that was successfully imported as a
			// type doesn't trip the diagnostic.
			if !isExportedType(dep, n.Name) {
				c.errorf(n.NamePos, "module %q has no exported name %q", ri.Decl.Path, n.Name)
			}
		}
	}
}

// isExportedType reports whether dep's source declares `export type Name`.
func isExportedType(dep *loader.Module, name string) bool {
	for _, d := range dep.File.Decls {
		if td, ok := d.(*ast.TypeDecl); ok && td.Name == name && td.IsExported {
			return true
		}
	}
	return false
}

// importValueIfExported looks up a function/global symbol in the dependency
// module, returning it iff the corresponding decl was marked `export`. We
// walk the dep's AST because the env scope contains imported names too, and
// we want to import only names *originally* declared (and exported) by dep.
func importValueIfExported(env *moduleEnv, dep *loader.Module, name string) *Symbol {
	for _, d := range dep.File.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Name == name && d.IsExported {
				if sym := env.scope.resolveLocal(name); sym != nil {
					return sym
				}
			}
		case *ast.VarDecl:
			if d.Name == name && d.IsExported {
				if sym := env.scope.resolveLocal(name); sym != nil {
					return sym
				}
			}
		}
	}
	return nil
}

func importTypeIfExported(env *moduleEnv, dep *loader.Module, name string) types.Type {
	for _, d := range dep.File.Decls {
		if td, ok := d.(*ast.TypeDecl); ok && td.Name == name && td.IsExported {
			if t, ok := env.typeNames[name]; ok {
				return t
			}
		}
	}
	return nil
}

// resolveLocal looks up a name only in the immediate scope (no parent walk).
// Used for import resolution where we want the dep's own symbols, not
// transitively imported ones, and not builtins.
func (s *scope) resolveLocal(name string) *Symbol {
	if sym, ok := s.syms[name]; ok {
		return sym
	}
	return nil
}

func (c *Checker) errorf(pos token.Pos, format string, args ...any) {
	c.errs = append(c.errs, fmt.Errorf("%s: %s", pos, fmt.Sprintf(format, args...)))
}

// resolveTypeName walks a module's typeNames + the predeclared types.
func (c *Checker) resolveTypeName(name string) types.Type {
	if c.currentMod != nil {
		if env := c.envs[c.currentMod]; env != nil {
			if t, ok := env.typeNames[name]; ok {
				return t
			}
		}
	}
	if t, ok := c.predeclTypes[name]; ok {
		return t
	}
	return nil
}

// resolveTypeDecl fills in the previously-registered placeholder Record or
// Sum type.
func (c *Checker) resolveTypeDecl(td *ast.TypeDecl) {
	env := c.envs[c.currentMod]
	switch existing := env.typeNames[td.Name].(type) {
	case *types.Record:
		c.resolveRecordDecl(td, existing)
	case *types.Sum:
		c.resolveSumDecl(td, existing)
	}
}

func (c *Checker) resolveRecordDecl(td *ast.TypeDecl, target *types.Record) {
	rt, ok := td.Spec.(*ast.RecordType)
	if !ok {
		c.errorf(td.NamePos, "type %q must be a record (e.g. `{ a: T, b: U }`)", td.Name)
		return
	}
	seen := map[string]bool{}
	for _, f := range rt.Fields {
		if seen[f.Name] {
			c.errorf(f.NamePos, "duplicate field %q in record %q", f.Name, td.Name)
			continue
		}
		seen[f.Name] = true
		ft := c.resolveTypeExpr(f.TypeAnn)
		if !c.isAllowedRecordFieldType(ft) {
			c.errorf(f.NamePos, "field %q has unsupported type %s; allowed: primitive, record, primitive[], or T?",
				f.Name, types.Format(ft))
			ft = types.Invalid
		}
		target.Fields = append(target.Fields, types.Field{Name: f.Name, Type: ft})
	}
}

func (c *Checker) resolveSumDecl(td *ast.TypeDecl, target *types.Sum) {
	st, ok := td.Spec.(*ast.SumType)
	if !ok {
		c.errorf(td.NamePos, "type %q must be a sum (e.g. `A{...} | B`)", td.Name)
		return
	}
	if len(st.Variants) == 0 {
		c.errorf(td.NamePos, "sum type %q must have at least one variant", td.Name)
		return
	}
	env := c.envs[c.currentMod]
	seenVariants := map[string]bool{}
	for _, v := range st.Variants {
		if seenVariants[v.Name] {
			c.errorf(v.NamePos, "duplicate variant %q in sum %q", v.Name, td.Name)
			continue
		}
		seenVariants[v.Name] = true
		// Cross-sum collisions are an error: variant names are looked up
		// directly in record/variant construction so they must be unique
		// within a module.
		if existing := env.variantOf[v.Name]; existing != nil && existing != target {
			c.errorf(v.NamePos,
				"variant %q is already declared in sum %q",
				v.Name, existing.Name)
		}
		env.variantOf[v.Name] = target
		variant := types.Variant{Name: v.Name}
		seenFields := map[string]bool{}
		for _, f := range v.Fields {
			if seenFields[f.Name] {
				c.errorf(f.NamePos, "duplicate field %q in variant %q", f.Name, v.Name)
				continue
			}
			seenFields[f.Name] = true
			ft := c.resolveTypeExpr(f.TypeAnn)
			if !c.isAllowedVariantFieldType(ft) {
				c.errorf(f.NamePos,
					"variant field %q has unsupported type %s; allowed: primitive or optional primitive",
					f.Name, types.Format(ft))
				ft = types.Invalid
			}
			variant.Fields = append(variant.Fields, types.Field{Name: f.Name, Type: ft})
		}
		target.Variants = append(target.Variants, variant)
	}
	// Unit variants double as value-level constants of the sum's type so the
	// user can write `let s: Shape = Empty`. Non-unit variants stay on the
	// type-level — they are constructed via the record-literal path.
	for _, v := range target.Variants {
		if len(v.Fields) > 0 {
			continue
		}
		sym := &Symbol{
			Name:      v.Name,
			Type:      target,
			IsConst:   true,
			IsVariant: true,
			DeclPos:   td.NamePos,
			Module:    c.currentMod,
		}
		if !c.envs[c.currentMod].scope.define(sym) {
			c.errorf(td.NamePos, "unit variant %q collides with an existing name", v.Name)
		}
	}
}

// isAllowedVariantFieldType is stricter than the record field shape: variant
// payloads are flattened into the sum's fixed slot layout, so each leaf
// must be a primitive (or optional primitive). No nested records, arrays, or
// sums for v0.
func (c *Checker) isAllowedVariantFieldType(t types.Type) bool {
	if t == types.Invalid {
		return true
	}
	switch t {
	case types.String, types.Number, types.Bool, types.Float:
		return true
	}
	if o, ok := t.(*types.Optional); ok {
		switch o.Elem {
		case types.String, types.Number, types.Bool, types.Float:
			return true
		}
		return false
	}
	return false
}

// isAllowedRecordFieldType decides whether a resolved type may appear as the
// type of a record field. Allowed: primitives (string/number/bool), optional
// of those primitives, arrays of primitives, and other records (cycle-free —
// validated separately). Disallowed: float scalar, optional records, optional
// arrays, arrays of records.
func (c *Checker) isAllowedRecordFieldType(t types.Type) bool {
	if t == types.Invalid {
		return true // already reported; don't cascade
	}
	switch t {
	case types.String, types.Number, types.Bool:
		return true
	}
	if o, ok := t.(*types.Optional); ok {
		switch o.Elem {
		case types.String, types.Number, types.Bool:
			return true
		}
		return false
	}
	if _, ok := t.(*types.Record); ok {
		return true
	}
	if a, ok := t.(*types.Array); ok {
		switch a.Elem {
		case types.String, types.Number, types.Bool, types.Float:
			return true
		}
		return false
	}
	return false
}

// firstReturnIn finds the position of the first return statement transitively
// contained in the given block. Used to reject `return` inside `defer { ... }`.
func firstReturnIn(b *ast.Block) (token.Pos, bool) {
	for _, s := range b.Stmts {
		if pos, ok := firstReturnInStmt(s); ok {
			return pos, true
		}
	}
	return token.Pos{}, false
}

func firstReturnInStmt(s ast.Stmt) (token.Pos, bool) {
	switch s := s.(type) {
	case *ast.ReturnStmt:
		return s.KwPos, true
	case *ast.IfStmt:
		if pos, ok := firstReturnIn(s.Then); ok {
			return pos, true
		}
		if s.Else != nil {
			if pos, ok := firstReturnIn(s.Else); ok {
				return pos, true
			}
		}
	case *ast.ForStmt:
		return firstReturnIn(s.Body)
	case *ast.MatchStmt:
		for _, arm := range s.Cases {
			if arm.Body != nil {
				if pos, ok := firstReturnIn(arm.Body); ok {
					return pos, true
				}
			}
		}
	case *ast.Block:
		return firstReturnIn(s)
	case *ast.DeferStmt:
		// Nested defer's body is its own scope; its returns are flagged when
		// we recurse into it via the outer checkStmt. Don't double-report.
	}
	return token.Pos{}, false
}

// firstNonPrimitiveLeaf walks a record's tree and returns the path to the
// first leaf field whose type is not a primitive (or optional primitive).
// Used to reject arrays-of-records whose elements would conflict with the
// row-based codegen encoding (no array leaves; nested records are fine
// because they flatten to primitive leaves).
func firstNonPrimitiveLeaf(rec *types.Record, path []string) []string {
	for _, f := range rec.Fields {
		next := append(path, f.Name)
		switch ft := f.Type.(type) {
		case *types.Primitive:
			continue
		case *types.Optional:
			if _, ok := ft.Elem.(*types.Primitive); !ok {
				return next
			}
		case *types.Record:
			if found := firstNonPrimitiveLeaf(ft, next); found != nil {
				return found
			}
		default:
			return next
		}
	}
	return nil
}

// detectRecordCycles emits an error for each record type that transitively
// contains itself through record-typed fields. Optionals don't break a cycle
// in v0 because optional records are not allowed as fields.
func (c *Checker) detectRecordCycles(modules []*loader.Module) {
	var reaches func(start, cur *types.Record, seen map[*types.Record]bool, path []string) []string
	reaches = func(start, cur *types.Record, seen map[*types.Record]bool, path []string) []string {
		for _, f := range cur.Fields {
			sub, ok := f.Type.(*types.Record)
			if !ok {
				continue
			}
			next := append(path, f.Name+":"+sub.Name)
			if sub == start {
				return next
			}
			if seen[sub] {
				continue
			}
			seen[sub] = true
			if found := reaches(start, sub, seen, next); found != nil {
				return found
			}
		}
		return nil
	}
	for _, m := range modules {
		for _, d := range m.File.Decls {
			td, ok := d.(*ast.TypeDecl)
			if !ok {
				continue
			}
			rec, ok := c.envs[m].typeNames[td.Name].(*types.Record)
			if !ok {
				continue
			}
			if path := reaches(rec, rec, map[*types.Record]bool{rec: true}, nil); path != nil {
				c.errorf(td.NamePos, "cyclic record type %q (via %s)",
					td.Name, strings.Join(path, " -> "))
			}
		}
	}
}

// resolveTypeExpr converts an AST type annotation into a types.Type.
func (c *Checker) resolveTypeExpr(te ast.TypeExpr) types.Type {
	if te == nil {
		return types.Invalid
	}
	switch t := te.(type) {
	case *ast.TypeName:
		if got := types.Lookup(t.Name); got != nil {
			return got
		}
		if got := c.resolveTypeName(t.Name); got != nil {
			return got
		}
		c.errorf(t.NamePos, "unknown type %q", t.Name)
		return types.Invalid
	case *ast.ArrayType:
		elem := c.resolveTypeExpr(t.Elem)
		if elem == types.Void {
			c.errorf(t.LBracket, "array element type cannot be void")
			return types.Invalid
		}
		if rec, isRec := elem.(*types.Record); isRec {
			// Arrays of records use a row-per-element encoding (newline as the
			// row separator, ASCII Unit Separator between leaf fields). That
			// requires every leaf to be a primitive — array leaves would
			// inject newlines and break the row delimiter.
			if path := firstNonPrimitiveLeaf(rec, nil); path != nil {
				c.errorf(t.LBracket,
					"arrays of records require all leaf fields to be primitives; record %q has a non-primitive at .%s",
					rec.Name, strings.Join(path, "."))
				return types.Invalid
			}
		}
		if _, isOpt := elem.(*types.Optional); isOpt {
			c.errorf(t.LBracket, "v0 does not support arrays of optionals")
			return types.Invalid
		}
		return &types.Array{Elem: elem}
	case *ast.OptionalType:
		elem := c.resolveTypeExpr(t.Elem)
		if elem == types.Void {
			c.errorf(t.QPos, "void cannot be made optional")
			return types.Invalid
		}
		if _, ok := elem.(*types.Optional); ok {
			c.errorf(t.QPos, "type is already optional; `T??` is not allowed")
			return types.Invalid
		}
		if _, ok := elem.(*types.Array); ok {
			c.errorf(t.QPos, "v0 does not support optional arrays")
			return types.Invalid
		}
		return &types.Optional{Elem: elem}
	case *ast.RecordType:
		c.errorf(te.Pos(), "anonymous record types are not supported; declare with `type Name = { ... }`")
		return types.Invalid
	case *ast.FuncType:
		params := make([]types.Type, len(t.Params))
		for i, p := range t.Params {
			params[i] = c.resolveTypeExpr(p)
		}
		result := c.resolveTypeExpr(t.Result)
		return &types.Func{Params: params, Result: result}
	}
	c.errorf(te.Pos(), "unsupported type expression")
	return types.Invalid
}

func (c *Checker) declareFunc(fd *ast.FuncDecl, m *loader.Module) {
	env := c.envs[m]
	params := make([]types.Type, len(fd.Params))
	for i, p := range fd.Params {
		params[i] = c.resolveTypeExpr(p.TypeAnn)
	}
	ret := c.resolveTypeExpr(fd.Result)
	sym := &Symbol{
		Name:     fd.Name,
		Type:     &types.Func{Params: params, Result: ret},
		IsFunc:   true,
		IsExport: fd.IsExported,
		Module:   m,
		DeclPos:  fd.NamePos,
	}
	if !env.scope.define(sym) {
		c.errorf(fd.NamePos, "redeclaration of %q", fd.Name)
		return
	}
	c.info.Decls[mangledName(m, fd.Name)] = sym
}

// mangledName produces the lookup key used in TypeInfo.Decls and the shell
// identifier used by codegen. The entry module — and the synthetic
// single-file module created by CheckFile — keeps unmangled names so the
// generated sh stays clean for the common case. Imported modules get an
// `__m<id>__` prefix so their globals can't collide with the entry module's
// or with each other's.
func mangledName(m *loader.Module, name string) string {
	if m == nil || m.IsEntry {
		return name
	}
	return fmt.Sprintf("__m%d__%s", m.ID, name)
}

// MangledName is the public form, used by codegen.
func MangledName(m *loader.Module, name string) string { return mangledName(m, name) }

// moduleLabel returns a short human label for a module in error messages.
func moduleLabel(m *loader.Module) string {
	if m == nil {
		return "<synthetic>"
	}
	if m.AbsPath != "" {
		return m.AbsPath
	}
	if m.File != nil {
		return m.File.Path
	}
	return "<unknown>"
}

func (c *Checker) checkFuncBody(fd *ast.FuncDecl, m *loader.Module) {
	env := c.envs[m]
	sym := env.scope.resolveLocal(fd.Name)
	if sym == nil {
		return
	}
	ft := sym.Type.(*types.Func)
	saved := c.current
	savedRet := c.currentRet
	savedInTest := c.inTest
	c.current = newScope(env.scope)
	c.currentRet = ft.Result
	c.inTest = false
	for i, p := range fd.Params {
		paramSym := &Symbol{
			Name:    p.Name,
			Type:    ft.Params[i],
			IsParam: true,
			DeclPos: p.NamePos,
		}
		if !c.current.define(paramSym) {
			c.errorf(p.NamePos, "duplicate parameter %q", p.Name)
		}
	}
	c.checkBlock(fd.Body)
	c.current = saved
	c.currentRet = savedRet
	c.inTest = savedInTest
}

func (c *Checker) checkVarDecl(d *ast.VarDecl, isGlobal bool) {
	// Record-type init shortcut: empty array literal needs the annotated type.
	var bound types.Type
	if d.TypeAnn != nil {
		declared := c.resolveTypeExpr(d.TypeAnn)
		if al, isArrLit := d.Value.(*ast.ArrayLit); isArrLit && len(al.Elems) == 0 {
			c.info.Types[al] = declared
			bound = declared
			c.bindVar(d, bound, isGlobal)
			return
		}
		got := c.checkExpr(d.Value)
		if declared != types.Invalid && got != types.Invalid && !types.IsAssignable(got, declared) {
			c.errorf(d.Value.Pos(),
				"type mismatch: variable %q declared as %s, initializer is %s",
				d.Name, types.Format(declared), types.Format(got))
		}
		bound = declared
	} else {
		got := c.checkExpr(d.Value)
		switch got {
		case types.Void:
			c.errorf(d.Value.Pos(), "cannot infer type for %q: initializer has type void", d.Name)
			bound = types.Invalid
		case types.Null:
			c.errorf(d.Value.Pos(), "cannot infer type for %q from a bare null; add an annotation like `: T?`", d.Name)
			bound = types.Invalid
		default:
			bound = got
		}
	}
	c.bindVar(d, bound, isGlobal)
}

func (c *Checker) bindVar(d *ast.VarDecl, bound types.Type, isGlobal bool) {
	sym := &Symbol{
		Name:     d.Name,
		Type:     bound,
		IsConst:  d.IsConst,
		IsExport: d.IsExported,
		DeclPos:  d.NamePos,
	}
	if isGlobal {
		sym.Module = c.currentMod
	}
	if !c.current.define(sym) {
		c.errorf(d.NamePos, "redeclaration of %q", d.Name)
	}
	if isGlobal {
		c.info.Decls[mangledName(c.currentMod, d.Name)] = sym
	}
}

func (c *Checker) checkBlock(b *ast.Block) {
	saved := c.current
	c.current = newScope(saved)
	for _, s := range b.Stmts {
		c.checkStmt(s)
	}
	c.current = saved
}

func (c *Checker) checkStmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.DeclStmt:
		c.checkVarDecl(s.Decl, false)
	case *ast.ExprStmt:
		c.checkExpr(s.X)
	case *ast.AssignStmt:
		sym := c.current.resolve(s.Name)
		if sym == nil {
			c.errorf(s.NamePos, "undefined name %q", s.Name)
			c.checkExpr(s.Value)
			return
		}
		c.info.Assigns[s] = sym
		if sym.IsConst {
			c.errorf(s.NamePos, "cannot assign to const %q", s.Name)
		}
		if sym.IsFunc {
			c.errorf(s.NamePos, "cannot assign to function %q", s.Name)
		}
		got := c.checkExpr(s.Value)
		if got != types.Invalid && sym.Type != types.Invalid && !types.IsAssignable(got, sym.Type) {
			c.errorf(s.Value.Pos(),
				"type mismatch: %q is %s, value is %s",
				s.Name, types.Format(sym.Type), types.Format(got))
		}
	case *ast.ReturnStmt:
		if s.Value == nil {
			if c.currentRet != types.Void {
				c.errorf(s.KwPos, "function returns %s, return statement has no value", types.Format(c.currentRet))
			}
			return
		}
		got := c.checkExpr(s.Value)
		if c.currentRet == types.Void {
			c.errorf(s.Value.Pos(), "void function cannot return a value")
		} else if got != types.Invalid && c.currentRet != types.Invalid && !types.IsAssignable(got, c.currentRet) {
			c.errorf(s.Value.Pos(),
				"return type mismatch: function returns %s, got %s",
				types.Format(c.currentRet), types.Format(got))
		}
	case *ast.IfStmt:
		ct := c.checkExpr(s.Cond)
		if ct != types.Invalid && ct != types.Bool {
			c.errorf(s.Cond.Pos(), "if condition must be bool, got %s", types.Format(ct))
		}
		c.checkBlock(s.Then)
		if s.Else != nil {
			c.checkBlock(s.Else)
		}
	case *ast.ForStmt:
		var elemTy types.Type
		switch iter := s.Iter.(type) {
		case *ast.RangeExpr:
			st := c.checkExpr(iter.Start)
			et := c.checkExpr(iter.End)
			if st != types.Number {
				c.errorf(iter.Start.Pos(), "range start must be number, got %s", types.Format(st))
			}
			if et != types.Number {
				c.errorf(iter.End.Pos(), "range end must be number, got %s", types.Format(et))
			}
			c.info.Types[iter] = types.Number
			elemTy = types.Number
		default:
			ity := c.checkExpr(s.Iter)
			switch t := ity.(type) {
			case *types.Array:
				elemTy = t.Elem
			case *types.Primitive:
				if t == types.String {
					elemTy = types.String
				} else if t != types.Invalid {
					c.errorf(s.Iter.Pos(), "for-in iterable must be a range, array, or string, got %s", types.Format(ity))
					elemTy = types.Invalid
				} else {
					elemTy = types.Invalid
				}
			default:
				c.errorf(s.Iter.Pos(), "for-in iterable must be a range, array, or string, got %s", types.Format(ity))
				elemTy = types.Invalid
			}
		}
		saved := c.current
		c.current = newScope(saved)
		c.current.define(&Symbol{Name: s.Var, Type: elemTy, DeclPos: s.VarPos})
		for _, st := range s.Body.Stmts {
			c.checkStmt(st)
		}
		c.current = saved
	case *ast.Block:
		c.checkBlock(s)
	case *ast.MatchStmt:
		c.checkMatch(s)
	case *ast.DeferStmt:
		if c.currentRet == nil {
			c.errorf(s.KwPos, "defer is only valid inside a function body")
		}
		c.checkBlock(s.Body)
		if pos, has := firstReturnIn(s.Body); has {
			c.errorf(pos, "return is not allowed inside a defer block")
		}
	case *ast.FieldAssignStmt:
		tt := c.checkExpr(s.Target)
		rec, ok := tt.(*types.Record)
		if !ok {
			if tt != types.Invalid {
				c.errorf(s.NamePos, "field assignment requires a record, got %s", types.Format(tt))
			}
			c.checkExpr(s.Value)
			return
		}
		f := rec.Lookup(s.Name)
		if f == nil {
			c.errorf(s.NamePos, "record %q has no field %q", rec.Name, s.Name)
			c.checkExpr(s.Value)
			return
		}
		got := c.checkExpr(s.Value)
		if got != types.Invalid && f.Type != types.Invalid && !types.IsAssignable(got, f.Type) {
			c.errorf(s.Value.Pos(),
				"field %q: expected %s, got %s",
				s.Name, types.Format(f.Type), types.Format(got))
		}
	default:
		c.errorf(s.Pos(), "unhandled statement type %T", s)
	}
}

func (c *Checker) checkMatch(s *ast.MatchStmt) {
	subj := c.checkExpr(s.Subject)
	switch subj {
	case types.Invalid, types.String, types.Number, types.Bool:
		// primitive subjects continue to use the literal-pattern path.
	default:
		if _, ok := subj.(*types.Sum); !ok {
			c.errorf(s.Subject.Pos(),
				"match subject must be a primitive (string, number, bool) or a sum type, got %s",
				types.Format(subj))
		}
	}
	for _, arm := range s.Cases {
		// Each arm's bindings live in their own scope so different arms can
		// rebind the same variant field name without colliding.
		saved := c.current
		c.current = newScope(saved)
		for _, pat := range arm.Patterns {
			c.checkPattern(pat, subj)
		}
		c.checkBlock(arm.Body)
		c.current = saved
	}
}

func (c *Checker) checkPattern(p ast.Pattern, subjectTy types.Type) {
	switch p := p.(type) {
	case *ast.WildcardPattern:
		return
	case *ast.VariantPattern:
		sum, ok := subjectTy.(*types.Sum)
		if !ok {
			if subjectTy != types.Invalid {
				c.errorf(p.NamePos,
					"variant pattern requires a sum subject, got %s",
					types.Format(subjectTy))
			}
			return
		}
		v := sum.LookupVariant(p.Name)
		if v == nil {
			c.errorf(p.NamePos, "variant %q is not part of sum %q", p.Name, sum.Name)
			return
		}
		if !p.HasBraces {
			if len(v.Fields) > 0 {
				c.errorf(p.NamePos,
					"variant %q has fields; pattern must list bindings (e.g. `%s{%s}`)",
					v.Name, v.Name, firstFieldName(v))
			}
			return
		}
		// Pattern uses braces: bind the listed fields as locals.
		seen := map[string]bool{}
		for _, b := range p.Bindings {
			if seen[b.Name] {
				c.errorf(b.NamePos, "duplicate binding %q in variant pattern", b.Name)
				continue
			}
			seen[b.Name] = true
			var fld *types.Field
			for i := range v.Fields {
				if v.Fields[i].Name == b.Name {
					fld = &v.Fields[i]
					break
				}
			}
			if fld == nil {
				c.errorf(b.NamePos, "variant %q has no field %q", v.Name, b.Name)
				continue
			}
			c.current.define(&Symbol{
				Name:    b.Name,
				Type:    fld.Type,
				IsConst: true,
				DeclPos: b.NamePos,
			})
		}
	case *ast.LiteralPattern:
		var lt types.Type
		switch lit := p.Lit.(type) {
		case *ast.IntLit:
			lt = types.Number
		case *ast.BoolLit:
			lt = types.Bool
		case *ast.StringLit:
			for _, part := range lit.Parts {
				if _, ok := part.(*ast.StringChunk); !ok {
					c.errorf(part.Pos(), "match pattern strings cannot contain interpolations")
				}
			}
			lt = types.String
		default:
			c.errorf(p.Pos(), "unsupported literal pattern")
			return
		}
		if subjectTy != types.Invalid && !types.Equal(lt, subjectTy) {
			c.errorf(p.Pos(), "pattern type %s does not match subject type %s",
				types.Format(lt), types.Format(subjectTy))
		}
		c.info.Types[p.Lit] = lt
	}
}

func firstFieldName(v *types.Variant) string {
	if len(v.Fields) == 0 {
		return ""
	}
	return v.Fields[0].Name
}

func (c *Checker) checkExpr(e ast.Expr) types.Type {
	if e == nil {
		return types.Invalid
	}
	t := c.inferExpr(e)
	c.info.Types[e] = t
	return t
}

func (c *Checker) inferExpr(e ast.Expr) types.Type {
	switch e := e.(type) {
	case *ast.IntLit:
		return types.Number
	case *ast.FloatLit:
		return types.Float
	case *ast.BoolLit:
		return types.Bool
	case *ast.NullLit:
		return types.Null
	case *ast.StringChunk:
		return types.String
	case *ast.StringLit:
		for _, p := range e.Parts {
			pt := c.checkExpr(p)
			if _, isChunk := p.(*ast.StringChunk); isChunk {
				continue
			}
			if pt != types.String && pt != types.Number && pt != types.Bool && pt != types.Invalid {
				c.errorf(p.Pos(), "cannot interpolate value of type %s into string", types.Format(pt))
			}
		}
		return types.String
	case *ast.CmdLit:
		for _, p := range e.Parts {
			pt := c.checkExpr(p)
			if _, isChunk := p.(*ast.StringChunk); isChunk {
				continue
			}
			if pt != types.String && pt != types.Number && pt != types.Bool && pt != types.Invalid {
				c.errorf(p.Pos(), "cannot interpolate value of type %s into command", types.Format(pt))
			}
		}
		return types.String
	case *ast.Ident:
		sym := c.current.resolve(e.Name)
		if sym == nil {
			c.errorf(e.NamePos, "undefined name %q", e.Name)
			return types.Invalid
		}
		c.info.Uses[e] = sym
		if sym.IsFunc {
			return sym.Type
		}
		return sym.Type
	case *ast.CallExpr:
		return c.checkCall(e)
	case *ast.UnaryExpr:
		ot := c.checkExpr(e.Operand)
		switch e.Op {
		case token.Minus:
			if ot != types.Number && ot != types.Float && ot != types.Invalid {
				c.errorf(e.OpPos, "unary - requires numeric, got %s", types.Format(ot))
			}
			if ot == types.Float {
				return types.Float
			}
			return types.Number
		case token.Bang:
			if ot != types.Bool && ot != types.Invalid {
				c.errorf(e.OpPos, "unary ! requires bool, got %s", types.Format(ot))
			}
			return types.Bool
		}
		c.errorf(e.OpPos, "invalid unary operator %s", e.Op)
		return types.Invalid
	case *ast.BinaryExpr:
		return c.checkBinary(e)
	case *ast.RangeExpr:
		c.errorf(e.OpPos, "range expression is only allowed as a for-in iterator")
		return types.Invalid
	case *ast.ArrayLit:
		return c.checkArrayLit(e)
	case *ast.IndexExpr:
		return c.checkIndexExpr(e)
	case *ast.RecordLit:
		return c.checkRecordLit(e)
	case *ast.FieldExpr:
		return c.checkFieldExpr(e)
	case *ast.CoalesceExpr:
		return c.checkCoalesce(e)
	case *ast.UnwrapExpr:
		return c.checkUnwrap(e)
	case *ast.TryExpr:
		return c.checkTry(e)
	}
	c.errorf(e.Pos(), "unhandled expression type %T", e)
	return types.Invalid
}

// checkTry validates `expr?`. Operand must be a Result-shaped sum (variants
// `Ok{value: T}` and `Err{error: E}`), inside a function whose return type
// is also a Result-shaped sum sharing the same Err type. Result is T.
func (c *Checker) checkTry(e *ast.TryExpr) types.Type {
	ot := c.checkExpr(e.Operand)
	if ot == types.Invalid {
		return types.Invalid
	}
	sum, okShape := ot.(*types.Sum)
	if !okShape {
		c.errorf(e.OpPos, "? requires a Result-shaped sum, got %s", types.Format(ot))
		return types.Invalid
	}
	okV, errV, why := resultShape(sum)
	if okV == nil {
		c.errorf(e.OpPos, "? requires a Result-shaped sum (Ok{value: T} | Err{error: E}); %s is not (%s)",
			types.Format(ot), why)
		return types.Invalid
	}
	if c.currentRet == nil {
		c.errorf(e.OpPos, "? is only valid inside a function body")
		return okV.Fields[0].Type
	}
	retSum, ok := c.currentRet.(*types.Sum)
	if !ok {
		c.errorf(e.OpPos,
			"? requires the enclosing function to return a Result-shaped sum, got %s",
			types.Format(c.currentRet))
		return okV.Fields[0].Type
	}
	_, retErrV, retWhy := resultShape(retSum)
	if retErrV == nil {
		c.errorf(e.OpPos,
			"function's return type %s is not Result-shaped (%s); cannot use ? here",
			types.Format(retSum), retWhy)
		return okV.Fields[0].Type
	}
	if !types.Equal(errV.Fields[0].Type, retErrV.Fields[0].Type) {
		c.errorf(e.OpPos,
			"? Err type mismatch: operand carries %s, function returns %s",
			types.Format(errV.Fields[0].Type), types.Format(retErrV.Fields[0].Type))
	}
	return okV.Fields[0].Type
}

// resultShape inspects a sum and returns its Ok/Err variants when the sum
// matches `Ok{value: T} | Err{error: E}` exactly. The third return value is
// a short reason string suitable for error messages when the shape doesn't
// match (e.g. "missing variant Err"). When ok and err are both non-nil the
// reason is empty.
func resultShape(s *types.Sum) (ok, err *types.Variant, reason string) {
	if len(s.Variants) != 2 {
		return nil, nil, "must have exactly two variants"
	}
	ok = s.LookupVariant("Ok")
	err = s.LookupVariant("Err")
	if ok == nil {
		return nil, nil, "missing variant Ok"
	}
	if err == nil {
		return nil, nil, "missing variant Err"
	}
	if len(ok.Fields) != 1 || ok.Fields[0].Name != "value" {
		return nil, nil, "Ok variant must have a single field named `value`"
	}
	if len(err.Fields) != 1 || err.Fields[0].Name != "error" {
		return nil, nil, "Err variant must have a single field named `error`"
	}
	return ok, err, ""
}

func (c *Checker) checkRecordLit(e *ast.RecordLit) types.Type {
	// Variant construction: `Foo{...}` where Foo is a sum-variant in the
	// current module's namespace. Type-check the fields against the
	// variant's declared field shape and yield the parent sum's type.
	if env := c.envs[c.currentMod]; env != nil {
		if sum := env.variantOf[e.TypeName]; sum != nil {
			return c.checkVariantLit(e, sum)
		}
	}
	resolved := c.resolveTypeName(e.TypeName)
	if resolved == nil {
		c.errorf(e.NamePos, "unknown type %q", e.TypeName)
		for _, f := range e.Fields {
			c.checkExpr(f.Value)
		}
		return types.Invalid
	}
	if _, isSum := resolved.(*types.Sum); isSum {
		c.errorf(e.NamePos,
			"cannot construct sum type %q directly; use one of its variants",
			e.TypeName)
		for _, f := range e.Fields {
			c.checkExpr(f.Value)
		}
		return types.Invalid
	}
	rec, ok := resolved.(*types.Record)
	if !ok {
		c.errorf(e.NamePos, "%q is not a record type", e.TypeName)
		return types.Invalid
	}
	seen := map[string]bool{}
	for _, init := range e.Fields {
		if seen[init.Name] {
			c.errorf(init.NamePos, "duplicate field %q in record literal", init.Name)
			c.checkExpr(init.Value)
			continue
		}
		seen[init.Name] = true
		f := rec.Lookup(init.Name)
		if f == nil {
			c.errorf(init.NamePos, "record %q has no field %q", rec.Name, init.Name)
			c.checkExpr(init.Value)
			continue
		}
		got := c.checkExpr(init.Value)
		if got != types.Invalid && f.Type != types.Invalid && !types.IsAssignable(got, f.Type) {
			c.errorf(init.Value.Pos(),
				"field %q: expected %s, got %s",
				init.Name, types.Format(f.Type), types.Format(got))
		}
	}
	for _, f := range rec.Fields {
		if !seen[f.Name] {
			c.errorf(e.LBrace, "record literal is missing field %q", f.Name)
		}
	}
	return rec
}

// checkVariantLit type-checks `Foo{a: ..., b: ...}` as the construction of
// variant `Foo` of the given sum. Fields are matched by name; missing ones
// are reported. The result is the parent sum type so an assignment to a
// `: Shape` annotation passes.
func (c *Checker) checkVariantLit(e *ast.RecordLit, sum *types.Sum) types.Type {
	v := sum.LookupVariant(e.TypeName)
	if v == nil {
		c.errorf(e.NamePos, "variant %q not found in sum %q", e.TypeName, sum.Name)
		return types.Invalid
	}
	if len(v.Fields) == 0 {
		c.errorf(e.NamePos,
			"variant %q is a unit variant; use bare `%s` (no braces)",
			v.Name, v.Name)
		for _, f := range e.Fields {
			c.checkExpr(f.Value)
		}
		return sum
	}
	seen := map[string]bool{}
	for _, init := range e.Fields {
		if seen[init.Name] {
			c.errorf(init.NamePos, "duplicate field %q in variant literal", init.Name)
			c.checkExpr(init.Value)
			continue
		}
		seen[init.Name] = true
		var fld *types.Field
		for i := range v.Fields {
			if v.Fields[i].Name == init.Name {
				fld = &v.Fields[i]
				break
			}
		}
		if fld == nil {
			c.errorf(init.NamePos, "variant %q has no field %q", v.Name, init.Name)
			c.checkExpr(init.Value)
			continue
		}
		got := c.checkExpr(init.Value)
		if got != types.Invalid && fld.Type != types.Invalid && !types.IsAssignable(got, fld.Type) {
			c.errorf(init.Value.Pos(),
				"field %q: expected %s, got %s",
				init.Name, types.Format(fld.Type), types.Format(got))
		}
	}
	for _, f := range v.Fields {
		if !seen[f.Name] {
			c.errorf(e.LBrace, "variant %q is missing field %q", v.Name, f.Name)
		}
	}
	return sum
}

func (c *Checker) checkFieldExpr(e *ast.FieldExpr) types.Type {
	tt := c.checkExpr(e.Target)
	rec, ok := tt.(*types.Record)
	if !ok {
		if tt != types.Invalid {
			c.errorf(e.DotPos, "field access requires a record, got %s", types.Format(tt))
		}
		return types.Invalid
	}
	f := rec.Lookup(e.Name)
	if f == nil {
		c.errorf(e.NamePos, "record %q has no field %q", rec.Name, e.Name)
		return types.Invalid
	}
	return f.Type
}

func (c *Checker) checkCoalesce(e *ast.CoalesceExpr) types.Type {
	lt := c.checkExpr(e.Lhs)
	rt := c.checkExpr(e.Rhs)
	if lt == types.Invalid || rt == types.Invalid {
		return types.Invalid
	}
	opt, ok := lt.(*types.Optional)
	if !ok {
		c.errorf(e.OpPos, "?? requires an optional left-hand side, got %s", types.Format(lt))
		return types.Invalid
	}
	// Right side must be assignable to the underlying T, or to the optional itself
	// (chained nullable defaults).
	if rt == types.Null {
		c.errorf(e.Rhs.Pos(), "?? right-hand side cannot be null")
		return types.Invalid
	}
	if !types.Equal(rt, opt.Elem) && !types.Equal(rt, lt) {
		c.errorf(e.OpPos, "?? type mismatch: left is %s, right is %s",
			types.Format(lt), types.Format(rt))
		return types.Invalid
	}
	// If the right side is itself optional (T?), result stays optional;
	// otherwise the result is non-optional T.
	if types.IsOptional(rt) {
		return lt
	}
	return opt.Elem
}

func (c *Checker) checkUnwrap(e *ast.UnwrapExpr) types.Type {
	t := c.checkExpr(e.Operand)
	if t == types.Invalid {
		return types.Invalid
	}
	opt, ok := t.(*types.Optional)
	if !ok {
		c.errorf(e.OpPos, "! requires an optional operand, got %s", types.Format(t))
		return types.Invalid
	}
	return opt.Elem
}

func (c *Checker) checkArrayLit(e *ast.ArrayLit) types.Type {
	if len(e.Elems) == 0 {
		c.errorf(e.LBracket, "cannot infer type of empty array literal; add an annotation like `: T[]`")
		return types.Invalid
	}
	first := c.checkExpr(e.Elems[0])
	if first == types.Void {
		c.errorf(e.Elems[0].Pos(), "array element cannot be void")
		first = types.Invalid
	}
	for i := 1; i < len(e.Elems); i++ {
		got := c.checkExpr(e.Elems[i])
		if got != types.Invalid && first != types.Invalid && !types.Equal(got, first) {
			c.errorf(e.Elems[i].Pos(), "array element %d: expected %s, got %s",
				i+1, types.Format(first), types.Format(got))
		}
	}
	return &types.Array{Elem: first}
}

func (c *Checker) checkIndexExpr(e *ast.IndexExpr) types.Type {
	tt := c.checkExpr(e.Target)
	it := c.checkExpr(e.Index)
	if it != types.Number && it != types.Invalid {
		c.errorf(e.Index.Pos(), "index must be number, got %s", types.Format(it))
	}
	arr, ok := tt.(*types.Array)
	if !ok {
		if tt != types.Invalid {
			c.errorf(e.LBracket, "indexing requires an array, got %s", types.Format(tt))
		}
		return types.Invalid
	}
	return arr.Elem
}

func (c *Checker) checkBinary(e *ast.BinaryExpr) types.Type {
	lt := c.checkExpr(e.Lhs)
	rt := c.checkExpr(e.Rhs)
	if lt == types.Invalid || rt == types.Invalid {
		return types.Invalid
	}
	switch e.Op {
	case token.Plus:
		if isNumeric(lt) && isNumeric(rt) {
			return promoteNumeric(lt, rt)
		}
		if lt == types.String && rt == types.String {
			return types.String
		}
		c.errorf(e.OpPos, "+ requires both operands to be numeric or both to be string (got %s and %s)", types.Format(lt), types.Format(rt))
		return types.Invalid
	case token.Minus, token.Star, token.Slash:
		if !isNumeric(lt) || !isNumeric(rt) {
			c.errorf(e.OpPos, "%s requires numeric operands (got %s and %s)", e.Op, types.Format(lt), types.Format(rt))
			return types.Invalid
		}
		return promoteNumeric(lt, rt)
	case token.Percent:
		// Modulo is integer-only; promoting to float would silently change
		// behaviour from the user's intent.
		if lt != types.Number || rt != types.Number {
			c.errorf(e.OpPos, "%s requires number operands (got %s and %s); use intOf() to truncate floats first", e.Op, types.Format(lt), types.Format(rt))
			return types.Invalid
		}
		return types.Number
	case token.Eq, token.Neq:
		// Null comparisons are the only operation v0 directly supports on
		// optional values: `x == null`, `x != null`, mirrored.
		if lt == types.Null && rt == types.Null {
			return types.Bool
		}
		if lt == types.Null {
			if !types.IsOptional(rt) {
				c.errorf(e.OpPos, "cannot compare %s to null (only optional types are nullable)", types.Format(rt))
			}
			return types.Bool
		}
		if rt == types.Null {
			if !types.IsOptional(lt) {
				c.errorf(e.OpPos, "cannot compare %s to null (only optional types are nullable)", types.Format(lt))
			}
			return types.Bool
		}
		if types.IsOptional(lt) || types.IsOptional(rt) {
			c.errorf(e.OpPos, "cannot use %s on optional values directly; use `??` or `!` first, or compare against null", e.Op)
			return types.Bool
		}
		// Allow cross-type numeric equality (number == float widens).
		if isNumeric(lt) && isNumeric(rt) {
			return types.Bool
		}
		if !types.Equal(lt, rt) {
			c.errorf(e.OpPos, "%s requires operands of the same type (got %s and %s)", e.Op, types.Format(lt), types.Format(rt))
			return types.Bool
		}
		return types.Bool
	case token.Lt, token.Lte, token.Gt, token.Gte:
		if isNumeric(lt) && isNumeric(rt) {
			return types.Bool
		}
		if lt == types.String && rt == types.String {
			return types.Bool
		}
		c.errorf(e.OpPos, "%s requires numeric or string operands (got %s and %s)", e.Op, types.Format(lt), types.Format(rt))
		return types.Bool
	case token.AndAnd, token.OrOr:
		if lt != types.Bool || rt != types.Bool {
			c.errorf(e.OpPos, "%s requires bool operands (got %s and %s)", e.Op, types.Format(lt), types.Format(rt))
		}
		return types.Bool
	}
	c.errorf(e.OpPos, "invalid binary operator %s", e.Op)
	return types.Invalid
}

func (c *Checker) checkCall(e *ast.CallExpr) types.Type {
	id, ok := e.Callee.(*ast.Ident)
	if !ok {
		c.errorf(e.LParenPos, "only direct function calls are supported")
		return types.Invalid
	}
	sym := c.current.resolve(id.Name)
	if sym == nil {
		c.errorf(id.NamePos, "undefined function %q", id.Name)
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return types.Invalid
	}
	c.info.Uses[id] = sym
	// `map`, `filter`, and `reduce` are higher-order builtins. We type-check
	// them by hand because v0 has no generics.
	if sym.IsBuiltin {
		switch sym.Name {
		case "map":
			return c.checkMapCall(e)
		case "filter":
			return c.checkFilterCall(e)
		case "reduce":
			return c.checkReduceCall(e)
		case "assertEq", "assertNe", "check", "fail", "skip":
			return c.checkTestBuiltinCall(e, sym)
		case "mockExec", "mockFetch", "mockEnv", "mockReadFile",
			"mockNow", "mockArgs", "mockReadStdin",
			"mockExecCalls", "mockFetchCalls", "mockReadFileCalls":
			// All mock builtins must be invoked from inside a `test`
			// body. Type-checking falls through to the regular path
			// once we've enforced the scope rule.
			if !c.inTest {
				c.errorf(e.LParenPos, "%s may only be called inside a `test \"...\" { ... }` body", sym.Name)
			}
		}
	}
	// `str` is polymorphic over the numeric primitives and bool. Optional
	// support could be added later, but for now we keep it strict.
	if sym.IsBuiltin && sym.Name == "str" {
		if len(e.Args) != 1 {
			c.errorf(e.LParenPos, "str expects 1 argument, got %d", len(e.Args))
		}
		for _, a := range e.Args {
			at := c.checkExpr(a)
			if at == types.Number || at == types.Float || at == types.Bool || at == types.Invalid {
				continue
			}
			c.errorf(a.Pos(), "str requires a number, float, or bool, got %s", types.Format(at))
		}
		return types.String
	}
	// `len` is polymorphic over string and array types.
	if sym.IsBuiltin && sym.Name == "len" {
		if len(e.Args) != 1 {
			c.errorf(e.LParenPos, "len expects 1 argument, got %d", len(e.Args))
		}
		for _, a := range e.Args {
			at := c.checkExpr(a)
			if _, ok := at.(*types.Array); ok {
				continue
			}
			if at == types.String || at == types.Invalid {
				continue
			}
			c.errorf(a.Pos(), "len requires a string or array, got %s", types.Format(at))
		}
		return types.Number
	}
	ft, ok := sym.Type.(*types.Func)
	if !ok {
		c.errorf(id.NamePos, "%q is not a function", id.Name)
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return types.Invalid
	}
	if len(e.Args) != len(ft.Params) {
		c.errorf(e.LParenPos, "function %q expects %d argument(s), got %d", id.Name, len(ft.Params), len(e.Args))
	}
	n := len(e.Args)
	if len(ft.Params) < n {
		n = len(ft.Params)
	}
	for i := 0; i < n; i++ {
		at := c.checkExpr(e.Args[i])
		if at != types.Invalid && !types.IsAssignable(at, ft.Params[i]) {
			c.errorf(e.Args[i].Pos(),
				"argument %d to %q: expected %s, got %s",
				i+1, id.Name, types.Format(ft.Params[i]), types.Format(at))
		}
	}
	for i := n; i < len(e.Args); i++ {
		c.checkExpr(e.Args[i])
	}
	return ft.Result
}

// isNumeric reports whether t is one of the numeric primitives (number/float).
func isNumeric(t types.Type) bool { return t == types.Number || t == types.Float }

// promoteNumeric returns Float if either operand is Float, otherwise Number.
// Used when checking arithmetic on mixed integer/float operands.
func promoteNumeric(a, b types.Type) types.Type {
	if a == types.Float || b == types.Float {
		return types.Float
	}
	return types.Number
}

// checkMapCall validates `map(arr: T[], f: func(T): U): U[]` and returns U[].
func (c *Checker) checkMapCall(e *ast.CallExpr) types.Type {
	if len(e.Args) != 2 {
		c.errorf(e.LParenPos, "map expects 2 arguments (array, function), got %d", len(e.Args))
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return types.Invalid
	}
	at := c.checkExpr(e.Args[0])
	ft := c.checkExpr(e.Args[1])
	arr, ok := at.(*types.Array)
	if !ok {
		c.errorf(e.Args[0].Pos(), "map: first argument must be an array, got %s", types.Format(at))
		return types.Invalid
	}
	fn, ok := ft.(*types.Func)
	if !ok {
		c.errorf(e.Args[1].Pos(), "map: second argument must be a function, got %s", types.Format(ft))
		return types.Invalid
	}
	if len(fn.Params) != 1 || !types.Equal(fn.Params[0], arr.Elem) {
		c.errorf(e.Args[1].Pos(), "map: function must take one parameter of type %s", types.Format(arr.Elem))
		return types.Invalid
	}
	if !c.isAllowedRecordFieldType(fn.Result) && fn.Result != types.Float {
		// Same scalar restriction as record fields, plus float.
		c.errorf(e.Args[1].Pos(), "map: function result must be a primitive, got %s", types.Format(fn.Result))
		return types.Invalid
	}
	return &types.Array{Elem: fn.Result}
}

// checkFilterCall validates `filter(arr: T[], pred: func(T): bool): T[]`.
func (c *Checker) checkFilterCall(e *ast.CallExpr) types.Type {
	if len(e.Args) != 2 {
		c.errorf(e.LParenPos, "filter expects 2 arguments (array, predicate), got %d", len(e.Args))
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return types.Invalid
	}
	at := c.checkExpr(e.Args[0])
	ft := c.checkExpr(e.Args[1])
	arr, ok := at.(*types.Array)
	if !ok {
		c.errorf(e.Args[0].Pos(), "filter: first argument must be an array, got %s", types.Format(at))
		return types.Invalid
	}
	fn, ok := ft.(*types.Func)
	if !ok {
		c.errorf(e.Args[1].Pos(), "filter: second argument must be a function, got %s", types.Format(ft))
		return types.Invalid
	}
	if len(fn.Params) != 1 || !types.Equal(fn.Params[0], arr.Elem) {
		c.errorf(e.Args[1].Pos(), "filter: predicate must take one parameter of type %s", types.Format(arr.Elem))
		return types.Invalid
	}
	if fn.Result != types.Bool {
		c.errorf(e.Args[1].Pos(), "filter: predicate must return bool, got %s", types.Format(fn.Result))
		return types.Invalid
	}
	return arr
}

// checkTestBuiltinCall handles the assertion/test-control builtins. Common
// rule: they may only appear inside a `test "..." { ... }` body, not in
// regular functions or globals. assertEq/assertNe are also polymorphic over
// scalar primitives (string, number, bool, float).
func (c *Checker) checkTestBuiltinCall(e *ast.CallExpr, sym *Symbol) types.Type {
	if !c.inTest {
		c.errorf(e.LParenPos, "%s may only be called inside a `test \"...\" { ... }` body", sym.Name)
	}
	switch sym.Name {
	case "assertEq", "assertNe":
		if len(e.Args) != 2 {
			c.errorf(e.LParenPos, "%s expects 2 arguments, got %d", sym.Name, len(e.Args))
			for _, a := range e.Args {
				c.checkExpr(a)
			}
			return types.Void
		}
		at := c.checkExpr(e.Args[0])
		bt := c.checkExpr(e.Args[1])
		if at == types.Invalid || bt == types.Invalid {
			return types.Void
		}
		if !isAssertableScalar(at) {
			c.errorf(e.Args[0].Pos(), "%s: argument 1 must be string, number, float, or bool, got %s",
				sym.Name, types.Format(at))
			return types.Void
		}
		// Allow cross-numeric comparison: number vs float widens to float at
		// runtime via awk; everything else must match exactly.
		if !(isNumeric(at) && isNumeric(bt)) && !types.Equal(at, bt) {
			c.errorf(e.LParenPos, "%s: arguments must have the same type, got %s and %s",
				sym.Name, types.Format(at), types.Format(bt))
		}
		return types.Void
	case "check":
		if len(e.Args) != 1 {
			c.errorf(e.LParenPos, "check expects 1 argument, got %d", len(e.Args))
		}
		for _, a := range e.Args {
			at := c.checkExpr(a)
			if at != types.Invalid && at != types.Bool {
				c.errorf(a.Pos(), "check: argument must be bool, got %s", types.Format(at))
			}
		}
		return types.Void
	case "fail", "skip":
		if len(e.Args) != 1 {
			c.errorf(e.LParenPos, "%s expects 1 argument, got %d", sym.Name, len(e.Args))
		}
		for _, a := range e.Args {
			at := c.checkExpr(a)
			if at != types.Invalid && at != types.String {
				c.errorf(a.Pos(), "%s: argument must be string, got %s", sym.Name, types.Format(at))
			}
		}
		return types.Void
	}
	return types.Void
}

func isAssertableScalar(t types.Type) bool {
	switch t {
	case types.String, types.Number, types.Bool, types.Float:
		return true
	}
	return false
}

// checkReduceCall validates `reduce(arr: T[], init: U, f: func(U, T): U): U`.
func (c *Checker) checkReduceCall(e *ast.CallExpr) types.Type {
	if len(e.Args) != 3 {
		c.errorf(e.LParenPos, "reduce expects 3 arguments (array, initial, function), got %d", len(e.Args))
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return types.Invalid
	}
	at := c.checkExpr(e.Args[0])
	it := c.checkExpr(e.Args[1])
	ft := c.checkExpr(e.Args[2])
	arr, ok := at.(*types.Array)
	if !ok {
		c.errorf(e.Args[0].Pos(), "reduce: first argument must be an array, got %s", types.Format(at))
		return types.Invalid
	}
	fn, ok := ft.(*types.Func)
	if !ok {
		c.errorf(e.Args[2].Pos(), "reduce: third argument must be a function, got %s", types.Format(ft))
		return types.Invalid
	}
	if len(fn.Params) != 2 {
		c.errorf(e.Args[2].Pos(), "reduce: function must take two parameters (accumulator, element)")
		return types.Invalid
	}
	if !types.IsAssignable(it, fn.Params[0]) {
		c.errorf(e.Args[1].Pos(), "reduce: initial value type %s does not match accumulator parameter type %s",
			types.Format(it), types.Format(fn.Params[0]))
	}
	if !types.Equal(fn.Params[1], arr.Elem) {
		c.errorf(e.Args[2].Pos(), "reduce: second function parameter must be element type %s, got %s",
			types.Format(arr.Elem), types.Format(fn.Params[1]))
	}
	if !types.Equal(fn.Result, fn.Params[0]) {
		c.errorf(e.Args[2].Pos(), "reduce: function result type must equal accumulator type %s",
			types.Format(fn.Params[0]))
	}
	return fn.Result
}

// builtins returns the symbol set seeded into the global scope. These do not
// reference any predeclared user-record types.
func builtins() []*Symbol {
	str := types.String
	num := types.Number
	flt := types.Float
	bln := types.Bool
	void := types.Void
	stringArr := &types.Array{Elem: types.String}
	mk := func(name string, params []types.Type, result types.Type) *Symbol {
		return &Symbol{Name: name, IsFunc: true, IsBuiltin: true,
			Type: &types.Func{Params: params, Result: result}}
	}
	return []*Symbol{
		mk("echo", []types.Type{str}, void),
		mk("eprint", []types.Type{str}, void),
		mk("str", []types.Type{num}, str),
		mk("num", []types.Type{str}, num),
		mk("len", []types.Type{str}, num),
		mk("env", []types.Type{str}, &types.Optional{Elem: str}),
		mk("exit", []types.Type{num}, void),
		mk("split", []types.Type{str, str}, stringArr),
		mk("join", []types.Type{stringArr, str}, str),
		mk("trim", []types.Type{str}, str),
		mk("upper", []types.Type{str}, str),
		mk("lower", []types.Type{str}, str),
		mk("replace", []types.Type{str, str, str}, str),
		mk("contains", []types.Type{str, str}, bln),
		mk("startsWith", []types.Type{str, str}, bln),
		mk("endsWith", []types.Type{str, str}, bln),
		mk("slice", []types.Type{str, num, num}, str),

		// File I/O.
		mk("readFile", []types.Type{str}, str),
		mk("writeFile", []types.Type{str, str}, void),
		mk("appendFile", []types.Type{str, str}, void),
		mk("removeFile", []types.Type{str}, void),
		mk("mkdir", []types.Type{str}, void),
		mk("listDir", []types.Type{str}, stringArr),
		mk("exists", []types.Type{str}, bln),
		mk("isFile", []types.Type{str}, bln),
		mk("isDir", []types.Type{str}, bln),
		mk("readStdin", nil, str),

		// Path manipulation (no I/O).
		mk("pathJoin", []types.Type{str, str}, str),
		mk("basename", []types.Type{str}, str),
		mk("dirname", []types.Type{str}, str),
		mk("extname", []types.Type{str}, str),

		// Process / time.
		mk("args", nil, stringArr),
		mk("now", nil, num),
		mk("sleep", []types.Type{num}, void),
		mk("formatTime", []types.Type{num, str}, str),

		// execTimeout depends on the predeclared Process record below; see
		// builtinsWithTypes.

		// JSON. Implementation shells out to jq, which the v0 stdlib treats
		// as a hard runtime dependency (must be on PATH at script-run time).
		mk("jsonGet", []types.Type{str, str}, &types.Optional{Elem: str}),
		mk("jsonHas", []types.Type{str, str}, bln),
		mk("jsonArray", []types.Type{str, str}, stringArr),
		mk("jsonEscape", []types.Type{str}, str),

		// Regex (POSIX ERE via awk).
		mk("regexMatch", []types.Type{str, str}, bln),
		mk("regexFind", []types.Type{str, str}, &types.Optional{Elem: str}),
		mk("regexFindAll", []types.Type{str, str}, stringArr),
		mk("regexReplace", []types.Type{str, str, str}, str),

		// Float helpers. `floatOf` widens an int; `intOf` truncates toward
		// zero. The `floor`/`ceil`/`round` family produces ints from floats,
		// matching the common ergonomic shape.
		mk("floatOf", []types.Type{num}, flt),
		mk("intOf", []types.Type{flt}, num),
		mk("parseFloat", []types.Type{str}, &types.Optional{Elem: flt}),
		mk("formatFloat", []types.Type{flt, num}, str),
		mk("floor", []types.Type{flt}, num),
		mk("ceil", []types.Type{flt}, num),
		mk("round", []types.Type{flt}, num),

		// Higher-order builtins. The signatures registered here are
		// placeholders — the actual type checking is done by the
		// checkMapCall / checkFilterCall / checkReduceCall handlers above,
		// which need to dispatch on the array element type.
		mk("map", []types.Type{stringArr, str}, stringArr),
		mk("filter", []types.Type{stringArr, str}, stringArr),
		mk("reduce", []types.Type{stringArr, str, str}, str),

		// Testing builtins. assertEq/assertNe are polymorphic over the
		// scalar primitives (string, number, bool, float); the placeholder
		// signature here is overridden by checkAssertEqCall. The remaining
		// three (check, fail, skip) are mono-typed and use this signature
		// directly. All five require the call to appear inside a test
		// body — enforced by the checker.
		mk("assertEq", []types.Type{str, str}, void),
		mk("assertNe", []types.Type{str, str}, void),
		mk("check", []types.Type{bln}, void),
		mk("fail", []types.Type{str}, void),
		mk("skip", []types.Type{str}, void),

		// Mock builtins — only legal inside `test "..." { ... }` bodies.
		// Each mockX matches against future calls to the corresponding real
		// builtin and either returns a canned response or, in strict mode
		// (the default), fails the test on an unmatched real call.
		//
		// `mockEnv` falls through to the real environment when no mock
		// matches the requested name; it is the only mock that does not
		// trigger strict mode, since `env()` already has a "missing"
		// shape.
		mk("mockEnv", []types.Type{str, &types.Optional{Elem: str}}, void),
		mk("mockReadFile", []types.Type{str, str}, void),
		mk("mockReadFileCalls", nil, stringArr),
		mk("mockNow", []types.Type{num}, void),
		mk("mockArgs", []types.Type{stringArr}, void),
		mk("mockReadStdin", []types.Type{str}, void),
		mk("mockExecCalls", nil, stringArr),
		mk("mockFetchCalls", nil, stringArr),
	}
}

// builtinTypes returns the predeclared record types available without import.
func builtinTypes() []*types.Record {
	return []*types.Record{
		{
			Name: "Response",
			Fields: []types.Field{
				{Name: "status", Type: types.Number},
				{Name: "ok", Type: types.Bool},
				{Name: "body", Type: types.String},
				{Name: "headers", Type: types.String},
			},
		},
		{
			Name: "Process",
			Fields: []types.Field{
				{Name: "code", Type: types.Number},
				{Name: "ok", Type: types.Bool},
				{Name: "stdout", Type: types.String},
				{Name: "stderr", Type: types.String},
			},
		},
		{
			Name: "FileInfo",
			Fields: []types.Field{
				{Name: "exists", Type: types.Bool},
				{Name: "isFile", Type: types.Bool},
				{Name: "isDir", Type: types.Bool},
				{Name: "size", Type: types.Number},
				{Name: "mtime", Type: types.Number},
				{Name: "mode", Type: types.String},
			},
		},
		{
			Name: "PathParts",
			Fields: []types.Field{
				{Name: "dir", Type: types.String},
				{Name: "base", Type: types.String},
				{Name: "name", Type: types.String},
				{Name: "ext", Type: types.String},
			},
		},
	}
}

// builtinsWithTypes returns builtins whose signatures reference predeclared types.
func builtinsWithTypes(typeNames map[string]types.Type) []*Symbol {
	response := typeNames["Response"]
	process := typeNames["Process"]
	fileInfo := typeNames["FileInfo"]
	pathParts := typeNames["PathParts"]
	return []*Symbol{
		{Name: "fetch", IsFunc: true, IsBuiltin: true, Type: &types.Func{Params: []types.Type{types.String}, Result: response}},
		{Name: "exec", IsFunc: true, IsBuiltin: true, Type: &types.Func{Params: []types.Type{types.String}, Result: process}},
		{Name: "execTimeout", IsFunc: true, IsBuiltin: true, Type: &types.Func{Params: []types.Type{types.String, types.Number}, Result: process}},
		{Name: "stat", IsFunc: true, IsBuiltin: true, Type: &types.Func{Params: []types.Type{types.String}, Result: fileInfo}},
		{Name: "parsePath", IsFunc: true, IsBuiltin: true, Type: &types.Func{Params: []types.Type{types.String}, Result: pathParts}},

		// Mock builtins for exec/fetch — pattern is a regex, response is a
		// pre-built Process / Response. Test-only (checker rejects calls
		// outside a `test "..." { ... }` body).
		{Name: "mockExec", IsFunc: true, IsBuiltin: true, Type: &types.Func{Params: []types.Type{types.String, process}, Result: types.Void}},
		{Name: "mockFetch", IsFunc: true, IsBuiltin: true, Type: &types.Func{Params: []types.Type{types.String, response}, Result: types.Void}},
	}
}
