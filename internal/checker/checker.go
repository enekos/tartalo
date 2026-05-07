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
	"strconv"
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
	// GenericInsts records the inferred type arguments for each call to a
	// generic function. The slice positions match the FuncDecl's TypeParams
	// order. Nil/absent for calls to monomorphic functions. Codegen consults
	// this map when monomorphizing.
	GenericInsts map[*ast.CallExpr][]types.Type
}

func newTypeInfo() *TypeInfo {
	// Empirical sizes from the typical Tartalo program: many more typed
	// expressions than ident uses, and a small handful of top-level decls and
	// assignments. Pre-sizing avoids the doubling-rehash hop most maps hit
	// while filling.
	return &TypeInfo{
		Types:        make(map[ast.Expr]types.Type, 32),
		Uses:         make(map[*ast.Ident]*Symbol, 16),
		Decls:        make(map[string]*Symbol, 8),
		Assigns:      make(map[*ast.AssignStmt]*Symbol, 4),
		GenericInsts: nil, // lazily allocated when a generic call is recorded
	}
}

// scope is a lexical name lookup chain.
type scope struct {
	parent *scope
	syms   map[string]*Symbol
}

func newScope(parent *scope) *scope {
	// syms is allocated lazily by define(). Many scopes (empty `if` branches,
	// for-body scopes that only borrow names) never define a symbol; skipping
	// the map allocation in those cases is a meaningful checker win.
	return &scope{parent: parent}
}

func (s *scope) define(sym *Symbol) bool {
	if s.syms == nil {
		s.syms = make(map[string]*Symbol, 4)
		s.syms[sym.Name] = sym
		return true
	}
	if _, exists := s.syms[sym.Name]; exists {
		return false
	}
	s.syms[sym.Name] = sym
	return true
}

func (s *scope) resolve(name string) *Symbol {
	for cur := s; cur != nil; cur = cur.parent {
		if cur.syms != nil {
			if sym, ok := cur.syms[name]; ok {
				return sym
			}
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

	// taskOuter is non-nil while checking the body of a `task { ... }` block.
	// Its value is the scope that immediately encloses the task body, so
	// `isLocalToTask` can tell whether a resolved symbol was declared inside
	// the task (assignable) or outside (read-only). Nil outside any task.
	taskOuter *scope

	// inTest is true while checking the body of a `test "..." { ... }` decl.
	// Assertion builtins (assertEq, fail, skip, ...) require this to be true.
	// Helpers called from tests cannot use them directly — pass a bool back
	// and use `check(...)` at the call site.
	inTest bool

	narrows []map[string]types.Type

	// expectedType carries an outer context type into a call expression — used
	// today only by readCsv to recover the row-record type from the LHS of a
	// `let xs: T[] = readCsv(...)`. Set by checkVarDecl / checkAssignStmt
	// before recursing into the initializer; nil otherwise.
	expectedType types.Type

	// genericScope maps a type-parameter name to its *types.TypeVar while
	// resolving the signature or body of a generic function. Cleared between
	// functions. resolveTypeExpr consults this map before falling back to the
	// module's typeNames or the predeclared list.
	genericScope map[string]*types.TypeVar
}

// sharedPredeclTypes and sharedBuiltinScope are built once at package init.
// They are read-only after init: modules never write to them (their own type
// names go into env.typeNames; their own values into env.scope, whose parent
// chain bottoms out at sharedBuiltinScope but never mutates it). Sharing across
// every Checker instance saves ~78% of the per-construction allocation cost.
var (
	sharedPredeclTypes map[string]types.Type
	sharedBuiltinScope *scope
)

func init() {
	sharedPredeclTypes = make(map[string]types.Type, len(builtinTypes()))
	for _, r := range builtinTypes() {
		sharedPredeclTypes[r.Name] = r
	}
	sharedBuiltinScope = newScope(nil)
	for _, b := range builtins() {
		sharedBuiltinScope.define(b)
	}
	for _, b := range builtinsWithTypes(sharedPredeclTypes) {
		sharedBuiltinScope.define(b)
	}
}

func New() *Checker {
	return &Checker{
		info:         newTypeInfo(),
		predeclTypes: sharedPredeclTypes,
		builtinScope: sharedBuiltinScope,
		envs:         map[*loader.Module]*moduleEnv{},
	}
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

// resolveTypeName walks a module's typeNames + the predeclared types. While
// checking a generic function, type-parameter names take precedence so that
// `T` inside `func f<T>(...): T` resolves to the function's *types.TypeVar
// rather than to a same-named record type from the surrounding module.
func (c *Checker) resolveTypeName(name string) types.Type {
	if c.genericScope != nil {
		if v, ok := c.genericScope[name]; ok {
			return v
		}
	}
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

	// Generic functions: allocate one *types.TypeVar per declared type
	// parameter and seed the genericScope so the param/result type expressions
	// resolve T to the same TypeVar throughout. The TypeVars themselves are
	// stored on the resulting *types.Func so checkFuncBody can re-establish
	// the same scope without re-allocating.
	var typeVars []*types.TypeVar
	if len(fd.TypeParams) > 0 {
		typeVars = make([]*types.TypeVar, len(fd.TypeParams))
		seen := map[string]bool{}
		for i, tp := range fd.TypeParams {
			if seen[tp.Name] {
				c.errorf(tp.NamePos, "duplicate type parameter %q", tp.Name)
			}
			seen[tp.Name] = true
			if _, isPrim := types.Lookup(tp.Name).(*types.Primitive); isPrim || types.Lookup(tp.Name) != nil {
				c.errorf(tp.NamePos, "type parameter %q shadows a primitive type", tp.Name)
			}
			if _, predeclared := c.predeclTypes[tp.Name]; predeclared {
				c.errorf(tp.NamePos, "type parameter %q shadows a predeclared type", tp.Name)
			}
			typeVars[i] = &types.TypeVar{Name: tp.Name}
		}
		c.genericScope = make(map[string]*types.TypeVar, len(typeVars))
		for i, tv := range typeVars {
			c.genericScope[fd.TypeParams[i].Name] = tv
		}
	}

	params := make([]types.Type, len(fd.Params))
	for i, p := range fd.Params {
		params[i] = c.resolveTypeExpr(p.TypeAnn)
	}
	ret := c.resolveTypeExpr(fd.Result)

	c.genericScope = nil

	sym := &Symbol{
		Name:     fd.Name,
		Type:     &types.Func{Params: params, Result: ret, TypeParams: typeVars},
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
	return "__m" + strconv.Itoa(m.ID) + "__" + name
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
	if fd.Kind == ast.FuncKindAgent && len(fd.Tools) > 0 {
		c.checkAgentToolList(fd, env)
	}
	saved := c.current
	savedRet := c.currentRet
	savedInTest := c.inTest
	savedGenScope := c.genericScope
	c.current = newScope(env.scope)
	c.currentRet = ft.Result
	c.inTest = false
	// Re-bind type-parameter names to the same *types.TypeVars used for the
	// signature so that any type annotation inside the body (e.g.
	// `let x: T = ...`) resolves to the same opaque type.
	if len(ft.TypeParams) > 0 {
		c.genericScope = make(map[string]*types.TypeVar, len(ft.TypeParams))
		for i, tv := range ft.TypeParams {
			c.genericScope[fd.TypeParams[i].Name] = tv
		}
	} else {
		c.genericScope = nil
	}
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
	c.genericScope = savedGenScope
}

// checkAgentToolList validates each name in an agent's `uses (...)` clause
// resolves to a declared tool in scope, and rejects duplicates. We reuse
// scope resolution so cross-module tool imports work without extra plumbing.
func (c *Checker) checkAgentToolList(fd *ast.FuncDecl, env *moduleEnv) {
	seen := map[string]bool{}
	for _, t := range fd.Tools {
		if seen[t] {
			c.errorf(fd.NamePos, "duplicate tool %q in agent %q uses clause", t, fd.Name)
			continue
		}
		seen[t] = true
		s := env.scope.resolve(t)
		if s == nil {
			c.errorf(fd.NamePos, "agent %q uses unknown tool %q", fd.Name, t)
			continue
		}
		if !s.IsFunc {
			c.errorf(fd.NamePos, "agent %q uses %q, which is not a tool", fd.Name, t)
		}
	}
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
		saved := c.expectedType
		c.expectedType = declared
		got := c.checkExpr(d.Value)
		c.expectedType = saved
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
		if c.taskOuter != nil && !c.isLocalToTask(sym) {
			c.errorf(s.NamePos, "cannot assign to outer-scope variable %q from inside a task", s.Name)
		}
		saved := c.expectedType
		c.expectedType = sym.Type
		got := c.checkExpr(s.Value)
		c.expectedType = saved
		if got != types.Invalid && sym.Type != types.Invalid && !types.IsAssignable(got, sym.Type) {
			c.errorf(s.Value.Pos(),
				"type mismatch: %q is %s, value is %s",
				s.Name, types.Format(sym.Type), types.Format(got))
		}
	case *ast.ReturnStmt:
		if c.taskOuter != nil {
			c.errorf(s.KwPos, "return is not allowed inside a task block")
		}
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
		thenNarrow := c.extractNarrowings(s.Cond, false)
		c.pushNarrow(thenNarrow)
		c.checkBlock(s.Then)
		c.popNarrow()
		if s.Else != nil {
			elseNarrow := c.extractNarrowings(s.Cond, true)
			c.pushNarrow(elseNarrow)
			c.checkBlock(s.Else)
			c.popNarrow()
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
		if c.taskOuter != nil {
			c.errorf(s.KwPos, "defer is not allowed inside a task block")
		}
		c.checkBlock(s.Body)
		if pos, has := firstReturnIn(s.Body); has {
			c.errorf(pos, "return is not allowed inside a defer block")
		}
	case *ast.ParallelStmt:
		c.checkParallel(s)
	case *ast.TaskStmt:
		c.errorf(s.KwPos, "task can only appear inside a parallel block")
		c.checkBlock(s.Body)
	case *ast.FieldAssignStmt:
		if c.taskOuter != nil {
			if root := rootIdent(s.Target); root != nil {
				if sym := c.current.resolve(root.Name); sym != nil && !c.isLocalToTask(sym) {
					c.errorf(s.NamePos, "cannot mutate field of outer-scope record %q from inside a task", root.Name)
				}
			}
		}
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

// checkParallel validates a `parallel { task { ... } ... }` block. The
// parser already restricts the body to TaskStmt children, but we re-check
// defensively in case a synthesised AST appears in tests. Each task body
// runs with c.taskOuter set, which makes nested return/defer/parallel and
// outer-scope writes errors (handled inline in checkStmt).
func (c *Checker) checkParallel(s *ast.ParallelStmt) {
	if c.currentRet == nil {
		c.errorf(s.KwPos, "parallel is only valid inside a function body")
	}
	if c.taskOuter != nil {
		c.errorf(s.KwPos, "parallel cannot be nested inside a task block")
		// Continue checking so further errors surface anyway.
	}
	for _, st := range s.Body.Stmts {
		ts, ok := st.(*ast.TaskStmt)
		if !ok {
			c.errorf(st.Pos(), "parallel block can only contain task { ... } statements")
			c.checkStmt(st)
			continue
		}
		savedOuter := c.taskOuter
		savedScope := c.current
		c.taskOuter = c.current
		c.current = newScope(savedScope)
		for _, bs := range ts.Body.Stmts {
			c.checkStmt(bs)
		}
		c.current = savedScope
		c.taskOuter = savedOuter
	}
}

// isLocalToTask reports whether sym was declared inside the current task
// body (between c.current and c.taskOuter, exclusive of taskOuter). Outer
// declarations are read-only inside a task because the sh backend runs each
// task in a subshell where mutations don't propagate back to the parent.
func (c *Checker) isLocalToTask(sym *Symbol) bool {
	if c.taskOuter == nil {
		return true
	}
	for cur := c.current; cur != nil && cur != c.taskOuter; cur = cur.parent {
		if cur.syms == nil {
			continue
		}
		if got, ok := cur.syms[sym.Name]; ok && got == sym {
			return true
		}
	}
	return false
}

// rootIdent walks an expression chain like `a.b.c[0].d` down to its root
// identifier. Returns nil if the root isn't an Ident (e.g. the chain starts
// from a call result, which can't be assigned to an outer name anyway).
func rootIdent(e ast.Expr) *ast.Ident {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x
		case *ast.FieldExpr:
			e = x.Target
		case *ast.IndexExpr:
			e = x.Target
		default:
			return nil
		}
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
		for i := len(c.narrows) - 1; i >= 0; i-- {
			if narrowed, ok := c.narrows[i][e.Name]; ok {
				c.info.Types[e] = narrowed
				return narrowed
			}
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
	case *ast.CastExpr:
		return c.checkCast(e)
	case *ast.FuncLit:
		return c.checkFuncLit(e)
	}
	c.errorf(e.Pos(), "unhandled expression type %T", e)
	return types.Invalid
}

// checkFuncLit type-checks an anonymous function literal and records its
// free variables — names referenced inside the body that resolve to a
// binding in the enclosing function (i.e., a non-global, non-builtin,
// non-top-level-function symbol). The codegen consults FreeVars to decide
// whether the lambda is a pure-function hoist or requires a closure cell.
func (c *Checker) checkFuncLit(e *ast.FuncLit) types.Type {
	params := make([]types.Type, len(e.Params))
	for i, p := range e.Params {
		params[i] = c.resolveTypeExpr(p.TypeAnn)
	}
	ret := c.resolveTypeExpr(e.Result)
	saved := c.current
	savedRet := c.currentRet
	c.current = newScope(saved)
	c.currentRet = ret
	for i, p := range e.Params {
		paramSym := &Symbol{
			Name:    p.Name,
			Type:    params[i],
			IsParam: true,
			DeclPos: p.NamePos,
		}
		if !c.current.define(paramSym) {
			c.errorf(p.NamePos, "duplicate parameter %q in function literal", p.Name)
		}
	}
	c.checkBlock(e.Body)
	c.current = saved
	c.currentRet = savedRet

	// Compute free variables: walk the body for identifier references whose
	// resolved symbol was declared OUTSIDE the lambda body's source span,
	// is neither a builtin, a top-level function, nor a module-level global,
	// and isn't one of the lambda's own parameters.
	bodyStart := e.Body.LBrace
	bodyEnd := e.Body.RBrace
	ownParams := make(map[string]bool, len(e.Params))
	for _, p := range e.Params {
		ownParams[p.Name] = true
	}
	seen := map[string]bool{}
	collectFreeVars(e.Body, c.info, bodyStart, bodyEnd, ownParams, seen, &e.FreeVars)
	return &types.Func{Params: params, Result: ret}
}

// collectFreeVars walks the lambda body and appends every identifier whose
// declaration falls outside [start, end] and which represents a captured
// local (not a global, builtin, or top-level function, and not one of the
// lambda's own parameters). Duplicates are suppressed via `seen`.
func collectFreeVars(b *ast.Block, info *TypeInfo, start, end token.Pos, ownParams map[string]bool, seen map[string]bool, out *[]string) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		walkStmtFreeVars(s, info, start, end, ownParams, seen, out)
	}
}

func walkStmtFreeVars(s ast.Stmt, info *TypeInfo, start, end token.Pos, ownParams map[string]bool, seen map[string]bool, out *[]string) {
	switch s := s.(type) {
	case *ast.DeclStmt:
		walkExprFreeVars(s.Decl.Value, info, start, end, ownParams, seen, out)
	case *ast.ExprStmt:
		walkExprFreeVars(s.X, info, start, end, ownParams, seen, out)
	case *ast.AssignStmt:
		walkExprFreeVars(s.Value, info, start, end, ownParams, seen, out)
	case *ast.FieldAssignStmt:
		walkExprFreeVars(s.Target, info, start, end, ownParams, seen, out)
		walkExprFreeVars(s.Value, info, start, end, ownParams, seen, out)
	case *ast.ReturnStmt:
		walkExprFreeVars(s.Value, info, start, end, ownParams, seen, out)
	case *ast.IfStmt:
		walkExprFreeVars(s.Cond, info, start, end, ownParams, seen, out)
		collectFreeVars(s.Then, info, start, end, ownParams, seen, out)
		collectFreeVars(s.Else, info, start, end, ownParams, seen, out)
	case *ast.ForStmt:
		walkExprFreeVars(s.Iter, info, start, end, ownParams, seen, out)
		collectFreeVars(s.Body, info, start, end, ownParams, seen, out)
	case *ast.MatchStmt:
		walkExprFreeVars(s.Subject, info, start, end, ownParams, seen, out)
		for _, arm := range s.Cases {
			collectFreeVars(arm.Body, info, start, end, ownParams, seen, out)
		}
	case *ast.Block:
		collectFreeVars(s, info, start, end, ownParams, seen, out)
	case *ast.DeferStmt:
		collectFreeVars(s.Body, info, start, end, ownParams, seen, out)
	case *ast.ParallelStmt:
		collectFreeVars(s.Body, info, start, end, ownParams, seen, out)
	case *ast.TaskStmt:
		collectFreeVars(s.Body, info, start, end, ownParams, seen, out)
	}
}

func walkExprFreeVars(e ast.Expr, info *TypeInfo, start, end token.Pos, ownParams map[string]bool, seen map[string]bool, out *[]string) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *ast.Ident:
		sym := info.Uses[e]
		if sym == nil {
			return
		}
		if sym.IsBuiltin || sym.IsFunc || sym.Module != nil {
			return
		}
		if ownParams[sym.Name] && sym.IsParam {
			return
		}
		if !sym.IsParam && !isPosBefore(sym.DeclPos, start) && !isPosAfter(sym.DeclPos, end) {
			// Declared inside the lambda body — local, not free.
			return
		}
		if seen[sym.Name] {
			return
		}
		seen[sym.Name] = true
		*out = append(*out, sym.Name)
	case *ast.CallExpr:
		walkExprFreeVars(e.Callee, info, start, end, ownParams, seen, out)
		for _, a := range e.Args {
			walkExprFreeVars(a, info, start, end, ownParams, seen, out)
		}
	case *ast.BinaryExpr:
		walkExprFreeVars(e.Lhs, info, start, end, ownParams, seen, out)
		walkExprFreeVars(e.Rhs, info, start, end, ownParams, seen, out)
	case *ast.UnaryExpr:
		walkExprFreeVars(e.Operand, info, start, end, ownParams, seen, out)
	case *ast.IndexExpr:
		walkExprFreeVars(e.Target, info, start, end, ownParams, seen, out)
		walkExprFreeVars(e.Index, info, start, end, ownParams, seen, out)
	case *ast.FieldExpr:
		walkExprFreeVars(e.Target, info, start, end, ownParams, seen, out)
	case *ast.CoalesceExpr:
		walkExprFreeVars(e.Lhs, info, start, end, ownParams, seen, out)
		walkExprFreeVars(e.Rhs, info, start, end, ownParams, seen, out)
	case *ast.UnwrapExpr:
		walkExprFreeVars(e.Operand, info, start, end, ownParams, seen, out)
	case *ast.TryExpr:
		walkExprFreeVars(e.Operand, info, start, end, ownParams, seen, out)
	case *ast.CastExpr:
		walkExprFreeVars(e.Operand, info, start, end, ownParams, seen, out)
	case *ast.ArrayLit:
		for _, x := range e.Elems {
			walkExprFreeVars(x, info, start, end, ownParams, seen, out)
		}
	case *ast.RecordLit:
		walkExprFreeVars(e.Spread, info, start, end, ownParams, seen, out)
		for _, f := range e.Fields {
			walkExprFreeVars(f.Value, info, start, end, ownParams, seen, out)
		}
	case *ast.StringLit:
		for _, p := range e.Parts {
			walkExprFreeVars(p, info, start, end, ownParams, seen, out)
		}
	case *ast.CmdLit:
		for _, p := range e.Parts {
			walkExprFreeVars(p, info, start, end, ownParams, seen, out)
		}
	case *ast.RangeExpr:
		walkExprFreeVars(e.Start, info, start, end, ownParams, seen, out)
		walkExprFreeVars(e.End, info, start, end, ownParams, seen, out)
	case *ast.FuncLit:
		// Nested lambda: its own free-var set is already populated by its
		// own checkFuncLit pass; lift those that escape the outer lambda
		// into the outer's free-var set as well, except those that are the
		// outer lambda's own params.
		for _, name := range e.FreeVars {
			if ownParams[name] {
				continue
			}
			if seen[name] {
				continue
			}
			seen[name] = true
			*out = append(*out, name)
		}
	}
}

func isPosBefore(a, b token.Pos) bool {
	if a.File != b.File {
		return false
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Col < b.Col
}

func isPosAfter(a, b token.Pos) bool {
	if a.File != b.File {
		return false
	}
	if a.Line != b.Line {
		return a.Line > b.Line
	}
	return a.Col > b.Col
}

// checkCast validates `expr as TypeName`. The cast is structural for record
// types: every field of the target must exist in the source with an
// assignable type. Casts to the same record type are a no-op (allowed).
// Casting to or from a primitive is rejected — use the str/num/floatOf
// builtins for those conversions.
func (c *Checker) checkCast(e *ast.CastExpr) types.Type {
	srcT := c.checkExpr(e.Operand)
	tgtT := c.resolveTypeExpr(e.TypeAnn)
	if srcT == types.Invalid || tgtT == nil || tgtT == types.Invalid {
		return types.Invalid
	}
	tgtRec, isRec := tgtT.(*types.Record)
	if !isRec {
		c.errorf(e.KwPos,
			"`as` casts are only supported to record types in v0, got %s",
			types.Format(tgtT))
		return types.Invalid
	}
	if types.Equal(srcT, tgtT) {
		return tgtT
	}
	srcRec, ok := srcT.(*types.Record)
	if !ok {
		c.errorf(e.KwPos,
			"cannot cast %s to record %s; only record-to-record casts are allowed",
			types.Format(srcT), tgtRec.Name)
		return types.Invalid
	}
	for _, tf := range tgtRec.Fields {
		sf := srcRec.Lookup(tf.Name)
		if sf == nil {
			c.errorf(e.KwPos,
				"cannot cast %s to %s: source has no field %q",
				srcRec.Name, tgtRec.Name, tf.Name)
			return types.Invalid
		}
		if !types.IsAssignable(sf.Type, tf.Type) {
			c.errorf(e.KwPos,
				"cannot cast %s to %s: field %q is %s in source but %s in target",
				srcRec.Name, tgtRec.Name, tf.Name,
				types.Format(sf.Type), types.Format(tf.Type))
			return types.Invalid
		}
	}
	return tgtT
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
		if e.Spread != nil {
			c.checkExpr(e.Spread)
		}
		for _, f := range e.Fields {
			c.checkExpr(f.Value)
		}
		return types.Invalid
	}
	if _, isSum := resolved.(*types.Sum); isSum {
		c.errorf(e.NamePos,
			"cannot construct sum type %q directly; use one of its variants",
			e.TypeName)
		if e.Spread != nil {
			c.checkExpr(e.Spread)
		}
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
	// A spread source must have the same record type as the literal so
	// every field is guaranteed to be present and well-typed.
	if e.Spread != nil {
		st := c.checkExpr(e.Spread)
		if st != types.Invalid && !types.Equal(st, rec) {
			c.errorf(e.SpreadPos,
				"record spread source must have type %s, got %s",
				rec.Name, types.Format(st))
		}
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
	// A spread fills any field not explicitly overridden, so the
	// "missing field" check only fires when no spread is present.
	if e.Spread == nil {
		for _, f := range rec.Fields {
			if !seen[f.Name] {
				c.errorf(e.LBrace, "record literal is missing field %q", f.Name)
			}
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
	// expectedType applies only to *this* call (e.g. readCsv with a known LHS
	// row-record type). Snapshot and clear so it doesn't bleed into nested
	// argument checks.
	expected := c.expectedType
	c.expectedType = nil
	defer func() { c.expectedType = expected }()
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
		case "count":
			return c.checkCountCall(e)
		case "unique":
			return c.checkUniqueCall(e)
		case "readCsv":
			return c.checkReadCsvCall(e, expected)
		case "writeCsv":
			return c.checkWriteCsvCall(e)
		case "assertEq", "assertNe", "check", "fail", "skip":
			return c.checkTestBuiltinCall(e, sym)
		case "mockExec", "mockFetch", "mockEnv", "mockReadFile", "mockLlm", "mockLlmCalls",
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
	if len(ft.TypeParams) > 0 {
		return c.checkGenericCall(e, id, ft)
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

// checkGenericCall infers each *types.TypeVar in ft.TypeParams from the call's
// argument types, substitutes the binding back into ft, and then performs the
// usual assignability check on the (now monomorphic) signature. The inferred
// type arguments are recorded on c.info.GenericInsts[e] in the same order as
// ft.TypeParams so codegen can monomorphize.
func (c *Checker) checkGenericCall(e *ast.CallExpr, id *ast.Ident, ft *types.Func) types.Type {
	if len(e.Args) != len(ft.Params) {
		c.errorf(e.LParenPos, "function %q expects %d argument(s), got %d", id.Name, len(ft.Params), len(e.Args))
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return types.Invalid
	}
	// Type-check arguments first so we have concrete types to unify against.
	argTypes := make([]types.Type, len(e.Args))
	for i, a := range e.Args {
		argTypes[i] = c.checkExpr(a)
	}
	subst := map[*types.TypeVar]types.Type{}
	for i, p := range ft.Params {
		if argTypes[i] == types.Invalid {
			continue
		}
		if !c.unify(p, argTypes[i], subst) {
			c.errorf(e.Args[i].Pos(),
				"argument %d to %q: expected %s, got %s",
				i+1, id.Name, types.Format(p), types.Format(argTypes[i]))
		}
	}
	// Verify every type parameter was inferred. With unbounded type params and
	// no explicit type arguments at the call site, an uninferred TypeVar means
	// the user wrote a function whose param types don't actually mention all
	// of the type params (e.g. `func f<T>(): T`). Reject with a clear message.
	for _, tv := range ft.TypeParams {
		if _, ok := subst[tv]; !ok {
			c.errorf(e.LParenPos,
				"cannot infer type parameter %q for %q from arguments — generic functions in v0 require every type parameter to appear in a parameter type",
				tv.Name, id.Name)
			return types.Invalid
		}
	}
	// Validate each substitution against the language's type-soundness rules
	// (no array-of-optional, no array-of-array, etc.) so a legal call site
	// can't bypass them via a generic.
	for _, tv := range ft.TypeParams {
		if !c.validTypeArg(subst[tv], e.LParenPos, tv) {
			return types.Invalid
		}
	}
	// Re-verify each argument with the substitution applied — catches
	// inconsistent inferences (e.g. T inferred as both number and string from
	// two parameters that both mention T).
	for i, p := range ft.Params {
		want := types.Substitute(p, subst)
		if argTypes[i] != types.Invalid && !types.IsAssignable(argTypes[i], want) {
			c.errorf(e.Args[i].Pos(),
				"argument %d to %q: expected %s, got %s",
				i+1, id.Name, types.Format(want), types.Format(argTypes[i]))
		}
	}
	if c.info.GenericInsts == nil {
		c.info.GenericInsts = map[*ast.CallExpr][]types.Type{}
	}
	args := make([]types.Type, len(ft.TypeParams))
	for i, tv := range ft.TypeParams {
		args[i] = subst[tv]
	}
	c.info.GenericInsts[e] = args
	return types.Substitute(ft.Result, subst)
}

// unify walks param and arg in lock-step, recording a *types.TypeVar →
// concrete-type mapping in subst. Returns false if the shapes don't match
// or if a TypeVar is bound to two incompatible types.
func (c *Checker) unify(param, arg types.Type, subst map[*types.TypeVar]types.Type) bool {
	if tv, ok := param.(*types.TypeVar); ok {
		if existing, ok := subst[tv]; ok {
			return types.IsAssignable(arg, existing) || types.IsAssignable(existing, arg)
		}
		subst[tv] = arg
		return true
	}
	switch p := param.(type) {
	case *types.Array:
		a, ok := arg.(*types.Array)
		if !ok {
			return false
		}
		return c.unify(p.Elem, a.Elem, subst)
	case *types.Optional:
		// `null` is assignable to any optional, including a generic one. We
		// can't infer T from null alone, so leave subst untouched and let the
		// missing-binding diagnostic fire later.
		if arg == types.Null {
			return true
		}
		a, ok := arg.(*types.Optional)
		if !ok {
			// Auto-wrap: `T` is assignable to `T?`. Unify the inner shape.
			return c.unify(p.Elem, arg, subst)
		}
		return c.unify(p.Elem, a.Elem, subst)
	case *types.Func:
		a, ok := arg.(*types.Func)
		if !ok {
			return false
		}
		if len(p.Params) != len(a.Params) {
			return false
		}
		for i := range p.Params {
			if !c.unify(p.Params[i], a.Params[i], subst) {
				return false
			}
		}
		return c.unify(p.Result, a.Result, subst)
	}
	// Param has no TypeVars at this point; fall back to ordinary
	// assignability for the leaf check.
	return types.IsAssignable(arg, param)
}

// validTypeArg rejects type arguments that would produce an illegal
// concrete signature after substitution (e.g. an array of optional, which
// the codegen has no encoding for).
func (c *Checker) validTypeArg(t types.Type, pos token.Pos, tv *types.TypeVar) bool {
	if t == types.Void {
		c.errorf(pos, "type parameter %q cannot be instantiated as void", tv.Name)
		return false
	}
	if t == types.Null {
		c.errorf(pos, "type parameter %q cannot be instantiated as null", tv.Name)
		return false
	}
	return true
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

// checkCountCall validates `count(arr: T[], pred: func(T): bool): number`.
// Same shape as filter but produces a count.
func (c *Checker) checkCountCall(e *ast.CallExpr) types.Type {
	if len(e.Args) != 2 {
		c.errorf(e.LParenPos, "count expects 2 arguments (array, predicate), got %d", len(e.Args))
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return types.Invalid
	}
	at := c.checkExpr(e.Args[0])
	ft := c.checkExpr(e.Args[1])
	arr, ok := at.(*types.Array)
	if !ok {
		c.errorf(e.Args[0].Pos(), "count: first argument must be an array, got %s", types.Format(at))
		return types.Invalid
	}
	fn, ok := ft.(*types.Func)
	if !ok {
		c.errorf(e.Args[1].Pos(), "count: second argument must be a function, got %s", types.Format(ft))
		return types.Invalid
	}
	if len(fn.Params) != 1 || !types.Equal(fn.Params[0], arr.Elem) {
		c.errorf(e.Args[1].Pos(), "count: predicate must take one parameter of type %s", types.Format(arr.Elem))
		return types.Invalid
	}
	if fn.Result != types.Bool {
		c.errorf(e.Args[1].Pos(), "count: predicate must return bool, got %s", types.Format(fn.Result))
		return types.Invalid
	}
	return types.Number
}

// checkUniqueCall validates `unique(arr: T[]): T[]`. T must be a primitive
// (string/number/float/bool); deduplicating arrays of records would require
// structural equality on records and is deferred.
func (c *Checker) checkUniqueCall(e *ast.CallExpr) types.Type {
	if len(e.Args) != 1 {
		c.errorf(e.LParenPos, "unique expects 1 argument, got %d", len(e.Args))
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return types.Invalid
	}
	at := c.checkExpr(e.Args[0])
	arr, ok := at.(*types.Array)
	if !ok {
		c.errorf(e.Args[0].Pos(), "unique: argument must be an array, got %s", types.Format(at))
		return types.Invalid
	}
	switch arr.Elem {
	case types.String, types.Number, types.Float, types.Bool:
		return arr
	}
	c.errorf(e.Args[0].Pos(),
		"unique: element type must be a primitive (string, number, float, bool), got %s",
		types.Format(arr.Elem))
	return types.Invalid
}

// checkReadCsvCall validates `readCsv(path: string): T[]`. The element type
// T comes from the surrounding context (variable annotation), and must be a
// record whose fields are all primitives. The expected type is captured by
// checkVarDecl / checkAssignStmt before the regular call-checking path runs;
// when reached without context, the call is rejected.
func (c *Checker) checkReadCsvCall(e *ast.CallExpr, want types.Type) types.Type {
	if len(e.Args) != 1 {
		c.errorf(e.LParenPos, "readCsv expects 1 argument (path), got %d", len(e.Args))
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return types.Invalid
	}
	pt := c.checkExpr(e.Args[0])
	if pt != types.Invalid && pt != types.String {
		c.errorf(e.Args[0].Pos(), "readCsv: path must be string, got %s", types.Format(pt))
	}
	if want == nil {
		c.errorf(e.LParenPos,
			"readCsv requires a typed context, e.g. `let xs: Person[] = readCsv(...)`")
		return types.Invalid
	}
	arr, ok := want.(*types.Array)
	if !ok {
		c.errorf(e.LParenPos, "readCsv: expected array type from context, got %s", types.Format(want))
		return types.Invalid
	}
	rec, ok := arr.Elem.(*types.Record)
	if !ok {
		c.errorf(e.LParenPos, "readCsv: element type must be a record, got %s", types.Format(arr.Elem))
		return types.Invalid
	}
	for _, f := range rec.Fields {
		if !isCsvFieldType(f.Type) {
			c.errorf(e.LParenPos,
				"readCsv: record field %q must be a primitive (string, number, float, bool), got %s",
				f.Name, types.Format(f.Type))
			return types.Invalid
		}
	}
	return arr
}

// checkWriteCsvCall validates `writeCsv(rows: T[], path: string): void`.
// T must be a record of primitive fields.
func (c *Checker) checkWriteCsvCall(e *ast.CallExpr) types.Type {
	if len(e.Args) != 2 {
		c.errorf(e.LParenPos, "writeCsv expects 2 arguments (rows, path), got %d", len(e.Args))
		for _, a := range e.Args {
			c.checkExpr(a)
		}
		return types.Void
	}
	at := c.checkExpr(e.Args[0])
	pt := c.checkExpr(e.Args[1])
	if pt != types.Invalid && pt != types.String {
		c.errorf(e.Args[1].Pos(), "writeCsv: path must be string, got %s", types.Format(pt))
	}
	arr, ok := at.(*types.Array)
	if !ok {
		c.errorf(e.Args[0].Pos(), "writeCsv: rows must be an array of records, got %s", types.Format(at))
		return types.Void
	}
	rec, ok := arr.Elem.(*types.Record)
	if !ok {
		c.errorf(e.Args[0].Pos(), "writeCsv: row element type must be a record, got %s", types.Format(arr.Elem))
		return types.Void
	}
	for _, f := range rec.Fields {
		if !isCsvFieldType(f.Type) {
			c.errorf(e.Args[0].Pos(),
				"writeCsv: record field %q must be a primitive (string, number, float, bool), got %s",
				f.Name, types.Format(f.Type))
			return types.Void
		}
	}
	return types.Void
}

// isCsvFieldType reports whether a record field is representable as a CSV
// cell. Optional primitives are OK (rendered as empty / parsed as null).
// Arrays, records-of-records, and sums are rejected.
func isCsvFieldType(t types.Type) bool {
	switch t {
	case types.String, types.Number, types.Float, types.Bool:
		return true
	}
	if opt, ok := t.(*types.Optional); ok {
		switch opt.Elem {
		case types.String, types.Number, types.Float, types.Bool:
			return true
		}
	}
	return false
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

func isNullLit(e ast.Expr) bool {
	_, ok := e.(*ast.NullLit)
	return ok
}

func (c *Checker) extractNarrowings(cond ast.Expr, invert bool) map[string]types.Type {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	if bin.Op != token.Eq && bin.Op != token.Neq {
		return nil
	}
	notNull := bin.Op == token.Neq
	if invert {
		notNull = !notNull
	}
	if !notNull {
		return nil
	}
	var ident *ast.Ident
	if isNullLit(bin.Rhs) {
		ident, _ = bin.Lhs.(*ast.Ident)
	} else if isNullLit(bin.Lhs) {
		ident, _ = bin.Rhs.(*ast.Ident)
	}
	if ident == nil {
		return nil
	}
	sym := c.current.resolve(ident.Name)
	if sym == nil {
		return nil
	}
	opt, isOpt := sym.Type.(*types.Optional)
	if !isOpt {
		return nil
	}
	return map[string]types.Type{ident.Name: opt.Elem}
}

func (c *Checker) pushNarrow(m map[string]types.Type) {
	if m == nil {
		m = map[string]types.Type{}
	}
	c.narrows = append(c.narrows, m)
}

func (c *Checker) popNarrow() {
	if len(c.narrows) > 0 {
		c.narrows = c.narrows[:len(c.narrows)-1]
	}
}

func builtins() []*Symbol {
	str := types.String
	num := types.Number
	flt := types.Float
	bln := types.Bool
	void := types.Void
	stringArr := &types.Array{Elem: types.String}
	floatArr := &types.Array{Elem: types.Float}
	numArr := &types.Array{Elem: types.Number}
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
		// Byte-level counterparts of len/slice. `len`/`slice` operate on
		// UTF-8 codepoints; `byteLen`/`byteSlice` retain byte semantics for
		// callers that need them (e.g. binary protocols).
		mk("byteLen", []types.Type{str}, num),
		mk("byteSlice", []types.Type{str, num, num}, str),
		mk("trimStart", []types.Type{str}, str),
		mk("trimEnd", []types.Type{str}, str),
		mk("repeat", []types.Type{str, num}, str),
		mk("indexOf", []types.Type{str, str}, num),
		mk("parseInt", []types.Type{str}, &types.Optional{Elem: num}),
		mk("abs", []types.Type{num}, num),
		mk("max", []types.Type{num, num}, num),
		mk("min", []types.Type{num, num}, num),
		mk("sorted", []types.Type{stringArr}, stringArr),
		mk("reversed", []types.Type{stringArr}, stringArr),

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

		// Numeric vector builtins (numpy-lite). All operate on float[];
		// arange returns number[]. Reductions return float; element-wise
		// ops return float[]. Mismatched-length binary ops use the shorter
		// operand's length (no error).
		mk("vSum", []types.Type{floatArr}, flt),
		mk("vMean", []types.Type{floatArr}, flt),
		mk("vMin", []types.Type{floatArr}, flt),
		mk("vMax", []types.Type{floatArr}, flt),
		mk("vVar", []types.Type{floatArr}, flt),
		mk("vStd", []types.Type{floatArr}, flt),
		mk("vAdd", []types.Type{floatArr, floatArr}, floatArr),
		mk("vSub", []types.Type{floatArr, floatArr}, floatArr),
		mk("vMul", []types.Type{floatArr, floatArr}, floatArr),
		mk("vScale", []types.Type{floatArr, flt}, floatArr),
		mk("vDot", []types.Type{floatArr, floatArr}, flt),
		mk("linspace", []types.Type{flt, flt, num}, floatArr),
		mk("arange", []types.Type{num, num, num}, numArr),
		mk("cumsum", []types.Type{floatArr}, floatArr),

		// Higher-order builtins. The signatures registered here are
		// placeholders — the actual type checking is done by the
		// checkMapCall / checkFilterCall / checkReduceCall handlers above,
		// which need to dispatch on the array element type.
		mk("map", []types.Type{stringArr, str}, stringArr),
		mk("filter", []types.Type{stringArr, str}, stringArr),
		mk("reduce", []types.Type{stringArr, str, str}, str),

		// Pandas-lite. Like map/filter/reduce, the signatures are
		// placeholders; checkCountCall/checkUniqueCall/checkReadCsvCall/
		// checkWriteCsvCall do the real work.
		mk("count", []types.Type{stringArr, str}, num),
		mk("unique", []types.Type{stringArr}, stringArr),
		mk("readCsv", []types.Type{str}, stringArr),
		mk("writeCsv", []types.Type{stringArr, str}, void),

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

		// Agent-platform builtins.
		//
		//   llm(prompt) -> string         shells out to $TARTALO_LLM_CMD; effect !ai
		//   approval(prompt) -> bool      prompts on /dev/tty (y/n); effect !io
		//   trace(label, value) -> void   appends NDJSON to $TARTALO_TRACE if set
		//   spawnAgent(name, in) -> string call agent by name in this program
		//   callTool(name, in) -> string  call a (string)→string tool by name
		//   agentTools() -> string        JSON of the surrounding agent's tools
		//   toolSchemas() -> string       JSON of all declared tools/agents
		//   mockLlm(pat, resp) -> void    test-only: canned llm response
		//   mockLlmCalls() -> string[]    test-only: prompts seen this run
		mk("llm", []types.Type{str}, str),
		mk("approval", []types.Type{str}, bln),
		mk("trace", []types.Type{str, str}, void),
		mk("spawnAgent", []types.Type{str, str}, str),
		mk("callTool", []types.Type{str, str}, str),
		mk("agentTools", nil, str),
		mk("toolSchemas", nil, str),
		mk("mockLlm", []types.Type{str, str}, void),
		mk("mockLlmCalls", nil, stringArr),
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
