// Package nativegen turns a type-checked Tartalo AST into a self-contained Go
// source file that can be compiled with the standard `go build` toolchain to
// produce a native binary for any platform Go supports.
//
// Conventions in the emitted Go:
//
//   - Everything lives in a single `package main`. Cross-module name collisions
//     are avoided by reusing the checker's MangledName (`__mN__name`) for
//     non-entry modules and then prefixing every user-derived identifier with
//     `tt_` to keep clear of Go reserved words and predeclared identifiers.
//   - Tartalo `string`/`number`/`float`/`bool` map to Go `string`/`int64`/
//     `float64`/`bool`. Records become Go structs, arrays become slices.
//   - Tartalo `T?` is `*T`. `null` is `nil`. Coalesce/unwrap go through tiny
//     generic helpers emitted in the runtime preamble (no separate package).
//   - `str(true)`/`str(false)` produce "1"/"0" to match the existing sh
//     backend's behaviour, so test fixtures and example output are identical.
//   - All builtins lower to Go stdlib calls (strings, os, time, etc).
package nativegen

import (
	"sort"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/loader"
	"github.com/enekos/tartalo/internal/types"
)

// Generator walks a typed AST and accumulates the Go source for a single
// `package main` program. It is single-use: build a fresh Generator per
// compile.
type Generator struct {
	info *checker.TypeInfo

	// out collects the body of the program (everything between the import
	// block and the runtime preamble at the bottom of the file).
	out strings.Builder

	indent int
	tmpSeq int

	// imports is a deduplicated set of stdlib packages the emitter has
	// determined the generated code needs. Populated as builtins are emitted.
	imports map[string]struct{}

	// currentModule mirrors the codegen's same-named field; mangling helpers
	// consult it while emitting top-level decls.
	currentModule *loader.Module

	// currentReturnType is the declared return type of the function being
	// emitted (nil at module scope). Used by emitReturn / unwrap helpers if
	// they need to know how to wrap the result.
	currentReturnType types.Type

	// flags toggled while walking — used to gate runtime helpers and imports.
	usesRuntimeUnwrap      bool
	usesRuntimePtr         bool
	usesRuntimeCoalesce    bool
	usesRuntimeShellOut    bool
	usesRuntimeArgs        bool
	usesRuntimeExec        bool
	usesRuntimeExecTimeout bool
	usesRuntimeFile        bool
	usesRuntimePath        bool
	usesRuntimeStat        bool
	usesRuntimeJSON        bool
	usesRuntimeRegex       bool
	usesRuntimeFormatTime  bool
	usesRuntimeFloat       bool
	usesRuntimeHigherOrder bool
	usesRuntimeFetch       bool
	usesRuntimeTestState   bool
	usesRuntimeEnv         bool
	usesRuntimeNow         bool
	usesRuntimeTry         bool

	// usesMockedBuiltin records, per builtin, whether the program calls the
	// matching mock setter / inspector. The runtime emits the dispatcher in
	// test mode regardless of this; the flag controls whether we emit the
	// per-builtin mock state slot and the helper functions that touch it.
	usesMockExec     bool
	usesMockFetch    bool
	usesMockEnv      bool
	usesMockReadFile bool
	usesMockNow      bool
	usesMockArgs     bool
	usesMockStdin    bool

	// emitMode selects whether the generated program calls main() (Run) or
	// invokes the test harness footer (Test).
	emitMode EmitMode
}

// EmitMode selects the program shape: EmitRun produces a `main()` that calls
// the user's main; EmitTest produces a runner that drives every `test "..."`
// declaration in the entry module.
type EmitMode int

const (
	EmitRun EmitMode = iota
	EmitTest
)

// New returns a Generator ready to emit a Go program for the given type info.
func New(info *checker.TypeInfo) *Generator {
	return &Generator{
		info:    info,
		imports: map[string]struct{}{},
	}
}

// EmitModules walks the modules in topological order and returns a complete
// Go source file in EmitRun mode (calls main()).
func (g *Generator) EmitModules(modules []*loader.Module) string {
	g.emitMode = EmitRun
	return g.emitProgram(modules)
}

// EmitModulesTest is the test-mode counterpart: every `test "..." { ... }`
// declaration in the entry module is compiled to a Go function and the
// runtime test harness drives them in declaration order.
func (g *Generator) EmitModulesTest(modules []*loader.Module) string {
	g.emitMode = EmitTest
	return g.emitProgram(modules)
}

func (g *Generator) emitProgram(modules []*loader.Module) string {
	// Test mode always emits the mock state struct, which references
	// *regexp.Regexp. Pre-register the import so it lands in the import
	// block (writeRuntimeTo runs after writeImportsTo).
	if g.emitMode == EmitTest {
		g.addImport("regexp")
	}

	// Pass 0: predeclared record types (Response, Process, FileInfo,
	// PathParts). The checker rejects user-side redeclaration, so emitting
	// them unconditionally is safe; Go is happy with declared-but-unused
	// types so this never breaks `go build`.
	g.emitPredeclaredTypes()

	// Pass 1: type declarations across all modules. Records used as parameters
	// or fields must be in scope before any function body that uses them.
	for _, m := range modules {
		g.currentModule = m
		for _, d := range m.File.Decls {
			if td, ok := d.(*ast.TypeDecl); ok {
				g.emitTypeDecl(td)
			}
		}
	}

	// Pass 2: function definitions.
	for _, m := range modules {
		g.currentModule = m
		for _, d := range m.File.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				g.emitFunc(fd)
				g.writeLine("")
			}
		}
	}

	// Pass 3: globals. Tartalo evaluates these in module order before main(),
	// so we wrap them in a synthesized __ttInit() called from main().
	hasGlobals := false
	for _, m := range modules {
		for _, d := range m.File.Decls {
			if _, ok := d.(*ast.VarDecl); ok {
				hasGlobals = true
				break
			}
		}
		if hasGlobals {
			break
		}
	}
	if hasGlobals {
		g.declareGlobals(modules)
		g.writeLine("")
		g.writeLine("func __ttInit() {")
		g.indent++
		for _, m := range modules {
			g.currentModule = m
			for _, d := range m.File.Decls {
				if vd, ok := d.(*ast.VarDecl); ok {
					g.emitGlobalInit(vd)
				}
			}
		}
		g.indent--
		g.writeLine("}")
		g.writeLine("")
	}

	// In test mode emit one Go function per `test "..."` declaration in the
	// entry module, plus a slice of {name, fn} test cases for the harness.
	var entry *loader.Module
	for _, m := range modules {
		if m.IsEntry {
			entry = m
			break
		}
	}
	g.currentModule = entry
	if g.emitMode == EmitTest {
		g.emitTestFunctions(entry)
	}

	g.writeLine("func main() {")
	g.indent++
	if hasGlobals {
		g.writeLine("__ttInit()")
	}
	switch g.emitMode {
	case EmitRun:
		if entry != nil {
			if _, ok := g.info.Decls[checker.MangledName(entry, "main")]; ok {
				g.writeLine(g.goFuncName(entry, "main") + "()")
			}
		}
	case EmitTest:
		g.emitTestRunnerCall(entry)
	}
	g.indent--
	g.writeLine("}")

	// Stitch the file together: header, imports, body, runtime helpers.
	var file strings.Builder
	file.WriteString("// Code generated by tartalo. DO NOT EDIT.\n")
	file.WriteString("package main\n\n")
	g.writeImportsTo(&file)
	file.WriteString(g.out.String())
	g.writeRuntimeTo(&file)
	return file.String()
}

// declareGlobals emits a top-level `var` block declaring every Tartalo global
// with its zero value. The actual initialiser expressions are written inside
// __ttInit so they can call functions, observe each other's values, and so on.
func (g *Generator) declareGlobals(modules []*loader.Module) {
	g.writeLine("var (")
	g.indent++
	for _, m := range modules {
		g.currentModule = m
		for _, d := range m.File.Decls {
			vd, ok := d.(*ast.VarDecl)
			if !ok {
				continue
			}
			t := g.info.Types[vd.Value]
			if ann := vd.TypeAnn; ann != nil {
				if at := g.typeFromAnn(ann); at != nil {
					t = at
				}
			}
			g.writeLine(g.goVarName(vd.Name) + " " + g.goType(t))
		}
	}
	g.indent--
	g.writeLine(")")
}

// emitGlobalInit produces a single assignment that runs inside __ttInit.
// We never use `:=` here — the variable is already declared at file scope.
func (g *Generator) emitGlobalInit(vd *ast.VarDecl) {
	rhs := g.compileExpr(vd.Value)
	target := g.goVarName(vd.Name)
	t := g.info.Types[vd.Value]
	if ann := vd.TypeAnn; ann != nil {
		if at := g.typeFromAnn(ann); at != nil {
			t = at
		}
	}
	g.writeLine(target + " = " + g.coerce(rhs, g.info.Types[vd.Value], t))
}

// goVarName mangles a value-namespace name (variable, parameter, function)
// into a Go identifier that's collision-free with Go reserved words and
// predeclared identifiers. Top-level names reuse the checker's module
// mangling so cross-module symbols stay distinct in the bundled program.
func (g *Generator) goVarName(name string) string {
	return "tt_" + checker.MangledName(g.currentModule, name)
}

// goLocalName mangles a strictly-local identifier (parameter or block-scoped
// `let`). No module mangling — these never escape their function.
func (g *Generator) goLocalName(name string) string {
	return "tt_" + name
}

// goFuncName builds the Go identifier for a top-level function in module m.
func (g *Generator) goFuncName(m *loader.Module, name string) string {
	return "tt_" + checker.MangledName(m, name)
}

// goTypeName mangles a type name. We capitalise the first character after
// the prefix so generated structs read more naturally in stack traces.
func goTypeName(name string) string {
	return "Tt_" + name
}

// goFieldName mangles a record field. Capitalising avoids collisions with
// Go reserved words used as field names (e.g. `type`, `range`).
func goFieldName(name string) string {
	return "F_" + name
}

func (g *Generator) writeLine(s string) {
	if g.indent > 0 {
		g.out.WriteString(strings.Repeat("\t", g.indent))
	}
	g.out.WriteString(s)
	g.out.WriteByte('\n')
}

func (g *Generator) tmp(prefix string) string {
	g.tmpSeq++
	return "_tt_" + prefix + itoa(g.tmpSeq)
}

func itoa(n int) string {
	if n < 10 {
		return string(byte('0' + n))
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// addImport flags a stdlib package as needed by the emitted program. The
// `import (...)` block is emitted at the very end, sorted, so the order in
// which builtins request packages doesn't matter.
func (g *Generator) addImport(pkg string) {
	g.imports[pkg] = struct{}{}
}

func (g *Generator) writeImportsTo(out *strings.Builder) {
	if len(g.imports) == 0 {
		return
	}
	pkgs := make([]string, 0, len(g.imports))
	for p := range g.imports {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	out.WriteString("import (\n")
	for _, p := range pkgs {
		out.WriteString("\t\"")
		out.WriteString(p)
		out.WriteString("\"\n")
	}
	out.WriteString(")\n\n")
}
