// Package codegen turns a type-checked AST into a POSIX-ish sh script.
//
// Conventions used by the generated code:
//
//   - Functions return their value via a hidden global named __ret. Callers
//     copy __ret into a fresh temporary the moment they receive it, so nested
//     calls don't clobber each other.
//   - Booleans are stored as 1 (true) or 0 (false), matching the result of
//     POSIX `$((expr))` evaluations. Conversions to/from exit codes happen at
//     `if` boundaries.
//   - All variable expansions are double-quoted to defend against word-splitting
//     and globbing.
//   - Function-local parameter bindings use `local`. POSIX doesn't standardize
//     `local`, but every modern /bin/sh (dash, bash, busybox ash) supports it,
//     and we lose more by polluting global scope than we gain by being strict.
package codegen

import (
	"fmt"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/loader"
	"github.com/enekos/tartalo/internal/token"
	"github.com/enekos/tartalo/internal/types"
)

// concatPrologues merges two prologue slices without the double-allocation
// of concatPrologues(a, b).
func concatPrologues(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	out = append(out, a...)
	out = append(out, b...)
	return out
}

// leafField is one shell-named slot inside a (possibly nested) record. Path is
// the dunder-joined name from the enclosing record's prefix to this slot. Type
// is the leaf type — never a record. Optional leaves carry a `<path>__null`
// sidecar; array leaves are a single newline-joined string slot.
type leafField struct {
	Path string
	Type types.Type
}

// recordLeaves walks a record's tree and returns every leaf field with its
// dunder-joined path. The checker has already rejected cyclic records, so this
// terminates.
func recordLeaves(rec *types.Record) []leafField {
	var out []leafField
	for _, f := range rec.Fields {
		if sub, ok := f.Type.(*types.Record); ok {
			for _, lf := range recordLeaves(sub) {
				out = append(out, leafField{Path: f.Name + "__" + lf.Path, Type: lf.Type})
			}
			continue
		}
		out = append(out, leafField{Path: f.Name, Type: f.Type})
	}
	return out
}

// sumLeaves returns the flat list of leaf slots for a sum-typed value: the
// "tag" leaf (a string holding the variant name) followed by every variant's
// fields, qualified by variant name (`Circle__r`, `Rectangle__w`, ...). At
// runtime exactly one variant's payload slots are meaningful for a given
// value; the others are initialised to safe zero values so consumers under
// `set -u` don't blow up.
func sumLeaves(s *types.Sum) []leafField {
	out := []leafField{{Path: "tag", Type: types.String}}
	for _, v := range s.Variants {
		for _, f := range v.Fields {
			out = append(out, leafField{Path: v.Name + "__" + f.Name, Type: f.Type})
		}
	}
	return out
}

// aggregateLeaves dispatches on the kind of aggregate so call sites that
// don't care which (function param/return fan-out, copies) can stay generic.
func aggregateLeaves(t types.Type) []leafField {
	switch t := t.(type) {
	case *types.Record:
		return recordLeaves(t)
	case *types.Sum:
		return sumLeaves(t)
	}
	return nil
}

type Generator struct {
	info   *checker.TypeInfo
	out    strings.Builder
	indent int
	tmpSeq int

	// currentModule is set while emitting top-level declarations of a module
	// so name-mangling helpers know which module's prefix to use.
	currentModule *loader.Module
	// currentReturnType tracks the declared return type of the function we're
	// emitting. emitReturn consults it to decide whether to also write a
	// `__ret__null` flag (for optional returns) or not.
	currentReturnType types.Type

	// trace, when true, makes the emitter prefix every statement with an
	// assignment to `__tt_loc` and install an EXIT trap that prints the last
	// known source location on a non-zero exit. Opt-in: it ~doubles the line
	// count of generated sh, so it's only useful when debugging.
	trace bool

	// needsArgv tracks whether the emitted script calls the `args()` builtin.
	// When false the `__tt_argv` snapshot is omitted, shrinking the output.
	needsArgv bool

	// usesRecordArrays tracks whether any emitted expression depends on the
	// `__tt_us` (ASCII Unit Separator) global used to encode arrays-of-records
	// as one-row-per-line strings.
	usesRecordArrays bool

	// usesDefer is set when any function body in any module contains a defer
	// statement; gates the emission of the global __tt_run_defers helper.
	usesDefer bool

	// currentFuncDefers is the per-defer-block helper-function name table
	// for the function currently being emitted. Set up by emitFunc before
	// walking the body so emitStmt can resolve DeferStmt → helper name.
	currentFuncDefers map[*ast.DeferStmt]string

	// currentFuncHasDefers is true while emitting a function whose body
	// contains at least one defer; emitReturn prepends a defer-runner call.
	currentFuncHasDefers bool

	// testMode is true while emitting a test runner script (EmitTest).
	// Builtins that check mock state branch on this flag — production
	// scripts stay free of any test-only code.
	testMode bool
}

func New(info *checker.TypeInfo) *Generator {
	return &Generator{info: info}
}

// WithTrace toggles the source-map runtime trace on/off. Returns the receiver
// for chaining: `codegen.New(info).WithTrace(true).EmitModules(...)`.
func (g *Generator) WithTrace(on bool) *Generator {
	g.trace = on
	return g
}

// EmitMode selects the script footer: EmitRun calls `main "$@"` when present
// (the build/run path), EmitTest emits a runner harness that drives every
// `test "..."` declaration in the entry module and exits non-zero on failure.
type EmitMode int

const (
	EmitRun  EmitMode = iota // call main(); ignore tests
	EmitTest                 // run all tests in the entry module; ignore main
)

// Emit produces the full shell script for a single parsed file. Convenience
// wrapper around EmitModules for the single-file path.
func (g *Generator) Emit(f *ast.File) string {
	m := &loader.Module{File: f, IsEntry: true}
	return g.EmitModules([]*loader.Module{m})
}

// EmitModules walks modules in topological order (deps before dependents) and
// emits a single bundled sh script. Functions and globals from non-entry
// modules are name-mangled (`__mN__name`) so cross-module collisions are
// impossible.
func (g *Generator) EmitModules(modules []*loader.Module) string {
	return g.emitModules(modules, EmitRun)
}

// EmitModulesTest is the same as EmitModules but emits a test runner footer.
// The entry module's `test "..." { ... }` declarations are compiled into sh
// functions and driven by a built-in harness with pretty pass/fail output.
// Exit code is 0 iff every test passed (skips don't fail the run).
func (g *Generator) EmitModulesTest(modules []*loader.Module) string {
	return g.emitModules(modules, EmitTest)
}

func (g *Generator) emitModules(modules []*loader.Module, mode EmitMode) string {
	g.testMode = mode == EmitTest
	g.writeLine("#!/bin/sh")
	g.writeLine("# Generated by tartalo. Do not edit by hand.")
	g.writeLine("set -eu")
	g.writeLine("")
	g.writeLine("__ret=\"\"")
	g.writeLine(`__tt_us=$(printf '\037')`)
	// Snapshot the script's positional args into a newline-joined string so
	// `args()` returns a stable view even from inside helper functions whose
	// own `$@` shadows the script's.
	g.writeLine(`__tt_argv=$(if [ $# -gt 0 ]; then for __tt_a in "$@"; do printf '%s\n' "$__tt_a"; done; fi)`)
	if g.trace {
		g.writeLines([]string{
			`__tt_loc=""`,
			`__tt_on_exit() {`,
			`  __tt_ec=$?`,
			`  trap - EXIT`,
			`  if [ "$__tt_ec" -ne 0 ] && [ -n "$__tt_loc" ]; then`,
			`    printf 'tartalo: error near %s (exit %d)\n' "$__tt_loc" "$__tt_ec" >&2`,
			`  fi`,
			`  exit "$__tt_ec"`,
			`}`,
			`trap __tt_on_exit EXIT`,
		})
	}
	g.writeLine("")

	// In test mode we declare placeholder test-state vars right after `set -eu`
	// so any reference inside helper code (e.g., a stray fail() in a function)
	// doesn't crash on `unbound variable`. The `__tt_mock_*` family is the
	// per-test mock state — populated by `mockEnv` / `mockNow` / `mockArgs` /
	// `mockReadStdin` and consulted by the matching builtin lowerings. Each
	// test runs inside a `( )` subshell so writes here never leak across
	// tests; we just need every name to exist so `set -u` is happy.
	if mode == EmitTest {
		g.writeLine(`__tt_msg_file=""`)
		g.writeLine(`__tt_skip_file=""`)
		g.writeLine(`__tt_mock_env=""`)
		g.writeLine(`__tt_mock_now_set=0`)
		g.writeLine(`__tt_mock_now=0`)
		g.writeLine(`__tt_mock_args_set=0`)
		g.writeLine(`__tt_mock_args=""`)
		g.writeLine(`__tt_mock_stdin_set=0`)
		g.writeLine(`__tt_mock_stdin=""`)
		g.writeLine("")
	}

	// Pre-pass: scan every function body for defer statements so the global
	// __tt_run_defers helper can be emitted up front (function definitions
	// come next, and any of them may need to call the helper).
	for _, m := range modules {
		for _, d := range m.File.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && hasDeferIn(fd.Body) {
				g.usesDefer = true
				break
			}
		}
		if g.usesDefer {
			break
		}
	}
	if g.usesDefer {
		g.writeLines([]string{
			`__tt_run_defers() {`,
			`  __tt_top=""`,
			`  while [ -n "${__tt_defstack:-}" ]; do`,
			`    __tt_top="${__tt_defstack%%:*}"`,
			`    case "$__tt_defstack" in`,
			`      *:*) __tt_defstack="${__tt_defstack#*:}" ;;`,
			`      *) __tt_defstack="" ;;`,
			`    esac`,
			`    "$__tt_top"`,
			`  done`,
			`}`,
			"",
		})
	}

	// Pass 1: function definitions, in topological order (a function may call
	// any other function declared in any loaded module).
	for _, m := range modules {
		g.currentModule = m
		for _, d := range m.File.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok {
				g.emitFunc(fd)
				g.writeLine("")
			}
		}
	}

	// Pass 2: globals, also in topological order.
	for _, m := range modules {
		g.currentModule = m
		for _, d := range m.File.Decls {
			if vd, ok := d.(*ast.VarDecl); ok {
				g.emitVarDecl(vd, false)
			}
		}
	}

	// Find the entry module to invoke its main(), if any.
	var entry *loader.Module
	for _, m := range modules {
		if m.IsEntry {
			entry = m
			break
		}
	}
	g.currentModule = entry

	switch mode {
	case EmitRun:
		// If the entry module declared `main`, invoke it. Imported modules'
		// main (if any) is just an ordinary function reachable by mangled name.
		if entry != nil {
			if _, ok := g.info.Decls[checker.MangledName(entry, "main")]; ok {
				g.writeLine("")
				g.writeLine(`main "$@"`)
			}
		}
	case EmitTest:
		g.emitTestFunctions(entry)
		g.emitTestHarness(entry)
	}
	result := g.out.String()
	if !g.needsArgv {
		idx := strings.Index(result, "__tt_argv=")
		if idx >= 0 {
			start := strings.LastIndexByte(result[:idx], '\n') + 1
			end := idx + strings.IndexByte(result[idx:], '\n')
			result = result[:start] + result[end+1:]
		}
	}
	if !g.usesRecordArrays {
		idx := strings.Index(result, "__tt_us=")
		if idx >= 0 {
			start := strings.LastIndexByte(result[:idx], '\n') + 1
			end := idx + strings.IndexByte(result[idx:], '\n')
			// also drop the comment block immediately preceding the assignment
			result = result[:start] + result[end+1:]
		}
	}
	return result
}

// emitTestFunctions emits one sh function per `test "..."` declaration in the
// entry module. Each is given a stable name (`__tt_test_<idx>`) so the
// harness can dispatch by name. The Tartalo body is lowered like a void
// function body — return-statements aren't valid (the parser will catch any
// stray ones), but assertions can `exit 1` to abort early.
func (g *Generator) emitTestFunctions(entry *loader.Module) {
	if entry == nil {
		return
	}
	g.currentModule = entry
	idx := 0
	for _, d := range entry.File.Decls {
		td, ok := d.(*ast.TestDecl)
		if !ok {
			continue
		}
		idx++
		g.writeLine("")
		g.writeLine(fmt.Sprintf("__tt_test_%d() {", idx))
		g.indent++
		for _, s := range td.Body.Stmts {
			g.emitStmt(s)
		}
		g.indent--
		g.writeLine("}")
	}
}

// emitTestHarness emits the runner: state init, the per-test wrapper that
// runs each test in a subshell so failed assertions can `exit 1` cleanly, and
// the summary footer. Exit status is 1 iff any test failed.
func (g *Generator) emitTestHarness(entry *loader.Module) {
	if entry == nil {
		return
	}
	// Collect tests with their display names.
	type testRef struct {
		idx  int
		name string
	}
	var tests []testRef
	idx := 0
	for _, d := range entry.File.Decls {
		td, ok := d.(*ast.TestDecl)
		if !ok {
			continue
		}
		idx++
		tests = append(tests, testRef{idx: idx, name: td.Name})
	}

	g.writeLine("")
	g.writeLines([]string{
		`__tt_passed=0`,
		`__tt_failed=0`,
		`__tt_skipped=0`,
		`__tt_total=0`,
		`__tt_c_pass=""`,
		`__tt_c_fail=""`,
		`__tt_c_skip=""`,
		`__tt_c_dim=""`,
		`__tt_c_bold=""`,
		`__tt_c_off=""`,
		`if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then`,
		`  __tt_c_pass=$(printf '\033[32m')`,
		`  __tt_c_fail=$(printf '\033[31m')`,
		`  __tt_c_skip=$(printf '\033[33m')`,
		`  __tt_c_dim=$(printf '\033[2m')`,
		`  __tt_c_bold=$(printf '\033[1m')`,
		`  __tt_c_off=$(printf '\033[0m')`,
		`fi`,
		``,
		`__tt_run_test() {`,
		`  __tt_name=$1`,
		`  __tt_fn=$2`,
		`  __tt_total=$((__tt_total + 1))`,
		`  __tt_msg_file=$(mktemp 2>/dev/null || printf '/tmp/tt_msg_%s_%s' "$$" "$__tt_total")`,
		`  __tt_skip_file=$(mktemp 2>/dev/null || printf '/tmp/tt_skip_%s_%s' "$$" "$__tt_total")`,
		`  : > "$__tt_msg_file"`,
		`  : > "$__tt_skip_file"`,
		`  __tt_status=0`,
		`  ( "$__tt_fn" ) || __tt_status=$?`,
		`  __tt_msg=$(cat "$__tt_msg_file" 2>/dev/null || printf '')`,
		`  __tt_skip=$(cat "$__tt_skip_file" 2>/dev/null || printf '')`,
		`  rm -f "$__tt_msg_file" "$__tt_skip_file"`,
		`  if [ -n "$__tt_skip" ]; then`,
		`    __tt_skipped=$((__tt_skipped + 1))`,
		`    printf '  %s-%s %s %s(skipped: %s)%s\n' "$__tt_c_skip" "$__tt_c_off" "$__tt_name" "$__tt_c_dim" "$__tt_skip" "$__tt_c_off"`,
		`    return 0`,
		`  fi`,
		`  if [ "$__tt_status" -eq 0 ] && [ -z "$__tt_msg" ]; then`,
		`    __tt_passed=$((__tt_passed + 1))`,
		`    printf '  %s\xe2\x9c\x93%s %s\n' "$__tt_c_pass" "$__tt_c_off" "$__tt_name"`,
		`    return 0`,
		`  fi`,
		`  __tt_failed=$((__tt_failed + 1))`,
		`  printf '  %s\xe2\x9c\x97%s %s%s%s\n' "$__tt_c_fail" "$__tt_c_off" "$__tt_c_bold" "$__tt_name" "$__tt_c_off"`,
		`  if [ -n "$__tt_msg" ]; then`,
		`    printf '%s\n' "$__tt_msg" | while IFS= read -r __tt_line; do`,
		`      printf '      %s\n' "$__tt_line"`,
		`    done`,
		`  fi`,
		`}`,
		``,
	})
	// Banner.
	suite := entry.File.Path
	if suite == "" {
		suite = "tests"
	}
	g.writeLine(fmt.Sprintf(`printf '%%srunning %d test(s) in %%s%%s\n\n' "$__tt_c_dim" '%s' "$__tt_c_off"`,
		len(tests), escForSingleQuoted(suite)))
	g.writeLine("")
	for _, t := range tests {
		g.writeLine(fmt.Sprintf(`__tt_run_test '%s' __tt_test_%d`,
			escForSingleQuoted(t.name), t.idx))
	}
	g.writeLine("")
	g.writeLines([]string{
		`printf '\n'`,
		`if [ "$__tt_failed" -eq 0 ]; then`,
		`  printf '%s%d passed%s' "$__tt_c_pass" "$__tt_passed" "$__tt_c_off"`,
		`else`,
		`  printf '%s%d failed%s, %d passed' "$__tt_c_fail" "$__tt_failed" "$__tt_c_off" "$__tt_passed"`,
		`fi`,
		`if [ "$__tt_skipped" -gt 0 ]; then`,
		`  printf ', %s%d skipped%s' "$__tt_c_skip" "$__tt_skipped" "$__tt_c_off"`,
		`fi`,
		`printf ' (%d total)\n' "$__tt_total"`,
		`if [ "$__tt_failed" -gt 0 ]; then exit 1; fi`,
	})
}

// --- low-level emit helpers -------------------------------------------------

func (g *Generator) writeLine(s string) {
	for i := 0; i < g.indent; i++ {
		g.out.WriteByte(' ')
		g.out.WriteByte(' ')
	}
	g.out.WriteString(s)
	g.out.WriteByte('\n')
}

func (g *Generator) writeLines(lines []string) {
	for _, l := range lines {
		g.writeLine(l)
	}
}

func (g *Generator) tmp(prefix string) string {
	g.tmpSeq++
	return "__" + prefix + itoa(g.tmpSeq)
}

// itoa is a fast non-allocating int-to-string for small positive ints.
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

func itoa64(n int64) string {
	if n >= 0 && n < 10 {
		return string(byte('0' + byte(n)))
	}
	var buf [24]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// --- function declarations --------------------------------------------------

func (g *Generator) emitFunc(fd *ast.FuncDecl) {
	mangled := shName(checker.MangledName(g.currentModule, fd.Name))
	g.writeLine(mangled + "() {")
	g.indent++
	// Record the return type so emitReturn knows whether to also write
	// __ret__null. We restore the previous value on exit so nested emits
	// (none today, but future-proof) don't leak.
	prevRet := g.currentReturnType
	if sym := g.info.Decls[checker.MangledName(g.currentModule, fd.Name)]; sym != nil {
		if ft, ok := sym.Type.(*types.Func); ok {
			g.currentReturnType = ft.Result
			// Initialise the return slots before binding any params. If the
			// function falls through without an explicit return, callers
			// see the safe default — null for optional, empty for everything
			// else — instead of stale state from a previous call (or, with
			// `set -u`, an "unbound variable" abort).
			// When every path returns explicitly we can skip the init.
			needsInit := !allPathsReturn(fd.Body.Stmts)
			switch r := ft.Result.(type) {
			case *types.Optional:
				if needsInit {
					g.writeLine(`__ret=""`)
					g.writeLine(`__ret__null=1`)
				}
				_ = r
			case *types.Record:
				if needsInit {
					for _, lf := range recordLeaves(r) {
						g.writeLine(fmt.Sprintf(`__ret__%s=""`, lf.Path))
						if _, isOpt := lf.Type.(*types.Optional); isOpt {
							g.writeLine(fmt.Sprintf(`__ret__%s__null=1`, lf.Path))
						}
					}
				}
			case *types.Sum:
				if needsInit {
					for _, lf := range sumLeaves(r) {
						g.writeLine(fmt.Sprintf(`__ret__%s=""`, lf.Path))
						if _, isOpt := lf.Type.(*types.Optional); isOpt {
							g.writeLine(fmt.Sprintf(`__ret__%s__null=1`, lf.Path))
						}
					}
				}
			default:
				if ft.Result != types.Void && needsInit {
					g.writeLine(`__ret=""`)
				}
			}
		}
	}
	prevDefers := g.currentFuncDefers
	prevHasDefers := g.currentFuncHasDefers
	defers := collectDefers(fd.Body)
	if len(defers) > 0 {
		g.usesDefer = true
		g.currentFuncDefers = map[*ast.DeferStmt]string{}
		for i, d := range defers {
			g.currentFuncDefers[d] = fmt.Sprintf("__tt_def_%s_%d", mangled, i)
		}
		g.currentFuncHasDefers = true
		g.writeLine(`local __tt_defstack=""`)
	} else {
		g.currentFuncDefers = nil
		g.currentFuncHasDefers = false
	}
	defer func() {
		g.currentReturnType = prevRet
		g.currentFuncDefers = prevDefers
		g.currentFuncHasDefers = prevHasDefers
	}()
	// Each parameter may consume one or more positional sh args:
	//   - record: one per field (recursing on optional fields below)
	//   - optional: two (value, then __null flag)
	//   - everything else: one
	pos := 1
	for _, p := range fd.Params {
		paramTy := g.paramType(fd, p.Name)
		switch t := paramTy.(type) {
		case *types.Record:
			for _, lf := range recordLeaves(t) {
				g.writeLine(fmt.Sprintf(`local %s__%s="$%d"`, shName(p.Name), lf.Path, pos))
				pos++
				if _, isOpt := lf.Type.(*types.Optional); isOpt {
					g.writeLine(fmt.Sprintf(`local %s__%s__null="$%d"`, shName(p.Name), lf.Path, pos))
					pos++
				}
			}
		case *types.Sum:
			for _, lf := range sumLeaves(t) {
				g.writeLine(fmt.Sprintf(`local %s__%s="$%d"`, shName(p.Name), lf.Path, pos))
				pos++
				if _, isOpt := lf.Type.(*types.Optional); isOpt {
					g.writeLine(fmt.Sprintf(`local %s__%s__null="$%d"`, shName(p.Name), lf.Path, pos))
					pos++
				}
			}
		case *types.Optional:
			name := shName(p.Name)
			g.writeLine("local " + name + `="$` + itoa(pos) + `"`)
			pos++
			g.writeLine("local " + name + `__null="$` + itoa(pos) + `"`)
			pos++
		default:
			_ = t
			g.writeLine("local " + shName(p.Name) + `="$` + itoa(pos) + `"`)
			pos++
		}
	}
	for _, s := range fd.Body.Stmts {
		g.emitStmt(s)
	}
	// Skip the fall-through defer-runner when every path already returns —
	// otherwise shellcheck flags the trailing line as unreachable (SC2317).
	if g.currentFuncHasDefers && !allPathsReturn(fd.Body.Stmts) {
		g.writeLine("__tt_run_defers")
	}
	g.indent--
	g.writeLine("}")

	if g.currentFuncHasDefers {
		g.writeLine("")
		for _, d := range defers {
			g.emitDeferHelper(g.currentFuncDefers[d], d)
		}
	}
}

// hasDeferIn reports whether the block tree transitively contains any
// DeferStmt. Used by emitModules to gate emission of the runtime helper.
func hasDeferIn(b *ast.Block) bool {
	if b == nil {
		return false
	}
	for _, s := range b.Stmts {
		if hasDeferInStmt(s) {
			return true
		}
	}
	return false
}

func hasDeferInStmt(s ast.Stmt) bool {
	switch s := s.(type) {
	case *ast.DeferStmt:
		return true
	case *ast.IfStmt:
		return hasDeferIn(s.Then) || hasDeferIn(s.Else)
	case *ast.ForStmt:
		return hasDeferIn(s.Body)
	case *ast.MatchStmt:
		for _, c := range s.Cases {
			if hasDeferIn(c.Body) {
				return true
			}
		}
	case *ast.Block:
		return hasDeferIn(s)
	}
	return false
}

// collectDefers returns every DeferStmt found anywhere in the block tree, in
// source order. Nested defer blocks themselves cannot contain defers (the
// checker forbids return inside a defer body — but we still walk in case
// future versions relax that).
func collectDefers(b *ast.Block) []*ast.DeferStmt {
	var out []*ast.DeferStmt
	collectDefersIn(b, &out)
	return out
}

func collectDefersIn(b *ast.Block, out *[]*ast.DeferStmt) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		switch s := s.(type) {
		case *ast.DeferStmt:
			*out = append(*out, s)
			collectDefersIn(s.Body, out)
		case *ast.IfStmt:
			collectDefersIn(s.Then, out)
			collectDefersIn(s.Else, out)
		case *ast.ForStmt:
			collectDefersIn(s.Body, out)
		case *ast.MatchStmt:
			for _, c := range s.Cases {
				collectDefersIn(c.Body, out)
			}
		case *ast.Block:
			collectDefersIn(s, out)
		}
	}
}

// emitDeferHelper emits a sh function whose body executes the deferred
// statements. The helper runs in a child scope that, by sh's dynamic-scoping
// of `local`, can read and modify the enclosing function's locals — that is
// exactly what `defer` semantics require.
func (g *Generator) emitDeferHelper(name string, d *ast.DeferStmt) {
	g.writeLine(name + "() {")
	g.indent++
	for _, s := range d.Body.Stmts {
		g.emitStmt(s)
	}
	g.indent--
	g.writeLine("}")
	g.writeLine("")
}

// allPathsReturn reports whether every execution path through stmts ends with
// a return statement. It is conservative: returns inside loops or match
// statements are not considered guaranteed.
func allPathsReturn(stmts []ast.Stmt) bool {
	for _, s := range stmts {
		switch s := s.(type) {
		case *ast.ReturnStmt:
			return true
		case *ast.IfStmt:
			if allPathsReturn(s.Then.Stmts) && s.Else != nil && allPathsReturn(s.Else.Stmts) {
				return true
			}
		case *ast.Block:
			if allPathsReturn(s.Stmts) {
				return true
			}
		}
	}
	return false
}

// paramType resolves the parameter's tartalo type by consulting the function
// symbol the checker recorded.
func (g *Generator) paramType(fd *ast.FuncDecl, paramName string) types.Type {
	sym := g.info.Decls[checker.MangledName(g.currentModule, fd.Name)]
	if sym == nil {
		return types.Invalid
	}
	ft, ok := sym.Type.(*types.Func)
	if !ok {
		return types.Invalid
	}
	for i, p := range fd.Params {
		if p.Name == paramName {
			if i < len(ft.Params) {
				return ft.Params[i]
			}
			return types.Invalid
		}
	}
	return types.Invalid
}

// --- statements -------------------------------------------------------------

func (g *Generator) emitStmt(s ast.Stmt) {
	if g.trace {
		if p := s.Pos(); p.File != "" && p.Line > 0 {
			// Skip the redundant case where Block forwards to its children:
			// the children will write their own __tt_loc, so writing one for
			// the wrapping block just adds noise.
			if _, ok := s.(*ast.Block); !ok {
				g.writeLine(fmt.Sprintf(`__tt_loc='%s:%d'`, escForSingleQuoted(p.File), p.Line))
			}
		}
	}
	switch s := s.(type) {
	case *ast.DeclStmt:
		g.emitVarDecl(s.Decl, true)
	case *ast.ExprStmt:
		g.emitExprStmt(s.X)
	case *ast.AssignStmt:
		g.emitAssign(s)
	case *ast.ReturnStmt:
		g.emitReturn(s)
	case *ast.IfStmt:
		g.emitIf(s)
	case *ast.ForStmt:
		g.emitFor(s)
	case *ast.Block:
		for _, st := range s.Stmts {
			g.emitStmt(st)
		}
	case *ast.MatchStmt:
		g.emitMatch(s)
	case *ast.FieldAssignStmt:
		g.emitFieldAssign(s)
	case *ast.DeferStmt:
		g.emitDeferPush(s)
	default:
		g.writeLine(fmt.Sprintf("# unsupported stmt: %T", s))
	}
}

// emitDeferPush appends the helper-function name for `s` to the current
// function's defer stack. The stack is colon-separated; head runs first, so
// new entries go at the front. The `:+:${...}` idiom skips the colon when
// the stack is empty, keeping the format unambiguous.
func (g *Generator) emitDeferPush(s *ast.DeferStmt) {
	name := g.currentFuncDefers[s]
	if name == "" {
		// Defensive: defer outside a function context — checker should have
		// rejected this, but emit a no-op rather than corrupt the script.
		return
	}
	g.writeLine(fmt.Sprintf(`__tt_defstack="%s${__tt_defstack:+:${__tt_defstack}}"`, name))
}

func (g *Generator) emitFieldAssign(s *ast.FieldAssignStmt) {
	tv := g.compileExpr(s.Target)
	g.writeLines(tv.prologue)
	target := tv.value + "__" + s.Name
	tt := g.info.Types[s.Target]
	rec, _ := tt.(*types.Record)
	var f *types.Field
	if rec != nil {
		f = rec.Lookup(s.Name)
	}
	// Record-typed field: write leaf-by-leaf into the target prefix, fast-pathing
	// a record-literal RHS.
	if f != nil {
		if subRec, ok := f.Type.(*types.Record); ok {
			if lit, ok := s.Value.(*ast.RecordLit); ok {
				lv := g.compileRecordLit(lit, target)
				g.writeLines(lv.prologue)
				return
			}
			v := g.compileExpr(s.Value)
			g.writeLines(v.prologue)
			for _, lf := range recordLeaves(subRec) {
				src := v.value + "__" + lf.Path
				dst := target + "__" + lf.Path
				g.writeLine(fmt.Sprintf(`%s="${%s}"`, dst, src))
				if _, isOpt := lf.Type.(*types.Optional); isOpt {
					g.writeLine(fmt.Sprintf(`%s__null="${%s__null}"`, dst, src))
				}
			}
			return
		}
	}
	v := g.compileExpr(s.Value)
	g.writeLines(v.prologue)
	if f != nil {
		if _, isOpt := f.Type.(*types.Optional); isOpt {
			nullExpr := v.nullCheck
			if nullExpr == "" {
				nullExpr = "0"
			}
			g.writeLine(fmt.Sprintf("%s__null=$((%s))", target, nullExpr))
		}
	}
	g.writeLine(fmt.Sprintf("%s=%s", target, v.assignmentRHS()))
}

func (g *Generator) emitMatch(s *ast.MatchStmt) {
	if sum, ok := g.info.Types[s.Subject].(*types.Sum); ok {
		g.emitMatchSum(s, sum)
		return
	}
	v := g.compileExpr(s.Subject)
	g.writeLines(v.prologue)
	subjectVar := g.tmp("subj")
	g.writeLine(fmt.Sprintf("%s=%s", subjectVar, v.assignmentRHS()))
	g.writeLine(fmt.Sprintf("case \"$%s\" in", subjectVar))
	g.indent++
	for _, arm := range s.Cases {
		pats := make([]string, 0, len(arm.Patterns))
		for _, pat := range arm.Patterns {
			pats = append(pats, patternToShCase(pat))
		}
		g.writeLine(strings.Join(pats, "|") + ")")
		g.indent++
		for _, st := range arm.Body.Stmts {
			g.emitStmt(st)
		}
		g.writeLine(";;")
		g.indent--
	}
	g.indent--
	g.writeLine("esac")
}

// emitMatchSum emits a `case` over the subject's tag leaf, with one arm per
// variant pattern. Inside each arm the listed bindings are materialised as
// local shell vars copied from the subject's variant-qualified slots, so
// the body can reference them by plain name.
func (g *Generator) emitMatchSum(s *ast.MatchStmt, sum *types.Sum) {
	v := g.compileExpr(s.Subject)
	g.writeLines(v.prologue)
	subjPrefix := v.value
	g.writeLine(fmt.Sprintf(`case "${%s__tag}" in`, subjPrefix))
	g.indent++
	for _, arm := range s.Cases {
		pats := make([]string, 0, len(arm.Patterns))
		hasWild := false
		// All patterns within a single arm share the body, so any bindings
		// must be consistent. We collect the first variant pattern's binding
		// list as authoritative; the checker has already enforced shape.
		var bindings []ast.VariantBinding
		var bindVariant string
		for _, pat := range arm.Patterns {
			switch p := pat.(type) {
			case *ast.WildcardPattern:
				hasWild = true
				pats = append(pats, "*")
			case *ast.VariantPattern:
				pats = append(pats, "'"+p.Name+"'")
				if bindVariant == "" {
					bindVariant = p.Name
					bindings = p.Bindings
				}
			}
		}
		if hasWild {
			g.writeLine("*)")
		} else {
			g.writeLine(strings.Join(pats, "|") + ")")
		}
		g.indent++
		// Materialise variant-field bindings as local shell vars before the
		// arm body so user code can reference them by plain name.
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
					src := subjPrefix + "__" + variant.Name + "__" + b.Name
					dst := shName(b.Name)
					g.writeLine(fmt.Sprintf(`local %s="${%s}"`, dst, src))
					if _, isOpt := fld.Type.(*types.Optional); isOpt {
						g.writeLine(fmt.Sprintf(`local %s__null="${%s__null}"`, dst, src))
					}
				}
			}
		}
		for _, st := range arm.Body.Stmts {
			g.emitStmt(st)
		}
		g.writeLine(";;")
		g.indent--
	}
	g.indent--
	g.writeLine("esac")
}

// patternToShCase renders a tartalo pattern as a sh case-pattern. We single-
// quote string and integer patterns so glob metacharacters match literally.
func patternToShCase(p ast.Pattern) string {
	switch p := p.(type) {
	case *ast.WildcardPattern:
		return "*"
	case *ast.LiteralPattern:
		switch lit := p.Lit.(type) {
		case *ast.IntLit:
			return shSingleQuote(fmt.Sprintf("%d", lit.Value))
		case *ast.BoolLit:
			if lit.Value {
				return "'1'"
			}
			return "'0'"
		case *ast.StringLit:
			var raw strings.Builder
			for _, part := range lit.Parts {
				if c, ok := part.(*ast.StringChunk); ok {
					raw.WriteString(c.Value)
				}
			}
			return shSingleQuote(raw.String())
		}
	}
	return "*"
}

// shSingleQuote wraps `s` in single quotes, escaping any embedded single-quote
// using the standard `'\”` close-reopen idiom. The resulting string matches
// `s` literally inside any sh context that interprets glob metacharacters.
func shSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// tryEmitDirectRet checks whether e is a bare function call whose return
// value can be read straight from __ret without a temp. When true it emits
// the call prologue (minus the temp snapshot) and the assignment, then
// returns true so the caller knows it's done.
func (g *Generator) tryEmitDirectRet(e ast.Expr, target, declPrefix string) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	v := g.compileCall(call, false)
	if v.form != formArith && v.form != formBool && v.form != formStr {
		return false
	}
	if len(v.prologue) == 0 {
		return false
	}
	last := v.prologue[len(v.prologue)-1]
	if !strings.HasPrefix(last, "__ret") || !strings.HasSuffix(last, `="$__ret"`) {
		return false
	}
	g.writeLines(v.prologue[:len(v.prologue)-1])
	g.writeLine(declPrefix + target + `="$__ret"`)
	return true
}

// tryEmitInlineArray checks whether e is an array literal whose temp can
// be elided and the body assigned directly to the target variable.
func (g *Generator) tryEmitInlineArray(e ast.Expr, target, declPrefix string) bool {
	_, ok := e.(*ast.ArrayLit)
	if !ok {
		return false
	}
	v := g.compileExpr(e)
	if len(v.prologue) == 0 {
		return false
	}
	last := v.prologue[len(v.prologue)-1]
	idx := strings.Index(last, `="`)
	if idx == -1 || last[len(last)-1] != '"' {
		return false
	}
	g.writeLines(v.prologue[:len(v.prologue)-1])
	body := last[idx+2 : len(last)-1]
	g.writeLine(declPrefix + target + `="` + body + `"`)
	return true
}

func (g *Generator) emitVarDecl(d *ast.VarDecl, local bool) {
	declPrefix := ""
	if d.IsConst {
		declPrefix = "readonly "
	} else if local {
		declPrefix = "local "
	}
	// Top-level globals get the module prefix so two modules can declare a
	// `let counter` without collision in the bundled output.
	target := shName(d.Name)
	if !local {
		target = shName(checker.MangledName(g.currentModule, d.Name))
	}
	// Spot the structural shape of the binding so we know whether to fan it
	// out into multiple shell variables. The declared annotation wins — if
	// the user wrote `: T?` or `: Record`, the binding has that shape even
	// when the initializer is plainly typed.
	if isOptionalTypeAnn(d.TypeAnn) || isOptionalValueType(g.info.Types[d.Value]) {
		g.emitOptVarDecl(d, target, declPrefix)
		return
	}
	if rec, ok := g.info.Types[d.Value].(*types.Record); ok {
		g.emitRecordCopy(target, declPrefix, rec, d.Value)
		return
	}
	if sum, ok := g.info.Types[d.Value].(*types.Sum); ok {
		g.emitSumCopy(target, declPrefix, sum, d.Value)
		return
	}
	if g.tryEmitDirectRet(d.Value, target, declPrefix) {
		return
	}
	if g.tryEmitInlineArray(d.Value, target, declPrefix) {
		return
	}
	// Fast path for simple literals — skip compileExpr entirely.
	switch val := d.Value.(type) {
	case *ast.IntLit:
		g.writeLine(declPrefix + target + "=" + fmt.Sprintf("%d", val.Value))
		return
	case *ast.BoolLit:
		v := "0"
		if val.Value {
			v = "1"
		}
		g.writeLine(declPrefix + target + "=" + v)
		return
	case *ast.StringLit:
		if len(val.Parts) == 0 {
			g.writeLine(declPrefix + target + `=""`)
			return
		}
		if len(val.Parts) == 1 {
			if chunk, ok := val.Parts[0].(*ast.StringChunk); ok {
				g.writeLine(declPrefix + target + `="` + escapeForDoubleQuoted(chunk.Value) + `"`)
				return
			}
		}
	}
	v := g.compileExpr(d.Value)
	g.writeLines(v.prologue)
	g.writeLine(fmt.Sprintf("%s%s=%s", declPrefix, target, v.assignmentRHS()))
}

// emitOptVarDecl emits both the value and the __null sidecar for an
// optional-typed variable declaration. Auto-wraps when the RHS is a
// non-optional value of the underlying type.
func (g *Generator) emitOptVarDecl(d *ast.VarDecl, target, declPrefix string) {
	v := g.compileExpr(d.Value)
	g.writeLines(v.prologue)
	nullCheck := v.nullCheck
	if nullCheck == "" {
		// RHS is a non-optional value being auto-wrapped. It is never null.
		nullCheck = "0"
	}
	g.writeLine(fmt.Sprintf("%s%s__null=$((%s))", declPrefix, target, nullCheck))
	g.writeLine(fmt.Sprintf("%s%s=%s", declPrefix, target, v.assignmentRHS()))
}

// isOptionalTypeAnn reports whether a type annotation is `T?`.
func isOptionalTypeAnn(t ast.TypeExpr) bool {
	_, ok := t.(*ast.OptionalType)
	return ok
}

// isOptionalValueType reports whether a (possibly nil) checker type is an
// Optional. nil values count as not-optional.
func isOptionalValueType(t types.Type) bool {
	if t == nil {
		return false
	}
	_, ok := t.(*types.Optional)
	return ok
}

func (g *Generator) emitAssign(s *ast.AssignStmt) {
	sym := g.info.Assigns[s]
	target := shName(s.Name)
	if sym != nil && sym.Module != nil {
		target = shName(checker.MangledName(sym.Module, sym.Name))
	}
	if rec, ok := g.info.Types[s.Value].(*types.Record); ok {
		g.emitRecordCopy(target, "", rec, s.Value)
		return
	}
	if sum, ok := g.info.Types[s.Value].(*types.Sum); ok {
		g.emitSumCopy(target, "", sum, s.Value)
		return
	}
	if sym != nil {
		if _, isOpt := sym.Type.(*types.Optional); isOpt {
			v := g.compileExpr(s.Value)
			g.writeLines(v.prologue)
			nullExpr := v.nullCheck
			if nullExpr == "" {
				nullExpr = "0"
			}
			g.writeLine(target + "__null=$((" + nullExpr + "))")
			g.writeLine(target + "=" + v.assignmentRHS())
			return
		}
	}
	if g.tryEmitDirectRet(s.Value, target, "") {
		return
	}
	v := g.compileExpr(s.Value)
	g.writeLines(v.prologue)
	g.writeLine(target + "=" + v.assignmentRHS())
}

func (g *Generator) emitReturn(s *ast.ReturnStmt) {
	if s.Value == nil {
		if g.currentFuncHasDefers {
			g.writeLine("__tt_run_defers")
		}
		g.writeLine("return 0")
		return
	}
	if rec, ok := g.info.Types[s.Value].(*types.Record); ok {
		g.emitRecordCopy("__ret", "", rec, s.Value)
		if g.currentFuncHasDefers {
			g.writeLine("__tt_run_defers")
		}
		g.writeLine("return 0")
		return
	}
	if sum, ok := g.info.Types[s.Value].(*types.Sum); ok {
		g.emitSumCopy("__ret", "", sum, s.Value)
		if g.currentFuncHasDefers {
			g.writeLine("__tt_run_defers")
		}
		g.writeLine("return 0")
		return
	}
	if g.tryEmitDirectRet(s.Value, "__ret", "") {
		if g.currentFuncHasDefers {
			g.writeLine("__tt_run_defers")
		}
		g.writeLine("return 0")
		return
	}
	v := g.compileExpr(s.Value)
	g.writeLines(v.prologue)
	if (v.form == formArith || v.form == formBool) && isSimpleIdent(v.value) {
		g.writeLine("__ret=$" + v.value)
	} else {
		g.writeLine(fmt.Sprintf("__ret=%s", v.assignmentRHS()))
	}
	// If the function's return type is optional, also propagate the null flag.
	// We detect this by whether the value carries one OR the function's
	// declared return type is optional and we're auto-wrapping a non-optional.
	if _, retIsOpt := g.currentReturnType.(*types.Optional); retIsOpt {
		nullExpr := v.nullCheck
		if nullExpr == "" {
			nullExpr = "0"
		}
		g.writeLine(fmt.Sprintf("__ret__null=$((%s))", nullExpr))
	}
	if g.currentFuncHasDefers {
		g.writeLine("__tt_run_defers")
	}
	g.writeLine("return 0")
}

func (g *Generator) emitIf(s *ast.IfStmt) {
	cond := g.compileCond(s.Cond)
	g.writeLines(cond.prologue)
	g.writeLine("if " + cond.test + "; then")
	g.indent++
	if len(s.Then.Stmts) == 0 {
		// sh requires at least one command in `then`. `:` is the POSIX no-op.
		g.writeLine(":")
	} else {
		for _, st := range s.Then.Stmts {
			g.emitStmt(st)
		}
	}
	g.indent--
	if s.Else != nil {
		g.writeLine("else")
		g.indent++
		if len(s.Else.Stmts) == 0 {
			g.writeLine(":")
		} else {
			for _, st := range s.Else.Stmts {
				g.emitStmt(st)
			}
		}
		g.indent--
	}
	g.writeLine("fi")
}

func (g *Generator) emitFor(s *ast.ForStmt) {
	switch iter := s.Iter.(type) {
	case *ast.RangeExpr:
		g.emitForRange(s, iter)
	default:
		// Array of records: each row is a US-separated record. Bind the
		// loop variable's leaves on each iteration.
		if arr, ok := g.info.Types[s.Iter].(*types.Array); ok {
			if rec, ok := arr.Elem.(*types.Record); ok {
				g.emitForArrayOfRecord(s, rec)
				return
			}
		}
		g.emitForLines(s)
	}
}

// emitForArrayOfRecord emits a loop that iterates rows of an array-of-record
// value, binding the loop variable's leaf slots once per row via parameter-
// expansion splitting. Skipped entirely when the array is empty so we don't
// process a spurious empty-row.
func (g *Generator) emitForArrayOfRecord(s *ast.ForStmt, rec *types.Record) {
	g.usesRecordArrays = true
	v := g.compileExpr(s.Iter)
	g.writeLines(v.prologue)
	linesVar := ""
	if id, ok := s.Iter.(*ast.Ident); ok {
		name := shName(id.Name)
		if sym := g.info.Uses[id]; sym != nil && sym.Module != nil {
			name = shName(checker.MangledName(sym.Module, sym.Name))
		}
		linesVar = name
	} else {
		linesVar = g.tmp("lines")
		g.writeLine(fmt.Sprintf("%s=%s", linesVar, v.assignmentRHS()))
	}
	rowVar := g.tmp("row")
	leaves := recordLeaves(rec)
	slots := rowSlots(shName(s.Var), leaves)
	g.writeLine(fmt.Sprintf("if [ -n \"$%s\" ]; then", linesVar))
	g.indent++
	g.writeLine(fmt.Sprintf("while IFS= read -r %s; do", rowVar))
	g.indent++
	var split []string
	split = g.emitRowSplit(split, rowVar, slots)
	g.writeLines(split)
	for _, st := range s.Body.Stmts {
		g.emitStmt(st)
	}
	g.indent--
	g.writeLine("done <<__TARTALO_LINES__")
	g.out.WriteString(fmt.Sprintf("$%s\n", linesVar))
	g.out.WriteString("__TARTALO_LINES__\n")
	g.indent--
	g.writeLine("fi")
}

func (g *Generator) emitForRange(s *ast.ForStmt, r *ast.RangeExpr) {
	start := g.compileExpr(r.Start)
	end := g.compileExpr(r.End)
	g.writeLines(start.prologue)
	g.writeLines(end.prologue)
	vname := shName(s.Var)
	g.writeLine(vname + "=" + start.assignmentRHS())
	// Inline simple end bounds (constants or bare identifiers) to avoid an
	// extra temp variable. Complex expressions still need a temp.
	if lit, ok := r.End.(*ast.IntLit); ok {
		g.writeLine("while [ \"$" + vname + "\" -lt " + itoa64(lit.Value) + " ]; do")
	} else if id, ok := r.End.(*ast.Ident); ok {
		name := shName(id.Name)
		if sym := g.info.Uses[id]; sym != nil && sym.Module != nil {
			name = shName(checker.MangledName(sym.Module, sym.Name))
		}
		g.writeLine("while [ \"$" + vname + "\" -lt \"$" + name + "\" ]; do")
	} else {
		endVar := g.tmp("end")
		g.writeLine(endVar + "=" + end.assignmentRHS())
		g.writeLine("while [ \"$" + vname + "\" -lt \"$" + endVar + "\" ]; do")
	}
	g.indent++
	for _, st := range s.Body.Stmts {
		g.emitStmt(st)
	}
	g.writeLine(vname + "=$((" + vname + " + 1))")
	g.indent--
	g.writeLine("done")
}

func (g *Generator) emitForLines(s *ast.ForStmt) {
	v := g.compileExpr(s.Iter)
	g.writeLines(v.prologue)
	// For a simple string variable we can use it directly in the heredoc
	// instead of copying into a temp first.
	linesVar := ""
	if id, ok := s.Iter.(*ast.Ident); ok {
		name := shName(id.Name)
		if sym := g.info.Uses[id]; sym != nil && sym.Module != nil {
			name = shName(checker.MangledName(sym.Module, sym.Name))
		}
		linesVar = name
	} else {
		linesVar = g.tmp("lines")
		g.writeLine(fmt.Sprintf("%s=%s", linesVar, v.assignmentRHS()))
	}
	g.writeLine(fmt.Sprintf("if [ -n \"$%s\" ]; then", linesVar))
	g.indent++
	g.writeLine(fmt.Sprintf("while IFS= read -r %s; do", shName(s.Var)))
	g.indent++
	for _, st := range s.Body.Stmts {
		g.emitStmt(st)
	}
	g.indent--
	g.writeLine(fmt.Sprintf("done <<__TARTALO_LINES__"))
	// heredoc bodies are not indented
	g.out.WriteString(fmt.Sprintf("$%s\n", linesVar))
	g.out.WriteString("__TARTALO_LINES__\n")
	g.indent--
	g.writeLine("fi")
}

func (g *Generator) emitExprStmt(x ast.Expr) {
	// Special cases that have no value: command literals, void calls.
	switch x := x.(type) {
	case *ast.CmdLit:
		// run for side effects, no capture
		cmd, prologue := g.compileCmdString(x)
		g.writeLines(prologue)
		g.writeLine(cmd)
		return
	case *ast.CallExpr:
		v := g.compileCall(x, true)
		g.writeLines(v.prologue)
		// builtins that compile to a single statement put it in prologue and
		// leave value empty; for non-void calls used as a statement we just
		// drop the result. No extra output needed.
		return
	}
	v := g.compileExpr(x)
	g.writeLines(v.prologue)
	// Discarded values need no runtime action.
}

// --- expression compilation -------------------------------------------------

// exprValue is the compiled form of an expression: a value reference plus the
// prologue (in source order) needed to make that reference valid.
type exprValue struct {
	prologue []string
	// value is a string in one of three forms depending on form:
	//   formStr   — a sh string suitable inside double-quotes, e.g. `${x}foo` or `Hello`
	//   formArith — a raw arithmetic expression, e.g. `x + 2 * y`
	//   formBool  — a 0/1 arithmetic expression evaluating the bool
	value string
	form  exprForm
	// nullCheck is a sh arithmetic expression that evaluates to 1 if the
	// expression's value is null, else 0. It is set iff the expression has an
	// optional type. For non-optional values it is the empty string.
	nullCheck string
}

type exprForm int

const (
	formStr    exprForm = iota // a quotable string fragment
	formArith                  // a raw arithmetic expression (no leading `$((`)
	formBool                   // a 0/1 arithmetic expression
	formRecord                 // value is a name prefix; the record's fields live as `prefix__<field>`
)

// assignmentRHS yields the right-hand-side text for a `var=...` assignment.
func (v exprValue) assignmentRHS() string {
	switch v.form {
	case formArith, formBool:
		if isSimpleAtom(v.value) {
			return v.value
		}
		return "$((" + v.value + "))"
	default:
		return shQuoteDouble(v.value)
	}
}

// shString returns a fragment suitable for embedding inside an enclosing
// double-quoted string. For arithmetic forms this is `$((expr))`, for string
// forms it is the literal value.
func (v exprValue) shString() string {
	switch v.form {
	case formArith, formBool:
		if isIntLiteral(v.value) {
			return v.value
		}
		return "$((" + v.value + "))"
	default:
		return v.value
	}
}

// isSimpleAtom reports whether the arithmetic value is just a literal int —
// in that case we can use it raw on the RHS without `$(( ))` wrapping.
func isSimpleAtom(s string) bool {
	return isIntLiteral(s)
}

func isIntLiteral(s string) bool {
	if s == "" {
		return false
	}
	i := 0
	if s[0] == '-' {
		if len(s) == 1 {
			return false
		}
		i = 1
	}
	for ; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func (g *Generator) compileExpr(e ast.Expr) exprValue {
	switch e := e.(type) {
	case *ast.IntLit:
		return exprValue{value: itoa64(e.Value), form: formArith}
	case *ast.FloatLit:
		// Floats live as plain shell strings; awk handles arithmetic on them.
		return exprValue{value: e.Text, form: formStr}
	case *ast.BoolLit:
		v := "0"
		if e.Value {
			v = "1"
		}
		return exprValue{value: v, form: formBool}
	case *ast.NullLit:
		// Untyped null. The value form is irrelevant — every consumer that
		// cares about an optional checks `nullCheck` first. We use "" so the
		// generated sh stays valid in both string and arithmetic contexts.
		return exprValue{value: "", form: formStr, nullCheck: "1"}
	case *ast.Ident:
		return g.compileIdent(e)
	case *ast.StringChunk:
		return exprValue{value: e.Value, form: formStr}
	case *ast.StringLit:
		return g.compileStringLit(e)
	case *ast.CmdLit:
		return g.compileCmdLit(e)
	case *ast.UnaryExpr:
		return g.compileUnary(e)
	case *ast.BinaryExpr:
		return g.compileBinary(e)
	case *ast.CallExpr:
		return g.compileCall(e, false)
	case *ast.ArrayLit:
		return g.compileArrayLit(e)
	case *ast.IndexExpr:
		return g.compileIndex(e)
	case *ast.RecordLit:
		return g.compileRecordLit(e, "")
	case *ast.FieldExpr:
		return g.compileFieldExpr(e)
	case *ast.CoalesceExpr:
		return g.compileCoalesce(e)
	case *ast.UnwrapExpr:
		return g.compileUnwrap(e)
	case *ast.TryExpr:
		return g.compileTry(e)
	}
	return exprValue{value: fmt.Sprintf("# unsupported expr: %T", e), form: formStr}
}

// compileTry lowers `expr?`. If the operand carries the Err tag, the
// enclosing function returns immediately, propagating the Err into the
// caller's __ret. Otherwise the result of the expression is the Ok variant's
// `value` payload.
func (g *Generator) compileTry(e *ast.TryExpr) exprValue {
	v := g.compileExpr(e.Operand)
	prologue := append([]string{}, v.prologue...)
	opSum, _ := g.info.Types[e.Operand].(*types.Sum)
	if opSum == nil {
		return exprValue{prologue: prologue, value: "", form: formStr}
	}
	retSum, _ := g.currentReturnType.(*types.Sum)
	prefix := v.value
	// On Err: synthesise a Result-shaped __ret carrying the same Err.
	prologue = append(prologue, fmt.Sprintf(`if [ "${%s__tag}" = "Err" ]; then`, prefix))
	if retSum != nil {
		// Init all leaves of the return sum to safe defaults.
		for _, lf := range sumLeaves(retSum) {
			if lf.Path == "tag" {
				continue
			}
			prologue = append(prologue, fmt.Sprintf(`  __ret__%s=""`, lf.Path))
			if _, isOpt := lf.Type.(*types.Optional); isOpt {
				prologue = append(prologue, fmt.Sprintf(`  __ret__%s__null=1`, lf.Path))
			}
		}
		prologue = append(prologue, `  __ret__tag="Err"`)
		prologue = append(prologue, fmt.Sprintf(`  __ret__Err__error="${%s__Err__error}"`, prefix))
	}
	if g.currentFuncHasDefers {
		prologue = append(prologue, "  __tt_run_defers")
	}
	prologue = append(prologue, "  return 0")
	prologue = append(prologue, "fi")
	// Fall-through: the operand was Ok; the result is the Ok variant's value.
	okV := opSum.LookupVariant("Ok")
	if okV == nil || len(okV.Fields) != 1 {
		return exprValue{prologue: prologue, value: "", form: formStr}
	}
	valVar := prefix + "__Ok__value"
	switch okV.Fields[0].Type {
	case types.Number, types.Bool:
		return exprValue{prologue: prologue, value: valVar, form: formArith}
	default:
		return exprValue{prologue: prologue, value: "${" + valVar + "}", form: formStr}
	}
}

func (g *Generator) compileArrayLit(a *ast.ArrayLit) exprValue {
	if len(a.Elems) == 0 {
		return exprValue{value: "", form: formStr}
	}
	// Array of records: rows separated by newline, fields by ASCII US.
	if arrTy, ok := g.info.Types[a].(*types.Array); ok {
		if rec, ok := arrTy.Elem.(*types.Record); ok {
			return g.compileArrayLitOfRecord(a, rec)
		}
	}
	// Build the array as a newline-joined string. We materialise it via a temp
	// using `printf` so newlines are preserved exactly.
	t := g.tmp("arr")
	var prologue []string
	parts := make([]string, len(a.Elems))
	for i, el := range a.Elems {
		v := g.compileExpr(el)
		prologue = append(prologue, v.prologue...)
		parts[i] = v.shString()
	}
	// Concatenate elements with a literal newline separator. Using a sh
	// here-doc-style string keeps the codegen one-line per join.
	var body strings.Builder
	for i, p := range parts {
		if i > 0 {
			body.WriteByte('\n')
		}
		body.WriteString(p)
	}
	prologue = append(prologue, fmt.Sprintf("%s=\"%s\"", t, body.String()))
	return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
}

// compileArrayLitOfRecord renders `[Rec{...}, Rec{...}]` as a single shell
// variable holding one row per record, US-separated leaf fields per row.
func (g *Generator) compileArrayLitOfRecord(a *ast.ArrayLit, rec *types.Record) exprValue {
	g.usesRecordArrays = true
	leaves := recordLeaves(rec)
	var prologue []string
	rows := make([]string, len(a.Elems))
	for i, el := range a.Elems {
		v := g.compileExpr(el)
		prologue = append(prologue, v.prologue...)
		rows[i] = recordRowExpr(v.value, leaves)
	}
	t := g.tmp("arr")
	var body strings.Builder
	for i, r := range rows {
		if i > 0 {
			body.WriteByte('\n')
		}
		body.WriteString(r)
	}
	prologue = append(prologue, fmt.Sprintf(`%s="%s"`, t, body.String()))
	return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
}

// recordRowExpr returns a double-quoted-string snippet that interpolates the
// leaves of a record value (rooted at `prefix`) joined by ${__tt_us}, with
// optional leaves followed immediately by their __null sidecar.
func recordRowExpr(prefix string, leaves []leafField) string {
	var b strings.Builder
	first := true
	for _, lf := range leaves {
		if !first {
			b.WriteString("${__tt_us}")
		}
		first = false
		b.WriteString("${")
		b.WriteString(prefix)
		b.WriteString("__")
		b.WriteString(lf.Path)
		b.WriteByte('}')
		if _, isOpt := lf.Type.(*types.Optional); isOpt {
			b.WriteString("${__tt_us}")
			b.WriteString("${")
			b.WriteString(prefix)
			b.WriteString("__")
			b.WriteString(lf.Path)
			b.WriteString("__null}")
		}
	}
	return b.String()
}

// rowSlots returns the linear list of shell-variable names that one row of
// an array-of-record must populate, in left-to-right order. Optional leaves
// contribute (value, __null) pairs.
func rowSlots(prefix string, leaves []leafField) []string {
	var out []string
	for _, lf := range leaves {
		out = append(out, prefix+"__"+lf.Path)
		if _, isOpt := lf.Type.(*types.Optional); isOpt {
			out = append(out, prefix+"__"+lf.Path+"__null")
		}
	}
	return out
}

// emitRowSplit appends prologue lines that split a single US-separated row
// (held in shell variable rowVar) into the listed slot variables, using
// POSIX parameter expansion. The last slot gets the unsplit remainder.
//
// The US byte ${__tt_us} is quoted inside the parameter pattern so it is
// treated as a literal — both for shellcheck (SC2295) and to make intent
// explicit, even though the byte is not itself a glob metacharacter.
func (g *Generator) emitRowSplit(prologue []string, rowVar string, slots []string) []string {
	rest := rowVar
	for i, slot := range slots {
		if i == len(slots)-1 {
			prologue = append(prologue, fmt.Sprintf(`%s="${%s}"`, slot, rest))
			break
		}
		prologue = append(prologue, fmt.Sprintf(`%s="${%s%%%%"${__tt_us}"*}"`, slot, rest))
		newRest := g.tmp("rst")
		prologue = append(prologue, fmt.Sprintf(`%s="${%s#*"${__tt_us}"}"`, newRest, rest))
		rest = newRest
	}
	return prologue
}

// compileCoalesce lowers `lhs ?? rhs`. The result is a non-optional value of
// the lhs's element type. We synthesise a temp that gets the underlying lhs
// value when non-null, otherwise the rhs default.
func (g *Generator) compileCoalesce(e *ast.CoalesceExpr) exprValue {
	lv := g.compileExpr(e.Lhs)
	rv := g.compileExpr(e.Rhs)
	prologue := concatPrologues(lv.prologue, rv.prologue)
	tmp := g.tmp("co")
	// `lv.nullCheck` is set since the checker requires lhs to be optional.
	prologue = append(prologue, fmt.Sprintf(
		`if [ "$%s" = 1 ]; then %s=%s; else %s=%s; fi`,
		lv.nullCheck, tmp, rv.assignmentRHS(), tmp, lv.assignmentRHS()))
	// The result form depends on the underlying element type. Since the
	// checker has already narrowed both sides to the same T, lv.form
	// describes T accurately.
	switch lv.form {
	case formArith, formBool:
		return exprValue{prologue: prologue, value: tmp, form: lv.form}
	default:
		return exprValue{prologue: prologue, value: "${" + tmp + "}", form: formStr}
	}
}

// compileUnwrap lowers `expr!`. If the operand is null the script aborts with
// a diagnostic; otherwise the result is the non-optional value. We use `if`
// rather than `&&` so the test's exit status doesn't fight `set -e`.
func (g *Generator) compileUnwrap(e *ast.UnwrapExpr) exprValue {
	v := g.compileExpr(e.Operand)
	prologue := append([]string{}, v.prologue...)
	prologue = append(prologue, fmt.Sprintf(
		`if [ "$%s" = 1 ]; then printf 'tartalo: forced unwrap of null at %s\n' >&2; exit 1; fi`,
		v.nullCheck, e.OpPos))
	return exprValue{
		prologue: prologue,
		value:    v.value,
		form:     v.form,
	}
}

// compileRecordLit emits the assignments for a record literal. If prefix is
// non-empty, fields are written directly into `prefix__<f>=...` (used when
// the literal is the immediate RHS of a record-typed declaration / return).
// Otherwise a fresh temp prefix is allocated and returned as the value.
func (g *Generator) compileRecordLit(e *ast.RecordLit, prefix string) exprValue {
	// `Foo{...}` may be a sum-variant construction rather than a record
	// literal; the checker stamps the result type with the parent sum.
	if sum, ok := g.info.Types[e].(*types.Sum); ok {
		return g.compileVariantLit(e, sum, prefix)
	}
	rec, _ := g.info.Types[e].(*types.Record)
	if rec == nil {
		return exprValue{value: "# bad record literal", form: formStr}
	}
	if prefix == "" {
		prefix = g.tmp("rec")
	}
	var prologue []string
	g.appendRecordLitFields(&prologue, rec, e, prefix)
	return exprValue{prologue: prologue, value: prefix, form: formRecord}
}

// compileVariantLit emits the slot assignments for a variant literal of the
// form `Foo{...}` whose result type is the given sum. The tag leaf is set
// to the variant name, the variant's payload fields are filled from the
// literal, and every other variant's payload slots are zero-initialised so
// the value carries a complete shape.
func (g *Generator) compileVariantLit(e *ast.RecordLit, sum *types.Sum, prefix string) exprValue {
	v := sum.LookupVariant(e.TypeName)
	if v == nil {
		return exprValue{value: "# unknown variant", form: formStr}
	}
	if prefix == "" {
		prefix = g.tmp("sum")
	}
	var prologue []string
	prologue = append(prologue, fmt.Sprintf(`%s__tag="%s"`, prefix, v.Name))
	// Initialise all variant slots so consumers under `set -u` can read any
	// of them safely; the active variant's slots get overwritten next.
	for _, av := range sum.Variants {
		for _, f := range av.Fields {
			slot := prefix + "__" + av.Name + "__" + f.Name
			prologue = append(prologue, fmt.Sprintf(`%s=""`, slot))
			if _, isOpt := f.Type.(*types.Optional); isOpt {
				prologue = append(prologue, fmt.Sprintf(`%s__null=1`, slot))
			}
		}
	}
	// Emit values for the active variant's fields, in declared order.
	for _, f := range v.Fields {
		var init *ast.FieldInit
		for i := range e.Fields {
			if e.Fields[i].Name == f.Name {
				init = &e.Fields[i]
				break
			}
		}
		slot := prefix + "__" + v.Name + "__" + f.Name
		if init == nil {
			continue
		}
		val := g.compileExpr(init.Value)
		prologue = append(prologue, val.prologue...)
		prologue = append(prologue, fmt.Sprintf("%s=%s", slot, val.assignmentRHS()))
		if _, isOpt := f.Type.(*types.Optional); isOpt {
			nullExpr := val.nullCheck
			if nullExpr == "" {
				nullExpr = "0"
			}
			prologue = append(prologue, fmt.Sprintf(`%s__null=$((%s))`, slot, nullExpr))
		}
	}
	return exprValue{prologue: prologue, value: prefix, form: formRecord}
}

// compileUnitVariant emits a fresh sum value carrying the given unit variant.
// Used when an Ident resolves to a unit-variant constructor symbol.
func (g *Generator) compileUnitVariant(sum *types.Sum, variantName, prefix string) exprValue {
	if prefix == "" {
		prefix = g.tmp("sum")
	}
	var prologue []string
	prologue = append(prologue, fmt.Sprintf(`%s__tag="%s"`, prefix, variantName))
	for _, av := range sum.Variants {
		for _, f := range av.Fields {
			slot := prefix + "__" + av.Name + "__" + f.Name
			prologue = append(prologue, fmt.Sprintf(`%s=""`, slot))
			if _, isOpt := f.Type.(*types.Optional); isOpt {
				prologue = append(prologue, fmt.Sprintf(`%s__null=1`, slot))
			}
		}
	}
	return exprValue{prologue: prologue, value: prefix, form: formRecord}
}

// appendRecordLitFields writes the per-field initializers for `e` into the
// shell prefix `prefix`. When a field is itself a record, it recurses (record
// literal RHS) or copies leaf-by-leaf (any other record-valued expression).
func (g *Generator) appendRecordLitFields(prologue *[]string, rec *types.Record, e *ast.RecordLit, prefix string) {
	for _, f := range rec.Fields {
		var init *ast.FieldInit
		for i := range e.Fields {
			if e.Fields[i].Name == f.Name {
				init = &e.Fields[i]
				break
			}
		}
		fieldVar := prefix + "__" + f.Name
		if subRec, ok := f.Type.(*types.Record); ok {
			if init == nil {
				// The checker forbids missing fields, but be defensive: zero
				// every leaf so the generated script doesn't reference unset
				// vars under `set -u`.
				for _, lf := range recordLeaves(subRec) {
					*prologue = append(*prologue, fmt.Sprintf(`%s__%s=""`, fieldVar, lf.Path))
					if _, isOpt := lf.Type.(*types.Optional); isOpt {
						*prologue = append(*prologue, fmt.Sprintf(`%s__%s__null=1`, fieldVar, lf.Path))
					}
				}
				continue
			}
			if subLit, ok := init.Value.(*ast.RecordLit); ok {
				g.appendRecordLitFields(prologue, subRec, subLit, fieldVar)
				continue
			}
			v := g.compileExpr(init.Value)
			*prologue = append(*prologue, v.prologue...)
			for _, lf := range recordLeaves(subRec) {
				*prologue = append(*prologue,
					fmt.Sprintf(`%s__%s="${%s__%s}"`, fieldVar, lf.Path, v.value, lf.Path))
				if _, isOpt := lf.Type.(*types.Optional); isOpt {
					*prologue = append(*prologue,
						fmt.Sprintf(`%s__%s__null="${%s__%s__null}"`, fieldVar, lf.Path, v.value, lf.Path))
				}
			}
			continue
		}
		if init == nil {
			*prologue = append(*prologue, fmt.Sprintf(`%s=""`, fieldVar))
			if _, isOpt := f.Type.(*types.Optional); isOpt {
				*prologue = append(*prologue, fmt.Sprintf(`%s__null=1`, fieldVar))
			}
			continue
		}
		v := g.compileExpr(init.Value)
		*prologue = append(*prologue, v.prologue...)
		*prologue = append(*prologue, fmt.Sprintf("%s=%s", fieldVar, v.assignmentRHS()))
		if _, isOpt := f.Type.(*types.Optional); isOpt {
			nullExpr := v.nullCheck
			if nullExpr == "" {
				nullExpr = "0"
			}
			*prologue = append(*prologue, fmt.Sprintf("%s__null=$((%s))", fieldVar, nullExpr))
		}
	}
}

func (g *Generator) compileFieldExpr(e *ast.FieldExpr) exprValue {
	tv := g.compileExpr(e.Target)
	fieldVar := tv.value + "__" + e.Name
	if _, isRec := g.info.Types[e].(*types.Record); isRec {
		// A record-typed field is a sub-prefix; chained `.x` access concatenates
		// onto it. There's no top-level shell var for the prefix itself.
		return exprValue{prologue: tv.prologue, value: fieldVar, form: formRecord}
	}
	if opt, isOpt := g.info.Types[e].(*types.Optional); isOpt {
		nullVar := fieldVar + "__null"
		switch opt.Elem {
		case types.Number, types.Bool:
			return exprValue{prologue: tv.prologue, value: fieldVar, form: formArith, nullCheck: nullVar}
		default:
			return exprValue{prologue: tv.prologue, value: "${" + fieldVar + "}", form: formStr, nullCheck: nullVar}
		}
	}
	switch g.info.Types[e] {
	case types.Number, types.Bool:
		return exprValue{prologue: tv.prologue, value: fieldVar, form: formArith}
	default:
		return exprValue{prologue: tv.prologue, value: "${" + fieldVar + "}", form: formStr}
	}
}

// emitSumCopy materialises a sum value into the destination prefix. Variant
// literals fast-path into per-slot writes; other expressions copy every leaf
// of the source prefix (including the unused variants' empty slots so the
// destination's shape is complete).
func (g *Generator) emitSumCopy(targetPrefix, declPrefix string, sum *types.Sum, value ast.Expr) {
	if lit, ok := value.(*ast.RecordLit); ok {
		v := g.compileVariantLit(lit, sum, targetPrefix)
		for _, line := range v.prologue {
			g.writeLine(declPrefix + line)
		}
		return
	}
	// A bare unit-variant ident: synthesise into the destination prefix.
	if id, ok := value.(*ast.Ident); ok {
		if sym := g.info.Uses[id]; sym != nil && sym.IsVariant {
			v := g.compileUnitVariant(sum, sym.Name, targetPrefix)
			for _, line := range v.prologue {
				g.writeLine(declPrefix + line)
			}
			return
		}
	}
	v := g.compileExpr(value)
	g.writeLines(v.prologue)
	for _, lf := range sumLeaves(sum) {
		src := v.value + "__" + lf.Path
		dst := targetPrefix + "__" + lf.Path
		g.writeLine(fmt.Sprintf(`%s%s="${%s}"`, declPrefix, dst, src))
		if _, isOpt := lf.Type.(*types.Optional); isOpt {
			g.writeLine(fmt.Sprintf(`%s%s__null="${%s__null}"`, declPrefix, dst, src))
		}
	}
}

// emitRecordCopy materialises a record value into the destination prefix,
// generating per-leaf assignments (nested record fields recurse into their
// own leaves). Used when assigning one record to another or when binding a
// function-returned record to a variable.
func (g *Generator) emitRecordCopy(targetPrefix, declPrefix string, rec *types.Record, value ast.Expr) {
	// Fast path: record literal as the RHS — write straight into the target.
	if lit, ok := value.(*ast.RecordLit); ok {
		v := g.compileRecordLit(lit, targetPrefix)
		// declPrefix is "local " / "readonly " or "" depending on scope.
		// We rewrite the produced lines to add the prefix.
		for _, line := range v.prologue {
			g.writeLine(declPrefix + line)
		}
		return
	}
	v := g.compileExpr(value)
	g.writeLines(v.prologue)
	for _, lf := range recordLeaves(rec) {
		src := v.value + "__" + lf.Path
		dst := targetPrefix + "__" + lf.Path
		g.writeLine(fmt.Sprintf(`%s%s="${%s}"`, declPrefix, dst, src))
		if _, isOpt := lf.Type.(*types.Optional); isOpt {
			g.writeLine(fmt.Sprintf(`%s%s__null="${%s__null}"`, declPrefix, dst, src))
		}
	}
}

func (g *Generator) compileIndex(e *ast.IndexExpr) exprValue {
	// Index of an array-of-record produces a record-form value: extract the
	// row (newline-separated), then split it into leaf slots.
	if rec, ok := g.info.Types[e].(*types.Record); ok {
		return g.compileIndexOfRecord(e, rec)
	}
	tv := g.compileExpr(e.Target)
	iv := g.compileExpr(e.Index)
	out := g.tmp("idx")
	prologue := concatPrologues(tv.prologue, iv.prologue)
	// awk uses 1-based NR; tartalo uses 0-based indexing, so add 1.
	prologue = append(prologue,
		fmt.Sprintf("%s=$(printf '%%s' \"%s\" | awk -v i=%s 'NR==i+1{print; exit}')",
			out, tv.shString(), asArithExpansion(iv)))
	// Render the result form according to the element type so arithmetic on
	// number-array elements doesn't need an extra cast at the use site.
	switch g.info.Types[e] {
	case types.Number, types.Bool:
		return exprValue{prologue: prologue, value: out, form: formArith}
	default:
		return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
	}
}

func (g *Generator) compileIndexOfRecord(e *ast.IndexExpr, rec *types.Record) exprValue {
	g.usesRecordArrays = true
	tv := g.compileExpr(e.Target)
	iv := g.compileExpr(e.Index)
	out := g.tmp("rec")
	rowVar := g.tmp("row")
	prologue := concatPrologues(tv.prologue, iv.prologue)
	prologue = append(prologue,
		fmt.Sprintf("%s=$(printf '%%s' \"%s\" | awk -v i=%s 'NR==i+1{print; exit}')",
			rowVar, tv.shString(), asArithExpansion(iv)))
	prologue = g.emitRowSplit(prologue, rowVar, rowSlots(out, recordLeaves(rec)))
	return exprValue{prologue: prologue, value: out, form: formRecord}
}

func (g *Generator) compileIdent(id *ast.Ident) exprValue {
	sym := g.info.Uses[id]
	if sym == nil {
		return exprValue{value: "${" + shName(id.Name) + "}", form: formStr}
	}
	name := shName(id.Name)
	if sym.Module != nil {
		name = shName(checker.MangledName(sym.Module, sym.Name))
	}
	// A reference to a function (declared with `func`) — not a call — yields
	// its mangled shell name as a string. Builtins are special-cased and
	// can't be passed by reference; the checker rejects that elsewhere if
	// needed, but here we'd just emit "echo" which won't dispatch correctly.
	if sym.IsFunc {
		if sym.IsBuiltin {
			// Conservative: emit a placeholder. The checker should have caught
			// this before; if it didn't, the runtime command lookup will fail.
			return exprValue{value: name, form: formStr}
		}
		return exprValue{value: name, form: formStr}
	}
	if opt, isOpt := sym.Type.(*types.Optional); isOpt {
		// Optional locals/globals live as `name` + `name__null` sidecar.
		nullCheck := name + "__null"
		switch opt.Elem {
		case types.Number, types.Bool:
			return exprValue{value: name, form: formArith, nullCheck: nullCheck}
		default:
			return exprValue{value: "${" + name + "}", form: formStr, nullCheck: nullCheck}
		}
	}
	if _, isRec := sym.Type.(*types.Record); isRec {
		return exprValue{value: name, form: formRecord}
	}
	if sum, isSum := sym.Type.(*types.Sum); isSum {
		// A unit-variant constructor materialises a fresh value each time it
		// is referenced; the symbol carries no slot of its own. Other sum-
		// typed bindings are ordinary prefixes whose slots are already live.
		if sym.IsVariant {
			return g.compileUnitVariant(sum, sym.Name, "")
		}
		return exprValue{value: name, form: formRecord}
	}
	switch sym.Type {
	case types.Number, types.Bool:
		return exprValue{value: name, form: formArith}
	default:
		return exprValue{value: "${" + name + "}", form: formStr}
	}
}

func (g *Generator) compileStringLit(s *ast.StringLit) exprValue {
	var b strings.Builder
	var prologue []string
	for _, p := range s.Parts {
		switch p := p.(type) {
		case *ast.StringChunk:
			b.WriteString(escapeForDoubleQuoted(p.Value))
		default:
			v := g.compileExpr(p)
			prologue = append(prologue, v.prologue...)
			b.WriteString(v.shString())
		}
	}
	return exprValue{prologue: prologue, value: b.String(), form: formStr}
}

func (g *Generator) compileCmdLit(c *ast.CmdLit) exprValue {
	cmd, prologue := g.compileCmdString(c)
	// Wrap in $(...) so the value is the captured stdout.
	return exprValue{
		prologue: prologue,
		value:    "$(" + cmd + ")",
		form:     formStr,
	}
}

// compileCmdString builds the command-line string for a CmdLit. It returns the
// already-shell-ready command string (suitable for placing after `$(`) plus a
// prologue for any interpolated subexpressions.
func (g *Generator) compileCmdString(c *ast.CmdLit) (string, []string) {
	var b strings.Builder
	var prologue []string
	for _, p := range c.Parts {
		switch p := p.(type) {
		case *ast.StringChunk:
			b.WriteString(p.Value)
		default:
			// Interpolations into a command line need to be embedded as
			// shell-quoted values; the simplest safe form is to evaluate the
			// expression into a temp and use ${tmp}, leaving quoting to the
			// command author.
			v := g.compileExpr(p)
			prologue = append(prologue, v.prologue...)
			t := g.tmp("ci")
			prologue = append(prologue, fmt.Sprintf("%s=%s", t, v.assignmentRHS()))
			b.WriteString("\"${" + t + "}\"")
		}
	}
	return b.String(), prologue
}

func (g *Generator) compileUnary(u *ast.UnaryExpr) exprValue {
	v := g.compileExpr(u.Operand)
	switch u.Op {
	case token.Minus:
		// Float negation can't go through `$(( ))` because sh arithmetic doesn't
		// understand decimal points. Defer to awk for floats.
		if g.info.Types[u.Operand] == types.Float {
			t := g.tmp("neg")
			prologue := append([]string{}, v.prologue...)
			prologue = append(prologue, fmt.Sprintf(
				`%s=$(awk -v x=%s 'BEGIN { printf "%%g", -x }')`,
				t, shQuoteDouble(v.shString())))
			return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
		}
		return exprValue{prologue: v.prologue, value: "-(" + asArith(v) + ")", form: formArith}
	case token.Bang:
		return exprValue{prologue: v.prologue, value: "!(" + asArith(v) + ")", form: formBool}
	}
	return exprValue{value: "0", form: formStr}
}

func (g *Generator) compileBinary(b *ast.BinaryExpr) exprValue {
	lt := g.info.Types[b.Lhs]
	rt := g.info.Types[b.Rhs]

	switch b.Op {
	case token.Plus:
		if lt == types.String || rt == types.String {
			lv := g.compileExpr(b.Lhs)
			rv := g.compileExpr(b.Rhs)
			return exprValue{
				prologue: concatPrologues(lv.prologue, rv.prologue),
				value:    lv.shString() + rv.shString(),
				form:     formStr,
			}
		}
		if lt == types.Float || rt == types.Float {
			return g.floatArith(b, "+")
		}
		fallthrough
	case token.Minus, token.Star, token.Slash:
		if lt == types.Float || rt == types.Float {
			return g.floatArith(b, arithSym(b.Op))
		}
		return g.arithOp(b)
	case token.Percent:
		return g.arithOp(b)
	case token.Eq, token.Neq, token.Lt, token.Lte, token.Gt, token.Gte:
		return g.compareOp(b, lt)
	case token.AndAnd, token.OrOr:
		return g.logicOp(b)
	}
	return exprValue{value: "0", form: formStr}
}

// --- higher-order builtin lowering ----------------------------------------
//
// All three of these take an array (as a newline-joined sh string) and a
// function reference (as a sh string holding the mangled function name).
// The element loop uses `while IFS= read -r` over a here-doc so the body
// runs in the parent shell — calling the user function via `"$fn" "$elem"`
// produces a result in the global `__ret`.

// emitHerdocLoop renders the entire while-read-do-done block plus its
// here-doc body and terminator as a single multi-line prologue entry. The
// terminator MUST appear at column 0, which we achieve by embedding raw
// `\n` characters in one prologue line — writeLine doesn't add indentation
// after embedded newlines, so the terminator stays unindented.
func herdocBlock(loopBody string, arr, eof string) string {
	return fmt.Sprintf("  while IFS= read -r __tt_iter; do\n%s\n  done <<%s\n$%s\n%s",
		loopBody, eof, arr, eof)
}

func (g *Generator) compileMap(args []exprValue, prologue []string) exprValue {
	arr := g.tmp("map_a")
	fn := g.tmp("map_f")
	out := g.tmp("map_o")
	first := g.tmp("map_i")
	prologue = append(prologue, fmt.Sprintf("%s=%s", arr, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", fn, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(`%s=""`, out))
	prologue = append(prologue, fmt.Sprintf(`%s=1`, first))
	prologue = append(prologue, fmt.Sprintf(`if [ -n "$%s" ]; then`, arr))
	body := fmt.Sprintf(
		`    "$%s" "$__tt_iter"`+"\n"+
			`    if [ "$%s" = 1 ]; then %s="$__ret"; %s=0; else %s=$(printf '%%s\n%%s' "$%s" "$__ret"); fi`,
		fn, first, out, first, out, out)
	prologue = append(prologue, herdocBlock(body, arr, "__TT_MAP_EOF__"))
	prologue = append(prologue, `fi`)
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}

func (g *Generator) compileFilter(args []exprValue, prologue []string) exprValue {
	arr := g.tmp("flt_a")
	fn := g.tmp("flt_f")
	out := g.tmp("flt_o")
	first := g.tmp("flt_i")
	prologue = append(prologue, fmt.Sprintf("%s=%s", arr, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", fn, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(`%s=""`, out))
	prologue = append(prologue, fmt.Sprintf(`%s=1`, first))
	prologue = append(prologue, fmt.Sprintf(`if [ -n "$%s" ]; then`, arr))
	body := fmt.Sprintf(
		`    "$%s" "$__tt_iter"`+"\n"+
			`    if [ "$__ret" = 1 ]; then`+"\n"+
			`      if [ "$%s" = 1 ]; then %s="$__tt_iter"; %s=0; else %s=$(printf '%%s\n%%s' "$%s" "$__tt_iter"); fi`+"\n"+
			`    fi`,
		fn, first, out, first, out, out)
	prologue = append(prologue, herdocBlock(body, arr, "__TT_FLT_EOF__"))
	prologue = append(prologue, `fi`)
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}

func (g *Generator) compileReduce(args []exprValue, prologue []string) exprValue {
	arr := g.tmp("red_a")
	fn := g.tmp("red_f")
	acc := g.tmp("red_r")
	prologue = append(prologue, fmt.Sprintf("%s=%s", arr, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", acc, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", fn, args[2].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(`if [ -n "$%s" ]; then`, arr))
	body := fmt.Sprintf(
		`    "$%s" "$%s" "$__tt_iter"`+"\n"+
			`    %s="$__ret"`,
		fn, acc, acc)
	prologue = append(prologue, herdocBlock(body, arr, "__TT_RED_EOF__"))
	prologue = append(prologue, `fi`)
	return exprValue{prologue: prologue, value: "${" + acc + "}", form: formStr}
}

// compileIntOf truncates a float to an int (toward zero).
func (g *Generator) compileIntOf(args []exprValue, prologue []string) exprValue {
	t := g.tmp("intof")
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(awk -v x=%s 'BEGIN { printf "%%d", (x < 0) ? -int(-x) : int(x) }')`,
		t, shQuoteDouble(args[0].shString())))
	return exprValue{prologue: prologue, value: t, form: formArith}
}

// compileParseFloat tries to parse the input as a float. Returns null if the
// input doesn't fully consume — awk's `+0` coerces partial-numeric prefixes
// silently, so we re-check the round-trip text.
func (g *Generator) compileParseFloat(args []exprValue, prologue []string) exprValue {
	val := g.tmp("pf_v")
	nullVar := g.tmp("pf_n")
	srcVar := g.tmp("pf_s")
	prologue = append(prologue, fmt.Sprintf("%s=%s", srcVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(awk -v s="$%s" 'BEGIN {`+
			` if (s ~ /^[ \t]*[-+]?([0-9]+(\.[0-9]+)?|\.[0-9]+)([eE][-+]?[0-9]+)?[ \t]*$/) printf "%%g", s + 0`+
			` }')`,
		val, srcVar))
	prologue = append(prologue, fmt.Sprintf(
		`if [ -z "$%s" ]; then %s=1; else %s=0; fi`,
		val, nullVar, nullVar))
	return exprValue{
		prologue:  prologue,
		value:     "${" + val + "}",
		form:      formStr,
		nullCheck: nullVar,
	}
}

// compileFormatFloat formats a float to the given decimal precision.
func (g *Generator) compileFormatFloat(args []exprValue, prologue []string) exprValue {
	t := g.tmp("ff")
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(awk -v x=%s -v d=%s 'BEGIN { printf "%%.*f", d, x }')`,
		t, shQuoteDouble(args[0].shString()), asArithExpansion(args[1])))
	return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
}

// compileFloatRound implements `floor`, `ceil`, and `round`. POSIX awk has
// `int()` (truncation toward zero) but no direct floor/ceil/round, so we
// build them from `int()` and a comparison.
func (g *Generator) compileFloatRound(args []exprValue, prologue []string, fn string) exprValue {
	t := g.tmp(fn)
	var awkExpr string
	switch fn {
	case "floor":
		awkExpr = `(x >= 0) ? int(x) : (int(x) == x ? int(x) : int(x) - 1)`
	case "ceil":
		awkExpr = `(x <= 0) ? int(x) : (int(x) == x ? int(x) : int(x) + 1)`
	case "round":
		awkExpr = `(x >= 0) ? int(x + 0.5) : -int(-x + 0.5)`
	}
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(awk -v x=%s 'BEGIN { printf "%%d", %s }')`,
		t, shQuoteDouble(args[0].shString()), awkExpr))
	return exprValue{prologue: prologue, value: t, form: formArith}
}

// floatCompare emits a 1/0 result for a float (or mixed int/float)
// comparison. Returned as formArith so it slots into the condition machinery
// without further wrapping.
func (g *Generator) floatCompare(b *ast.BinaryExpr) exprValue {
	lv := g.compileExpr(b.Lhs)
	rv := g.compileExpr(b.Rhs)
	prologue := concatPrologues(lv.prologue, rv.prologue)
	t := g.tmp("fc")
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(awk -v a=%s -v b=%s 'BEGIN { print (a %s b) ? 1 : 0 }')`,
		t, shQuoteDouble(lv.shString()), shQuoteDouble(rv.shString()), arithSym(b.Op)))
	return exprValue{prologue: prologue, value: t, form: formArith}
}

// floatArith performs arithmetic on float (or mixed int/float) operands by
// shelling out to awk. The result is a sh string holding awk's `%g` rendering.
func (g *Generator) floatArith(b *ast.BinaryExpr, op string) exprValue {
	lv := g.compileExpr(b.Lhs)
	rv := g.compileExpr(b.Rhs)
	prologue := concatPrologues(lv.prologue, rv.prologue)
	t := g.tmp("fa")
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(awk -v a=%s -v b=%s 'BEGIN { printf "%%g", a %s b }')`,
		t, shQuoteDouble(lv.shString()), shQuoteDouble(rv.shString()), op))
	return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
}

func (g *Generator) arithOp(b *ast.BinaryExpr) exprValue {
	lv := g.compileExpr(b.Lhs)
	rv := g.compileExpr(b.Rhs)
	op := arithSym(b.Op)
	return exprValue{
		prologue: concatPrologues(lv.prologue, rv.prologue),
		value:    arithGroup(lv, b.Op, true) + " " + op + " " + arithGroup(rv, b.Op, false),
		form:     formArith,
	}
}

// tryCompileNullValue is the value-form counterpart to tryCompileNullCond.
// It returns a 1/0 bool for `x == null` / `x != null` (and the trivial
// null-vs-null cases).
func (g *Generator) tryCompileNullValue(b *ast.BinaryExpr) (exprValue, bool) {
	if b.Op != token.Eq && b.Op != token.Neq {
		return exprValue{}, false
	}
	lt := g.info.Types[b.Lhs]
	rt := g.info.Types[b.Rhs]
	var opt ast.Expr
	switch {
	case lt == types.Null && types.IsOptional(rt):
		opt = b.Rhs
	case rt == types.Null && types.IsOptional(lt):
		opt = b.Lhs
	case lt == types.Null && rt == types.Null:
		v := "1"
		if b.Op == token.Neq {
			v = "0"
		}
		return exprValue{value: v, form: formBool}, true
	default:
		return exprValue{}, false
	}
	v := g.compileExpr(opt)
	op := "=="
	if b.Op == token.Neq {
		op = "!="
	}
	return exprValue{
		prologue: v.prologue,
		value:    fmt.Sprintf("%s %s 1", v.nullCheck, op),
		form:     formBool,
	}, true
}

// tryCompileNullCond detects `x == null`, `null == x`, `x != null`, etc. and
// emits a check on the optional side's null flag. Returns ok=true iff the
// comparison was a null comparison.
func (g *Generator) tryCompileNullCond(b *ast.BinaryExpr) (condValue, bool) {
	if b.Op != token.Eq && b.Op != token.Neq {
		return condValue{}, false
	}
	lt := g.info.Types[b.Lhs]
	rt := g.info.Types[b.Rhs]
	// Locate the optional side and the literal-null side.
	var opt ast.Expr
	switch {
	case lt == types.Null && types.IsOptional(rt):
		opt = b.Rhs
	case rt == types.Null && types.IsOptional(lt):
		opt = b.Lhs
	case lt == types.Null && rt == types.Null:
		// `null == null` is always true; `null != null` always false.
		test := "true"
		if b.Op == token.Neq {
			test = "false"
		}
		return condValue{test: test}, true
	default:
		return condValue{}, false
	}
	v := g.compileExpr(opt)
	want := "1"
	if b.Op == token.Neq {
		want = "0"
	}
	return condValue{
		prologue: v.prologue,
		test:     fmt.Sprintf(`[ "$%s" = %s ]`, v.nullCheck, want),
	}, true
}

func (g *Generator) compareOp(b *ast.BinaryExpr, operandType types.Type) exprValue {
	// Null comparison in value position (e.g. `let b = x == null`).
	if v, ok := g.tryCompileNullValue(b); ok {
		return v
	}
	// Float (or mixed int/float) comparison goes through awk.
	lt := g.info.Types[b.Lhs]
	rt := g.info.Types[b.Rhs]
	if lt == types.Float || rt == types.Float {
		return g.floatCompare(b)
	}
	lv := g.compileExpr(b.Lhs)
	rv := g.compileExpr(b.Rhs)
	if operandType == types.String {
		t := g.tmp("scmp")
		var lines []string
		lines = append(lines, lv.prologue...)
		lines = append(lines, rv.prologue...)
		switch b.Op {
		case token.Eq, token.Neq:
			op := "="
			if b.Op == token.Neq {
				op = "!="
			}
			lines = append(lines,
				fmt.Sprintf("if [ \"%s\" %s \"%s\" ]; then %s=1; else %s=0; fi",
					lv.shString(), op, rv.shString(), t, t))
		default:
			lines = append(lines, awkStringCmpAssign(t, b.Op, lv.shString(), rv.shString()))
		}
		return exprValue{prologue: lines, value: t, form: formArith}
	}
	// Numeric / bool: use sh arithmetic comparisons (returns 1 or 0).
	op := arithSym(b.Op)
	return exprValue{
		prologue: concatPrologues(lv.prologue, rv.prologue),
		value:    fmt.Sprintf("%s %s %s", arithGroup(lv, b.Op, true), op, arithGroup(rv, b.Op, false)),
		form:     formBool,
	}
}

func (g *Generator) logicOp(b *ast.BinaryExpr) exprValue {
	lv := g.compileExpr(b.Lhs)
	rv := g.compileExpr(b.Rhs)
	op := "&&"
	if b.Op == token.OrOr {
		op = "||"
	}
	return exprValue{
		prologue: concatPrologues(lv.prologue, rv.prologue),
		value:    fmt.Sprintf("%s %s %s", arithGroup(lv, b.Op, true), op, arithGroup(rv, b.Op, false)),
		form:     formBool,
	}
}

func (g *Generator) compileCall(call *ast.CallExpr, isStmt bool) exprValue {
	id, _ := call.Callee.(*ast.Ident)
	if id == nil {
		return exprValue{value: "# bad call", form: formStr}
	}
	sym := g.info.Uses[id]
	if sym == nil {
		return exprValue{value: "# bad call", form: formStr}
	}

	// Compile arguments first, gathering prologues.
	var argVals []exprValue
	var argTypes []types.Type
	var prologue []string
	for _, a := range call.Args {
		av := g.compileExpr(a)
		prologue = append(prologue, av.prologue...)
		argVals = append(argVals, av)
		argTypes = append(argTypes, g.info.Types[a])
	}

	if sym.IsBuiltin {
		return g.compileBuiltinCall(sym, argVals, argTypes, prologue, isStmt, call.LParenPos)
	}

	// User-defined function or function-typed variable. The callee word is
	// either the mangled function name (direct) or an expansion of the
	// variable holding such a name (indirect through a reference value).
	var calleeName string
	if sym.IsFunc {
		calleeName = shName(id.Name)
		if sym.Module != nil {
			calleeName = shName(checker.MangledName(sym.Module, sym.Name))
		}
	} else {
		// Variable of function type: `"${var}"` expands to the mangled name
		// the variable is holding, which sh then runs as a command.
		varName := shName(id.Name)
		if sym.Module != nil {
			varName = shName(checker.MangledName(sym.Module, sym.Name))
		}
		calleeName = `"${` + varName + `}"`
	}
	args := []string{calleeName}
	// The function's declared param types (sym.Type) drive the argument
	// shape, since callers may pass `T` to a `T?` parameter (auto-wrap).
	paramTypes := sym.Type.(*types.Func).Params
	for i, av := range argVals {
		var paramTy types.Type
		if i < len(paramTypes) {
			paramTy = paramTypes[i]
		}
		// Record arguments expand into one positional per leaf field; optional
		// leaves contribute their __null sidecar after their value.
		if rec, ok := paramTy.(*types.Record); ok {
			for _, lf := range recordLeaves(rec) {
				args = append(args, fmt.Sprintf(`"${%s__%s}"`, av.value, lf.Path))
				if _, isOpt := lf.Type.(*types.Optional); isOpt {
					args = append(args, fmt.Sprintf(`"${%s__%s__null}"`, av.value, lf.Path))
				}
			}
			continue
		}
		// Sum arguments fan out the same way: the tag leaf plus every
		// variant-field slot, in declared order.
		if sum, ok := paramTy.(*types.Sum); ok {
			for _, lf := range sumLeaves(sum) {
				args = append(args, fmt.Sprintf(`"${%s__%s}"`, av.value, lf.Path))
				if _, isOpt := lf.Type.(*types.Optional); isOpt {
					args = append(args, fmt.Sprintf(`"${%s__%s__null}"`, av.value, lf.Path))
				}
			}
			continue
		}
		// Optional parameter: value then null flag.
		if _, isOpt := paramTy.(*types.Optional); isOpt {
			args = append(args, shQuoteDouble(av.shString()))
			nullArg := av.nullCheck
			if nullArg == "" {
				// Auto-wrapped: the actual value is non-null.
				nullArg = "0"
			}
			args = append(args, fmt.Sprintf(`"$((%s))"`, nullArg))
			continue
		}
		args = append(args, shQuoteDouble(av.shString()))
	}
	prologue = append(prologue, strings.Join(args, " "))

	ft := sym.Type.(*types.Func)
	if ft.Result == types.Void {
		return exprValue{prologue: prologue, value: "", form: formStr}
	}
	if rec, ok := ft.Result.(*types.Record); ok {
		// Snapshot __ret__<leaf> into a fresh prefix so subsequent calls don't clobber.
		t := g.tmp("rec")
		for _, lf := range recordLeaves(rec) {
			prologue = append(prologue,
				fmt.Sprintf(`%s__%s="${__ret__%s}"`, t, lf.Path, lf.Path))
			if _, isOpt := lf.Type.(*types.Optional); isOpt {
				prologue = append(prologue,
					fmt.Sprintf(`%s__%s__null="${__ret__%s__null}"`, t, lf.Path, lf.Path))
			}
		}
		return exprValue{prologue: prologue, value: t, form: formRecord}
	}
	if sum, ok := ft.Result.(*types.Sum); ok {
		t := g.tmp("sum")
		for _, lf := range sumLeaves(sum) {
			prologue = append(prologue,
				fmt.Sprintf(`%s__%s="${__ret__%s}"`, t, lf.Path, lf.Path))
			if _, isOpt := lf.Type.(*types.Optional); isOpt {
				prologue = append(prologue,
					fmt.Sprintf(`%s__%s__null="${__ret__%s__null}"`, t, lf.Path, lf.Path))
			}
		}
		return exprValue{prologue: prologue, value: t, form: formRecord}
	}
	if opt, ok := ft.Result.(*types.Optional); ok {
		valTmp := g.tmp("ret")
		nullTmp := g.tmp("retn")
		prologue = append(prologue, fmt.Sprintf(`%s="$__ret"`, valTmp))
		prologue = append(prologue, fmt.Sprintf(`%s="$__ret__null"`, nullTmp))
		switch opt.Elem {
		case types.Number, types.Bool:
			return exprValue{prologue: prologue, value: valTmp, form: formArith, nullCheck: nullTmp}
		default:
			return exprValue{prologue: prologue, value: "${" + valTmp + "}", form: formStr, nullCheck: nullTmp}
		}
	}
	t := g.tmp("ret")
	prologue = append(prologue, fmt.Sprintf("%s=\"$__ret\"", t))
	switch ft.Result {
	case types.Number, types.Bool:
		return exprValue{prologue: prologue, value: t, form: formArith}
	default:
		return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
	}
}

func (g *Generator) compileBuiltinCall(sym *checker.Symbol, args []exprValue, argTypes []types.Type, prologue []string, isStmt bool, callPos token.Pos) exprValue {
	switch sym.Name {
	case "echo":
		prologue = append(prologue, fmt.Sprintf("printf '%%s\\n' %s", shQuoteDouble(args[0].shString())))
		return exprValue{prologue: prologue, value: "", form: formStr}
	case "eprint":
		prologue = append(prologue, fmt.Sprintf("printf '%%s\\n' %s 1>&2", shQuoteDouble(args[0].shString())))
		return exprValue{prologue: prologue, value: "", form: formStr}
	case "exit":
		prologue = append(prologue, fmt.Sprintf("exit %s", asArithStr(args[0])))
		return exprValue{prologue: prologue, value: "", form: formStr}
	case "str":
		// number → string; identical underlying representation
		return exprValue{prologue: prologue, value: asArithStr(args[0]), form: formStr}
	case "num":
		// string → number; the value is unquoted so sh's arithmetic parser
		// expands the variable and reads the digits. We trust the user — sh
		// will produce a runtime error at use if the contents aren't numeric.
		t := g.tmp("num")
		prologue = append(prologue,
			fmt.Sprintf("%s=$((%s + 0))", t, args[0].shString()))
		return exprValue{prologue: prologue, value: t, form: formArith}
	case "len":
		t := g.tmp("len")
		if _, isArr := argTypes[0].(*types.Array); isArr {
			// Arrays serialize as newline-joined strings, so the length is the
			// line count — with an empty-array short-circuit since `wc -l` on
			// "" still reports 1.
			argTmp := g.tmp("s")
			prologue = append(prologue, fmt.Sprintf("%s=%s", argTmp, args[0].assignmentRHS()))
			prologue = append(prologue,
				fmt.Sprintf(`if [ -z "$%s" ]; then %s=0; else %s=$(printf '%%s\n' "$%s" | awk 'END{print NR}'); fi`,
					argTmp, t, t, argTmp))
		} else {
			// For a simple string variable we can read ${#name} directly;
			// otherwise snapshot into a temp first.
			v := args[0].value
			if args[0].form == formStr && args[0].nullCheck == "" && len(args[0].prologue) == 0 &&
				strings.HasPrefix(v, "${") && strings.HasSuffix(v, "}") && !strings.Contains(v[2:len(v)-1], "{") {
				name := v[2 : len(v)-1]
				prologue = append(prologue, fmt.Sprintf("%s=${#%s}", t, name))
			} else {
				argTmp := g.tmp("s")
				prologue = append(prologue, fmt.Sprintf("%s=%s", argTmp, args[0].assignmentRHS()))
				prologue = append(prologue, fmt.Sprintf("%s=${#%s}", t, argTmp))
			}
		}
		return exprValue{prologue: prologue, value: t, form: formArith}
	case "env":
		// env(name): string?
		// We use POSIX `${var+x}` to distinguish "unset" (null) from "set to
		// empty" (non-null empty string). Both branches populate the sidecar
		// `__null` flag so consumers see a consistent optional shape.
		//
		// The `eval` here would be a shell-injection vector if the runtime
		// name contained metacharacters (e.g. `FOO}; rm -rf /; #`). We gate it
		// behind a `case` that only admits strings matching the POSIX env-var
		// name grammar `[A-Za-z_][A-Za-z0-9_]*`. Anything else cannot name a
		// real env var anyway, so reporting it as unset is correct.
		val := g.tmp("env")
		nullVar := g.tmp("envn")
		nameTmp := g.tmp("envname")
		isSet := g.tmp("envset")
		prologue = append(prologue, fmt.Sprintf("%s=%s", nameTmp, args[0].assignmentRHS()))
		// In test mode, consult __tt_mock_env first. Each rule in the state
		// var is a tab-separated `name<TAB>nullflag<TAB>value` line.
		// awk does the lookup; an empty result means no override and we
		// fall through to the real env probe.
		if g.testMode {
			mockHit := g.tmp("envmh")
			mockN := g.tmp("envmn")
			mockV := g.tmp("envmv")
			prologue = append(prologue, fmt.Sprintf(
				`%s=$(printf '%%s' "$__tt_mock_env" | awk -F'\t' -v n="$%s" '$1==n{ print $2 "\t" $3; exit }')`,
				mockHit, nameTmp))
			prologue = append(prologue, fmt.Sprintf(`%s=""`, mockN))
			prologue = append(prologue, fmt.Sprintf(`%s=""`, mockV))
			prologue = append(prologue, fmt.Sprintf(
				`if [ -n "$%s" ]; then %s=$(printf '%%s' "$%s" | awk -F'\t' '{print $1}'); %s=$(printf '%%s' "$%s" | awk -F'\t' '{print $2}'); fi`,
				mockHit, mockN, mockHit, mockV, mockHit))
			prologue = append(prologue, fmt.Sprintf(
				`if [ -n "$%s" ]; then %s=$%s; %s=$%s; else case "$%s" in ''|[!A-Za-z_]*|*[!A-Za-z0-9_]*) %s=""; %s=1 ;; *) eval "%s=\${$%s+1}"; if [ -z "$%s" ]; then %s=""; %s=1; else eval "%s=\$$%s"; %s=0; fi ;; esac; fi`,
				mockHit,
				val, mockV, nullVar, mockN,
				nameTmp,
				val, nullVar,
				isSet, nameTmp,
				isSet, val, nullVar,
				val, nameTmp, nullVar))
		} else {
			prologue = append(prologue, fmt.Sprintf(
				`case "$%s" in ''|[!A-Za-z_]*|*[!A-Za-z0-9_]*) %s=""; %s=1 ;; *) eval "%s=\${$%s+1}"; if [ -z "$%s" ]; then %s=""; %s=1; else eval "%s=\$$%s"; %s=0; fi ;; esac`,
				nameTmp,
				val, nullVar,
				isSet, nameTmp,
				isSet, val, nullVar,
				val, nameTmp, nullVar))
		}
		return exprValue{
			prologue:  prologue,
			value:     "${" + val + "}",
			form:      formStr,
			nullCheck: nullVar,
		}
	case "fetch":
		return g.compileFetch(args, prologue)
	case "exec":
		return g.compileExec(args, prologue)
	case "execTimeout":
		return g.compileExecTimeout(args, prologue)
	case "split":
		return g.compileSplit(args, prologue)
	case "join":
		return g.compileJoin(args, prologue)
	case "trim":
		return g.compileTrim(args, prologue)
	case "upper":
		return g.compileTrSimple(args, prologue, "[:lower:]", "[:upper:]")
	case "lower":
		return g.compileTrSimple(args, prologue, "[:upper:]", "[:lower:]")
	case "replace":
		return g.compileReplace(args, prologue)
	case "contains":
		return g.compileContains(args, prologue, "*\"$%s\"*")
	case "startsWith":
		return g.compileContains(args, prologue, "\"$%s\"*")
	case "endsWith":
		return g.compileContains(args, prologue, "*\"$%s\"")
	case "slice":
		return g.compileSlice(args, prologue)

	// --- file I/O ---
	case "readFile":
		return g.compileReadFile(args, prologue)
	case "writeFile":
		return g.compileWriteOrAppendFile(args, prologue, ">")
	case "appendFile":
		return g.compileWriteOrAppendFile(args, prologue, ">>")
	case "removeFile":
		return g.compileSimpleVoidCmd(args, prologue, `rm -f -- "$%s"`)
	case "mkdir":
		return g.compileSimpleVoidCmd(args, prologue, `mkdir -p -- "$%s"`)
	case "listDir":
		return g.compileListDir(args, prologue)
	case "exists":
		return g.compileFsTest(args, prologue, "-e")
	case "isFile":
		return g.compileFsTest(args, prologue, "-f")
	case "isDir":
		return g.compileFsTest(args, prologue, "-d")
	case "stat":
		return g.compileStat(args, prologue)
	case "readStdin":
		return g.compileReadStdin(prologue)

	// --- path manipulation ---
	case "pathJoin":
		return g.compilePathJoin(args, prologue)
	case "basename":
		return g.compilePathProgram(args, prologue, `basename -- "$%s"`, "bn")
	case "dirname":
		return g.compilePathProgram(args, prologue, `dirname -- "$%s"`, "dn")
	case "extname":
		return g.compileExtname(args, prologue)
	case "parsePath":
		return g.compileParsePath(args, prologue)

	// --- process / time ---
	case "args":
		return g.compileArgs(prologue)
	case "now":
		return g.compileNow(prologue)
	case "sleep":
		return g.compileSleep(args, prologue)
	case "formatTime":
		return g.compileFormatTime(args, prologue)

	// --- JSON (via jq) ---
	case "jsonGet":
		return g.compileJsonGet(args, prologue)
	case "jsonHas":
		return g.compileJsonHas(args, prologue)
	case "jsonArray":
		return g.compileJsonArray(args, prologue)
	case "jsonEscape":
		return g.compileJsonEscape(args, prologue)

	// --- regex (POSIX ERE via awk) ---
	case "regexMatch":
		return g.compileRegexMatch(args, prologue)
	case "regexFind":
		return g.compileRegexFind(args, prologue)
	case "regexFindAll":
		return g.compileRegexFindAll(args, prologue)
	case "regexReplace":
		return g.compileRegexReplace(args, prologue)

	// --- higher-order ---
	case "map":
		return g.compileMap(args, prologue)
	case "filter":
		return g.compileFilter(args, prologue)
	case "reduce":
		return g.compileReduce(args, prologue)

	// --- float helpers ---
	case "floatOf":
		// number → float widening: the underlying sh representation is just
		// the integer's text form, which awk accepts as a float.
		return exprValue{prologue: prologue, value: asArithStr(args[0]), form: formStr}
	case "intOf":
		return g.compileIntOf(args, prologue)
	case "parseFloat":
		return g.compileParseFloat(args, prologue)
	case "formatFloat":
		return g.compileFormatFloat(args, prologue)
	case "floor", "ceil", "round":
		return g.compileFloatRound(args, prologue, sym.Name)

	// --- testing ---
	case "assertEq":
		return g.compileAssertEqual(args, argTypes, prologue, callPos, false)
	case "assertNe":
		return g.compileAssertEqual(args, argTypes, prologue, callPos, true)
	case "check":
		return g.compileCheck(args, prologue, callPos)
	case "fail":
		return g.compileFail(args, prologue, callPos)
	case "skip":
		return g.compileSkip(args, prologue)

	// --- mocks (test-only; checker enforces scope) ---
	case "mockEnv":
		return g.compileMockEnv(args, prologue)
	case "mockNow":
		return g.compileMockNow(args, prologue)
	case "mockArgs":
		return g.compileMockArgs(args, prologue)
	case "mockReadStdin":
		return g.compileMockReadStdin(args, prologue)
	case "mockExec", "mockFetch", "mockReadFile",
		"mockExecCalls", "mockFetchCalls", "mockReadFileCalls":
		// Native already supports the full mock set; the sh backend ships
		// with the four name/value-style mocks above. exec/fetch/readFile
		// mocks need response-record serialisation (multi-line stdout,
		// embedded base64) that isn't worth the runtime weight for v1 —
		// users who need them should switch to --target=native, which is
		// the recommended target for shipping anyway.
		prologue = append(prologue, fmt.Sprintf(
			`printf 'tartalo: %s requires --target=native (sh backend supports mockEnv/mockNow/mockArgs/mockReadStdin only)\n' >&2; exit 1`,
			sym.Name))
		return exprValue{prologue: prologue, value: "", form: formStr}
	}
	return exprValue{prologue: prologue, value: "# unknown builtin " + sym.Name, form: formStr}
}

// --- mock builtins (sh backend) --------------------------------------------
//
// All four lowerings simply update the well-known `__tt_mock_*` shell vars
// declared in the test-mode preamble. They run inside a test's `( )`
// subshell so writes don't leak across tests.

func (g *Generator) compileMockEnv(args []exprValue, prologue []string) exprValue {
	nameVar := g.tmp("me_n")
	valVar := g.tmp("me_v")
	nullVar := g.tmp("me_x")
	prologue = append(prologue, fmt.Sprintf("%s=%s", nameVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", valVar, args[0].assignmentRHS()))
	// args[1] is `string?`. The compiler exposes its value + null sidecar
	// via the standard optional shape; we copy both into our state record.
	prologue = append(prologue, fmt.Sprintf("%s=%s", valVar, args[1].assignmentRHS()))
	nullExpr := args[1].nullCheck
	if nullExpr == "" {
		nullExpr = "0"
	}
	prologue = append(prologue, fmt.Sprintf("%s=$((%s))", nullVar, nullExpr))
	// State format: tab-separated `name<TAB>nullflag<TAB>value`, one rule per
	// line (newline-terminated). Lookups go through `awk -F\t '$1==name'`.
	prologue = append(prologue, fmt.Sprintf(
		`__tt_mock_env="${__tt_mock_env}$%s	$%s	$%s
"`, nameVar, nullVar, valVar))
	return exprValue{prologue: prologue, value: "", form: formStr}
}

func (g *Generator) compileMockNow(args []exprValue, prologue []string) exprValue {
	prologue = append(prologue, fmt.Sprintf("__tt_mock_now=%s", args[0].assignmentRHS()))
	prologue = append(prologue, "__tt_mock_now_set=1")
	return exprValue{prologue: prologue, value: "", form: formStr}
}

func (g *Generator) compileMockArgs(args []exprValue, prologue []string) exprValue {
	prologue = append(prologue, fmt.Sprintf("__tt_mock_args=%s", args[0].assignmentRHS()))
	prologue = append(prologue, "__tt_mock_args_set=1")
	return exprValue{prologue: prologue, value: "", form: formStr}
}

func (g *Generator) compileMockReadStdin(args []exprValue, prologue []string) exprValue {
	prologue = append(prologue, fmt.Sprintf("__tt_mock_stdin=%s", args[0].assignmentRHS()))
	prologue = append(prologue, "__tt_mock_stdin_set=1")
	return exprValue{prologue: prologue, value: "", form: formStr}
}

// --- testing builtins ------------------------------------------------------
//
// All five assertion/test-control builtins write their state via the well-
// known temp files `__tt_msg_file` (failure messages) and `__tt_skip_file`
// (skip reasons), and exit the enclosing subshell with status 1 (fail) or 0
// (skip). The runner harness wraps each test in `( ... )` so `exit` only
// terminates the test, not the whole script.
//
// `posLoc` is rendered as `file:line:col` so failure reports are clickable.

func (g *Generator) compileAssertEqual(args []exprValue, argTypes []types.Type, prologue []string, pos token.Pos, negated bool) exprValue {
	loc := fmt.Sprintf("%s:%d:%d", pos.File, pos.Line, pos.Col)
	aTmp := g.tmp("eq_a")
	bTmp := g.tmp("eq_b")
	prologue = append(prologue, fmt.Sprintf("%s=%s", aTmp, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", bTmp, args[1].assignmentRHS()))

	// Pick the comparison operator based on the (already type-checked) arg
	// types. Numbers and bools use shell arithmetic; floats go through awk;
	// strings use POSIX `=` / `!=`.
	var cmpFails string // a sh test that exits 0 if the assertion FAILS
	at := argTypes[0]
	bt := argTypes[1]
	switch {
	case at == types.Float || bt == types.Float:
		op := "!="
		if negated {
			op = "=="
		}
		cmpFails = fmt.Sprintf(
			`__a="$%s" __b="$%s" awk 'BEGIN{exit !((ENVIRON["__a"]+0) %s (ENVIRON["__b"]+0))}'`,
			aTmp, bTmp, op)
	case at == types.Number || at == types.Bool:
		op := "-ne"
		if negated {
			op = "-eq"
		}
		cmpFails = fmt.Sprintf(`[ "$%s" %s "$%s" ]`, aTmp, op, bTmp)
	default:
		op := "!="
		if negated {
			op = "="
		}
		cmpFails = fmt.Sprintf(`[ "$%s" %s "$%s" ]`, aTmp, op, bTmp)
	}

	verb := "assertEq"
	wanted := "expected"
	if negated {
		verb = "assertNe"
		wanted = "but got equal"
	}
	prologue = append(prologue, fmt.Sprintf("if %s; then", cmpFails))
	prologue = append(prologue, fmt.Sprintf(
		`  printf '%%s\n' '%s failed at %s:' >> "$__tt_msg_file"`,
		verb, escForSingleQuoted(loc)))
	if negated {
		prologue = append(prologue, fmt.Sprintf(
			`  printf '  %s: %%s\n  actual:        %%s\n' "$%s" "$%s" >> "$__tt_msg_file"`,
			wanted, aTmp, bTmp))
	} else {
		prologue = append(prologue, fmt.Sprintf(
			`  printf '  expected: %%s\n  actual:   %%s\n' "$%s" "$%s" >> "$__tt_msg_file"`,
			bTmp, aTmp))
	}
	prologue = append(prologue, "  exit 1")
	prologue = append(prologue, "fi")
	return exprValue{prologue: prologue, value: "", form: formStr}
}

func (g *Generator) compileCheck(args []exprValue, prologue []string, pos token.Pos) exprValue {
	loc := fmt.Sprintf("%s:%d:%d", pos.File, pos.Line, pos.Col)
	cTmp := g.tmp("chk")
	prologue = append(prologue, fmt.Sprintf("%s=%s", cTmp, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(`if [ "$%s" -eq 0 ]; then`, cTmp))
	prologue = append(prologue, fmt.Sprintf(
		`  printf '%%s\n' 'check failed at %s' >> "$__tt_msg_file"`,
		escForSingleQuoted(loc)))
	prologue = append(prologue, "  exit 1")
	prologue = append(prologue, "fi")
	return exprValue{prologue: prologue, value: "", form: formStr}
}

func (g *Generator) compileFail(args []exprValue, prologue []string, pos token.Pos) exprValue {
	loc := fmt.Sprintf("%s:%d:%d", pos.File, pos.Line, pos.Col)
	mTmp := g.tmp("failmsg")
	prologue = append(prologue, fmt.Sprintf("%s=%s", mTmp, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`printf '%%s\n' 'fail at %s:' >> "$__tt_msg_file"`,
		escForSingleQuoted(loc)))
	prologue = append(prologue, fmt.Sprintf(`printf '  %%s\n' "$%s" >> "$__tt_msg_file"`, mTmp))
	prologue = append(prologue, "exit 1")
	return exprValue{prologue: prologue, value: "", form: formStr}
}

func (g *Generator) compileSkip(args []exprValue, prologue []string) exprValue {
	rTmp := g.tmp("skipr")
	prologue = append(prologue, fmt.Sprintf("%s=%s", rTmp, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(`printf '%%s' "$%s" > "$__tt_skip_file"`, rTmp))
	prologue = append(prologue, "exit 0")
	return exprValue{prologue: prologue, value: "", form: formStr}
}

// escForSingleQuoted escapes text so it can sit inside a single-quoted shell
// string. POSIX sh has no single-quote escape, so we close-quote, insert an
// escaped quote, and re-open: `'` → `'\”`.
func escForSingleQuoted(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// compileExec lowers `exec(cmd)` to a sh invocation that captures stdout,
// stderr, and exit code separately. We dump stdout and stderr to temp files
// and pair the command with `|| code=$?` so that `set -e` in the surrounding
// script doesn't abort us on a non-zero exit. Like fetch, the result is
// snapshotted into a fresh prefix so subsequent calls don't clobber __ret__*.
func (g *Generator) compileExec(args []exprValue, prologue []string) exprValue {
	cmdVar := g.tmp("xcmd")
	stdoutTmp := g.tmp("xout")
	stderrTmp := g.tmp("xerr")
	codeVar := g.tmp("xcode")
	prologue = append(prologue, fmt.Sprintf("%s=%s", cmdVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(`%s=$(mktemp 2>/dev/null || printf '/tmp/tt_o_%%s' "$$")`, stdoutTmp))
	prologue = append(prologue, fmt.Sprintf(`%s=$(mktemp 2>/dev/null || printf '/tmp/tt_e_%%s' "$$")`, stderrTmp))
	prologue = append(prologue, fmt.Sprintf(`%s=0`, codeVar))
	// Run via `sh -c`. Both `>` and `2>` are quoted so paths with spaces work.
	// The `|| code=$?` clause both records the exit code AND prevents `set -e`
	// from aborting the outer script when the user's command fails.
	prologue = append(prologue, fmt.Sprintf(
		`sh -c "$%s" >"$%s" 2>"$%s" || %s=$?`,
		cmdVar, stdoutTmp, stderrTmp, codeVar))
	prologue = append(prologue, fmt.Sprintf(`__ret__code=$%s`, codeVar))
	prologue = append(prologue, fmt.Sprintf(`__ret__stdout=$(cat "$%s" 2>/dev/null || printf '')`, stdoutTmp))
	prologue = append(prologue, fmt.Sprintf(`__ret__stderr=$(cat "$%s" 2>/dev/null || printf '')`, stderrTmp))
	prologue = append(prologue, `__ret__ok=$(( __ret__code == 0 ))`)
	prologue = append(prologue, fmt.Sprintf(`rm -f "$%s" "$%s"`, stdoutTmp, stderrTmp))
	out := g.tmp("rec")
	for _, f := range []string{"code", "ok", "stdout", "stderr"} {
		prologue = append(prologue, fmt.Sprintf(`%s__%s="${__ret__%s}"`, out, f, f))
	}
	return exprValue{prologue: prologue, value: out, form: formRecord}
}

// compileExecTimeout is exec(cmd) with a wall-clock cap. We try `timeout`
// (GNU coreutils) and `gtimeout` (macOS via `brew install coreutils`) at
// runtime; if neither is on PATH the script aborts so the user can't
// silently get an unbounded run.
//
// `timeout` exits 124 when the command is killed, which we surface as
// `Process.code = 124`. The caller can branch on that to distinguish a
// timeout from an ordinary failure.
func (g *Generator) compileExecTimeout(args []exprValue, prologue []string) exprValue {
	cmdVar := g.tmp("xtcmd")
	secsVar := g.tmp("xtsecs")
	binVar := g.tmp("xtbin")
	stdoutTmp := g.tmp("xtout")
	stderrTmp := g.tmp("xterr")
	codeVar := g.tmp("xtcode")
	prologue = append(prologue, fmt.Sprintf("%s=%s", cmdVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", secsVar, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(command -v timeout 2>/dev/null || command -v gtimeout 2>/dev/null || true)`,
		binVar))
	prologue = append(prologue, fmt.Sprintf(
		`if [ -z "$%s" ]; then printf 'tartalo: execTimeout: requires `+"`"+`timeout`+"`"+` or `+"`"+`gtimeout`+"`"+` on PATH\n' >&2; exit 1; fi`,
		binVar))
	prologue = append(prologue, fmt.Sprintf(`%s=$(mktemp 2>/dev/null || printf '/tmp/tt_to_%%s' "$$")`, stdoutTmp))
	prologue = append(prologue, fmt.Sprintf(`%s=$(mktemp 2>/dev/null || printf '/tmp/tt_te_%%s' "$$")`, stderrTmp))
	prologue = append(prologue, fmt.Sprintf(`%s=0`, codeVar))
	prologue = append(prologue, fmt.Sprintf(
		`"$%s" "$%s" sh -c "$%s" >"$%s" 2>"$%s" || %s=$?`,
		binVar, secsVar, cmdVar, stdoutTmp, stderrTmp, codeVar))
	prologue = append(prologue, fmt.Sprintf(`__ret__code=$%s`, codeVar))
	prologue = append(prologue, fmt.Sprintf(`__ret__stdout=$(cat "$%s" 2>/dev/null || printf '')`, stdoutTmp))
	prologue = append(prologue, fmt.Sprintf(`__ret__stderr=$(cat "$%s" 2>/dev/null || printf '')`, stderrTmp))
	prologue = append(prologue, `__ret__ok=$(( __ret__code == 0 ))`)
	prologue = append(prologue, fmt.Sprintf(`rm -f "$%s" "$%s"`, stdoutTmp, stderrTmp))
	out := g.tmp("rec")
	for _, f := range []string{"code", "ok", "stdout", "stderr"} {
		prologue = append(prologue, fmt.Sprintf(`%s__%s="${__ret__%s}"`, out, f, f))
	}
	return exprValue{prologue: prologue, value: out, form: formRecord}
}

// --- string stdlib lowering -------------------------------------------------
//
// All of the helpers below pass values to awk via environment variables (the
// ENVIRON array) rather than `-v` to side-step awk's escape rules for newlines
// and backslashes.

func (g *Generator) compileTrSimple(args []exprValue, prologue []string, from, to string) exprValue {
	t := g.tmp("up")
	prologue = append(prologue,
		fmt.Sprintf("%s=$(printf '%%s' %s | tr '%s' '%s')",
			t, shQuoteDouble(args[0].shString()), from, to))
	return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
}

func (g *Generator) compileTrim(args []exprValue, prologue []string) exprValue {
	t := g.tmp("trim")
	srcVar := g.tmp("trim_s")
	prologue = append(prologue, fmt.Sprintf("%s=%s", srcVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(%s="$%s" awk 'BEGIN { s = ENVIRON["%s"]; sub(/^[ \t\r\n]+/, "", s); sub(/[ \t\r\n]+$/, "", s); printf "%%s", s }')`,
		t, srcVar, srcVar, srcVar))
	return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
}

func (g *Generator) compileReplace(args []exprValue, prologue []string) exprValue {
	t := g.tmp("rep")
	sVar := g.tmp("rep_s")
	fVar := g.tmp("rep_f")
	tVar := g.tmp("rep_t")
	prologue = append(prologue, fmt.Sprintf("%s=%s", sVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", fVar, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", tVar, args[2].assignmentRHS()))
	// Literal-substring replace via awk's index(); avoids regex surprises.
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(%s="$%s" %s="$%s" %s="$%s" awk 'BEGIN { s=ENVIRON["%s"]; f=ENVIRON["%s"]; t=ENVIRON["%s"]; if (f=="") { printf "%%s", s; exit }; out=""; while ((i=index(s,f))>0) { out=out substr(s,1,i-1) t; s=substr(s,i+length(f)) }; printf "%%s", out s }')`,
		t,
		sVar, sVar, fVar, fVar, tVar, tVar,
		sVar, fVar, tVar))
	return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
}

// compileContains shares an implementation among contains/startsWith/endsWith
// — the only thing that differs is the case-pattern wrapper.
func (g *Generator) compileContains(args []exprValue, prologue []string, patternFmt string) exprValue {
	t := g.tmp("has")
	sVar := g.tmp("has_s")
	pVar := g.tmp("has_p")
	prologue = append(prologue, fmt.Sprintf("%s=%s", sVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", pVar, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("case \"$%s\" in", sVar))
	prologue = append(prologue, fmt.Sprintf("  "+patternFmt+") %s=1 ;;", pVar, t))
	prologue = append(prologue, fmt.Sprintf("  *) %s=0 ;;", t))
	prologue = append(prologue, "esac")
	return exprValue{prologue: prologue, value: t, form: formArith}
}

func (g *Generator) compileSlice(args []exprValue, prologue []string) exprValue {
	t := g.tmp("slc")
	sVar := g.tmp("slc_s")
	prologue = append(prologue, fmt.Sprintf("%s=%s", sVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(%s="$%s" awk -v a=%s -v b=%s 'BEGIN { s=ENVIRON["%s"]; if (a<0) a=0; if (b>length(s)) b=length(s); if (b<=a) { printf ""; exit }; printf "%%s", substr(s, a+1, b-a) }')`,
		t,
		sVar, sVar,
		asArithExpansion(args[1]), asArithExpansion(args[2]),
		sVar))
	return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
}

func (g *Generator) compileSplit(args []exprValue, prologue []string) exprValue {
	t := g.tmp("spl")
	sVar := g.tmp("spl_s")
	dVar := g.tmp("spl_d")
	prologue = append(prologue, fmt.Sprintf("%s=%s", sVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", dVar, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(%s="$%s" %s="$%s" awk 'BEGIN { s=ENVIRON["%s"]; d=ENVIRON["%s"]; if (d=="") { printf "%%s", s; exit }; pos=1; dl=length(d); first=1; while(1) { rest=substr(s,pos); i=index(rest,d); if (i==0) { if (!first) printf "\n"; printf "%%s", rest; exit }; if (!first) printf "\n"; printf "%%s", substr(s,pos,i-1); pos=pos+i+dl-1; first=0 } }')`,
		t,
		sVar, sVar, dVar, dVar,
		sVar, dVar))
	return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
}

// --- file I/O lowering ------------------------------------------------------

// compileReadFile reads an entire file into a string. On any error (missing
// file, permission denied, …) the script aborts with a diagnostic on stderr.
// Trailing newlines in the file content are stripped — that's a property of
// `$(...)` capture and we accept it for v0.
func (g *Generator) compileReadFile(args []exprValue, prologue []string) exprValue {
	pathVar := g.tmp("rf_p")
	contentVar := g.tmp("rf_c")
	prologue = append(prologue, fmt.Sprintf("%s=%s", pathVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(cat -- "$%s") || { printf 'tartalo: readFile: cannot read %%s\n' "$%s" >&2; exit 1; }`,
		contentVar, pathVar, pathVar))
	return exprValue{prologue: prologue, value: "${" + contentVar + "}", form: formStr}
}

// compileWriteOrAppendFile lowers writeFile/appendFile by parameterising the
// redirection operator. Both abort the script on I/O failure.
func (g *Generator) compileWriteOrAppendFile(args []exprValue, prologue []string, redir string) exprValue {
	pathVar := g.tmp("wf_p")
	contentVar := g.tmp("wf_c")
	prologue = append(prologue, fmt.Sprintf("%s=%s", pathVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", contentVar, args[1].assignmentRHS()))
	op := "writeFile"
	if redir == ">>" {
		op = "appendFile"
	}
	prologue = append(prologue, fmt.Sprintf(
		`printf '%%s' "$%s" %s "$%s" || { printf 'tartalo: %s: cannot write %%s\n' "$%s" >&2; exit 1; }`,
		contentVar, redir, pathVar, op, pathVar))
	return exprValue{prologue: prologue, value: "", form: formStr}
}

// compileSimpleVoidCmd emits a single sh statement for a one-arg-path void
// builtin (removeFile, mkdir). The command is given as a printf format taking
// the path-temp's name.
func (g *Generator) compileSimpleVoidCmd(args []exprValue, prologue []string, cmdFmt string) exprValue {
	pathVar := g.tmp("p")
	prologue = append(prologue, fmt.Sprintf("%s=%s", pathVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(cmdFmt, pathVar))
	return exprValue{prologue: prologue, value: "", form: formStr}
}

// compileListDir returns the entries of a directory as a string[] (newline-
// joined). Hidden files are included (`-A` excludes only `.` and `..`). On
// error the script aborts.
func (g *Generator) compileListDir(args []exprValue, prologue []string) exprValue {
	pathVar := g.tmp("ld_p")
	listVar := g.tmp("ld_l")
	prologue = append(prologue, fmt.Sprintf("%s=%s", pathVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(ls -1 -A -- "$%s") || { printf 'tartalo: listDir: cannot list %%s\n' "$%s" >&2; exit 1; }`,
		listVar, pathVar, pathVar))
	return exprValue{prologue: prologue, value: "${" + listVar + "}", form: formStr}
}

// compileFsTest emits a 1/0 result based on a `[ -X path ]` test.
func (g *Generator) compileFsTest(args []exprValue, prologue []string, flag string) exprValue {
	pathVar := g.tmp("fst_p")
	resVar := g.tmp("fst_r")
	prologue = append(prologue, fmt.Sprintf("%s=%s", pathVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`if [ %s "$%s" ]; then %s=1; else %s=0; fi`,
		flag, pathVar, resVar, resVar))
	return exprValue{prologue: prologue, value: resVar, form: formArith}
}

// compileReadStdin reads stdin until EOF and returns it as a string. In
// test mode it consults `__tt_mock_stdin_set` first so tests can supply
// canned input via `mockReadStdin`.
func (g *Generator) compileReadStdin(prologue []string) exprValue {
	t := g.tmp("stdin")
	if g.testMode {
		prologue = append(prologue, fmt.Sprintf(
			`if [ "$__tt_mock_stdin_set" = "1" ]; then %s="$__tt_mock_stdin"; else %s=$(cat); fi`,
			t, t))
	} else {
		prologue = append(prologue, fmt.Sprintf("%s=$(cat)", t))
	}
	return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
}

// --- path manipulation ------------------------------------------------------

// compilePathJoin joins two path components, mirroring Node's `path.join`
// semantics for the second-argument-absolute case.
func (g *Generator) compilePathJoin(args []exprValue, prologue []string) exprValue {
	aVar := g.tmp("pj_a")
	bVar := g.tmp("pj_b")
	out := g.tmp("pj_r")
	prologue = append(prologue, fmt.Sprintf("%s=%s", aVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", bVar, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`case "$%s" in /*) %s="$%s" ;; *) case "$%s" in */) %s="$%s$%s" ;; *) %s="$%s/$%s" ;; esac ;; esac`,
		bVar, out, bVar,
		aVar, out, aVar, bVar,
		out, aVar, bVar))
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}

// compilePathProgram runs a path utility (basename / dirname) and captures stdout.
func (g *Generator) compilePathProgram(args []exprValue, prologue []string, cmdFmt, label string) exprValue {
	pathVar := g.tmp(label + "_p")
	out := g.tmp(label + "_r")
	prologue = append(prologue, fmt.Sprintf("%s=%s", pathVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(`%s=$(`+cmdFmt+`)`, append([]any{out}, pathVar)...))
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}

// compileExtname returns the extension (including the leading dot) of the
// final component of a path, or "" when the basename has no dot.
func (g *Generator) compileExtname(args []exprValue, prologue []string) exprValue {
	pathVar := g.tmp("ex_p")
	out := g.tmp("ex_r")
	prologue = append(prologue, fmt.Sprintf("%s=%s", pathVar, args[0].assignmentRHS()))
	// Compute basename via shell parameter expansion, then look for a dot.
	prologue = append(prologue, fmt.Sprintf(
		`__ex_b=$(basename -- "$%s"); case "$__ex_b" in *.*) %s=".${__ex_b##*.}" ;; *) %s="" ;; esac`,
		pathVar, out, out))
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}

// compileParsePath splits a path into { dir, base, name, ext }. Pure string
// manipulation — no syscalls, no I/O. The ext rule matches extname(): ".gz"
// for "foo.tar.gz", "" when the basename has no dot.
func (g *Generator) compileParsePath(args []exprValue, prologue []string) exprValue {
	pathVar := g.tmp("pp_p")
	prologue = append(prologue, fmt.Sprintf("%s=%s", pathVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(`__ret__dir=$(dirname -- "$%s")`, pathVar))
	prologue = append(prologue, fmt.Sprintf(`__ret__base=$(basename -- "$%s")`, pathVar))
	prologue = append(prologue,
		`case "$__ret__base" in *.*) __ret__ext=".${__ret__base##*.}"; __ret__name="${__ret__base%.*}" ;; *) __ret__ext=""; __ret__name="$__ret__base" ;; esac`)
	out := g.tmp("rec")
	for _, f := range []string{"dir", "base", "name", "ext"} {
		prologue = append(prologue, fmt.Sprintf(`%s__%s="${__ret__%s}"`, out, f, f))
	}
	return exprValue{prologue: prologue, value: out, form: formRecord}
}

// compileStat returns a FileInfo record for `path`. We run `[ -e/-f/-d ]` for
// the booleans (cheap and POSIX-portable) and probe `stat` for size, mtime,
// and permission bits with a runtime fallback between GNU (`stat -c`) and BSD
// (`stat -f`) so the same emitted script runs on Linux and macOS. When the
// path doesn't exist, every numeric field is 0 and `mode` is "".
func (g *Generator) compileStat(args []exprValue, prologue []string) exprValue {
	pathVar := g.tmp("st_p")
	prologue = append(prologue, fmt.Sprintf("%s=%s", pathVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(`if [ -e "$%s" ]; then`, pathVar))
	prologue = append(prologue, `  __ret__exists=1`)
	prologue = append(prologue, fmt.Sprintf(`  if [ -f "$%s" ]; then __ret__isFile=1; else __ret__isFile=0; fi`, pathVar))
	prologue = append(prologue, fmt.Sprintf(`  if [ -d "$%s" ]; then __ret__isDir=1; else __ret__isDir=0; fi`, pathVar))
	prologue = append(prologue, fmt.Sprintf(
		`  __ret__size=$(stat -c '%%s' "$%s" 2>/dev/null || stat -f '%%z' "$%s" 2>/dev/null || printf 0)`,
		pathVar, pathVar))
	prologue = append(prologue, fmt.Sprintf(
		`  __ret__mtime=$(stat -c '%%Y' "$%s" 2>/dev/null || stat -f '%%m' "$%s" 2>/dev/null || printf 0)`,
		pathVar, pathVar))
	prologue = append(prologue, fmt.Sprintf(
		`  __ret__mode=$(stat -c '%%a' "$%s" 2>/dev/null || stat -f '%%OLp' "$%s" 2>/dev/null || printf '')`,
		pathVar, pathVar))
	prologue = append(prologue, `else`)
	prologue = append(prologue, `  __ret__exists=0; __ret__isFile=0; __ret__isDir=0`)
	prologue = append(prologue, `  __ret__size=0; __ret__mtime=0; __ret__mode=""`)
	prologue = append(prologue, `fi`)
	out := g.tmp("rec")
	for _, f := range []string{"exists", "isFile", "isDir", "size", "mtime", "mode"} {
		prologue = append(prologue, fmt.Sprintf(`%s__%s="${__ret__%s}"`, out, f, f))
	}
	return exprValue{prologue: prologue, value: out, form: formRecord}
}

// --- process / time --------------------------------------------------------

// compileArgs returns the script's positional args as a string array. The
// `__tt_argv` global was populated at script entry by EmitModules. In test
// mode `mockArgs` lets a test override the value for the duration of one
// test body — we branch on `__tt_mock_args_set` and pick the override or
// the original snapshot.
func (g *Generator) compileArgs(prologue []string) exprValue {
	g.needsArgv = true
	if !g.testMode {
		return exprValue{prologue: prologue, value: "${__tt_argv}", form: formStr}
	}
	t := g.tmp("argv")
	prologue = append(prologue, fmt.Sprintf(
		`if [ "$__tt_mock_args_set" = "1" ]; then %s="$__tt_mock_args"; else %s="$__tt_argv"; fi`,
		t, t))
	return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
}

// compileNow returns the current Unix timestamp via `date +%s`. Test mode
// honours `mockNow` by reading the frozen value out of `__tt_mock_now`.
func (g *Generator) compileNow(prologue []string) exprValue {
	t := g.tmp("now")
	if g.testMode {
		prologue = append(prologue, fmt.Sprintf(
			`if [ "$__tt_mock_now_set" = "1" ]; then %s=$__tt_mock_now; else %s=$(date +%%s); fi`,
			t, t))
	} else {
		prologue = append(prologue, fmt.Sprintf(`%s=$(date +%%s)`, t))
	}
	return exprValue{prologue: prologue, value: t, form: formArith}
}

// compileSleep blocks for `secs` seconds. Fractional seconds are not
// guaranteed by POSIX `sleep`, so the underlying number is treated as an
// integer.
func (g *Generator) compileSleep(args []exprValue, prologue []string) exprValue {
	prologue = append(prologue, fmt.Sprintf(`sleep %s`, asArithExpansion(args[0])))
	return exprValue{prologue: prologue, value: "", form: formStr}
}

// compileFormatTime formats a Unix timestamp using `date`. We try the BSD
// (`-r SECS`) form first, then the GNU (`-d @SECS`) form, so the same script
// runs on macOS and Linux.
func (g *Generator) compileFormatTime(args []exprValue, prologue []string) exprValue {
	out := g.tmp("ft")
	secsVar := g.tmp("ft_s")
	fmtVar := g.tmp("ft_f")
	prologue = append(prologue, fmt.Sprintf("%s=%s", secsVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", fmtVar, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(date -r "$%s" "+$%s" 2>/dev/null) || %s=$(date -d "@$%s" "+$%s" 2>/dev/null) || %s=""`,
		out, secsVar, fmtVar, out, secsVar, fmtVar, out))
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}

// --- JSON ------------------------------------------------------------------

// compileJsonGet runs `jq -r path` against the JSON input. Missing paths and
// JSON-null values both surface as null on the tartalo side; jsonHas() is
// the way to disambiguate.
func (g *Generator) compileJsonGet(args []exprValue, prologue []string) exprValue {
	jsonVar := g.tmp("jg_j")
	pathVar := g.tmp("jg_p")
	val := g.tmp("jg_v")
	nullVar := g.tmp("jg_n")
	prologue = append(prologue, fmt.Sprintf("%s=%s", jsonVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", pathVar, args[1].assignmentRHS()))
	// jq with --exit-status fails if the path is missing or evaluates to null.
	// We treat both as null on the tartalo side.
	prologue = append(prologue, fmt.Sprintf(
		`if %s=$(printf '%%s' "$%s" | jq -e -r "$%s" 2>/dev/null); then %s=0; else %s=""; %s=1; fi`,
		val, jsonVar, pathVar, nullVar, val, nullVar))
	return exprValue{
		prologue:  prologue,
		value:     "${" + val + "}",
		form:      formStr,
		nullCheck: nullVar,
	}
}

func (g *Generator) compileJsonHas(args []exprValue, prologue []string) exprValue {
	jsonVar := g.tmp("jh_j")
	pathVar := g.tmp("jh_p")
	res := g.tmp("jh_r")
	prologue = append(prologue, fmt.Sprintf("%s=%s", jsonVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", pathVar, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`if printf '%%s' "$%s" | jq -e "$%s" >/dev/null 2>&1; then %s=1; else %s=0; fi`,
		jsonVar, pathVar, res, res))
	return exprValue{prologue: prologue, value: res, form: formArith}
}

// compileJsonArray returns the elements of a JSON array as a tartalo
// string[]. Each element is jq's stringification (raw for scalars, JSON for
// objects/arrays). Caveat: elements containing literal newlines confuse the
// newline-joined array model — that's a v0 limitation.
func (g *Generator) compileJsonArray(args []exprValue, prologue []string) exprValue {
	jsonVar := g.tmp("ja_j")
	pathVar := g.tmp("ja_p")
	out := g.tmp("ja_r")
	prologue = append(prologue, fmt.Sprintf("%s=%s", jsonVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", pathVar, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(printf '%%s' "$%s" | jq -r "$%s | .[]" 2>/dev/null) || %s=""`,
		out, jsonVar, pathVar, out))
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}

// compileJsonEscape encodes a string as a JSON string literal (with the
// surrounding quotes), escaping anything that needs escaping. Useful when
// the user is hand-building a JSON request body.
func (g *Generator) compileJsonEscape(args []exprValue, prologue []string) exprValue {
	srcVar := g.tmp("je_s")
	out := g.tmp("je_r")
	prologue = append(prologue, fmt.Sprintf("%s=%s", srcVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(printf '%%s' "$%s" | jq -R -s '.')`,
		out, srcVar))
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}

// --- regex (POSIX ERE via awk) ---------------------------------------------
//
// All four helpers pass the haystack and pattern via ENVIRON to skirt awk's
// `-v` escape rules. Patterns are awk's POSIX ERE syntax: character classes
// like `[[:digit:]]`, `+`, `?`, `|`, `()`. There are no `\d` / `\w` shortcuts
// — those would require gawk.

func (g *Generator) compileRegexMatch(args []exprValue, prologue []string) exprValue {
	sVar := g.tmp("rm_s")
	pVar := g.tmp("rm_p")
	out := g.tmp("rm_r")
	prologue = append(prologue, fmt.Sprintf("%s=%s", sVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", pVar, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(%s="$%s" %s="$%s" awk 'BEGIN { print (ENVIRON["%s"] ~ ENVIRON["%s"]) ? 1 : 0 }')`,
		out, sVar, sVar, pVar, pVar, sVar, pVar))
	return exprValue{prologue: prologue, value: out, form: formArith}
}

func (g *Generator) compileRegexFind(args []exprValue, prologue []string) exprValue {
	sVar := g.tmp("rf_s")
	pVar := g.tmp("rf_p")
	val := g.tmp("rf_v")
	nullVar := g.tmp("rf_n")
	prologue = append(prologue, fmt.Sprintf("%s=%s", sVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", pVar, args[1].assignmentRHS()))
	// We use awk's exit code to tell "no match" from "matched the empty
	// string": exit 1 means no match, exit 0 means matched (the captured
	// substring is on stdout).
	prologue = append(prologue, fmt.Sprintf(`%s=0`, nullVar))
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(%s="$%s" %s="$%s" awk 'BEGIN {`+
			` s=ENVIRON["%s"]; p=ENVIRON["%s"];`+
			` if (match(s, p)) { printf "%%s", substr(s, RSTART, RLENGTH); exit 0 }`+
			` exit 1`+
			` }') || %s=1`,
		val, sVar, sVar, pVar, pVar, sVar, pVar, nullVar))
	return exprValue{
		prologue:  prologue,
		value:     "${" + val + "}",
		form:      formStr,
		nullCheck: nullVar,
	}
}

func (g *Generator) compileRegexFindAll(args []exprValue, prologue []string) exprValue {
	sVar := g.tmp("rfa_s")
	pVar := g.tmp("rfa_p")
	out := g.tmp("rfa_r")
	prologue = append(prologue, fmt.Sprintf("%s=%s", sVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", pVar, args[1].assignmentRHS()))
	// Loop with awk's `match`, advancing past each hit. Empty-string matches
	// would loop forever; we guard with `RLENGTH > 0`.
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(%s="$%s" %s="$%s" awk 'BEGIN {`+
			` s=ENVIRON["%s"]; p=ENVIRON["%s"]; first=1;`+
			` while (match(s, p) > 0 && RLENGTH > 0) {`+
			` if (!first) printf "\n";`+
			` printf "%%s", substr(s, RSTART, RLENGTH);`+
			` s = substr(s, RSTART + RLENGTH);`+
			` first = 0`+
			` } }')`,
		out, sVar, sVar, pVar, pVar, sVar, pVar))
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}

func (g *Generator) compileRegexReplace(args []exprValue, prologue []string) exprValue {
	sVar := g.tmp("rr_s")
	pVar := g.tmp("rr_p")
	rVar := g.tmp("rr_r")
	out := g.tmp("rr_o")
	prologue = append(prologue, fmt.Sprintf("%s=%s", sVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", pVar, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", rVar, args[2].assignmentRHS()))
	// awk's gsub treats `&` and `\` in the replacement specially. We escape
	// both so the user sees plain literal-string behaviour. (Users who need
	// gsub semantics can use exec("...awk...") directly.)
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(%s="$%s" %s="$%s" %s="$%s" awk 'BEGIN {`+
			` s=ENVIRON["%s"]; p=ENVIRON["%s"]; r=ENVIRON["%s"];`+
			` gsub(/\\/, "\\\\\\\\", r); gsub(/&/, "\\\\&", r);`+
			` gsub(p, r, s); printf "%%s", s`+
			` }')`,
		out, sVar, sVar, pVar, pVar, rVar, rVar, sVar, pVar, rVar))
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}

func (g *Generator) compileJoin(args []exprValue, prologue []string) exprValue {
	t := g.tmp("jn")
	sVar := g.tmp("jn_s")
	dVar := g.tmp("jn_d")
	prologue = append(prologue, fmt.Sprintf("%s=%s", sVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", dVar, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(%s="$%s" %s="$%s" awk 'BEGIN { s=ENVIRON["%s"]; d=ENVIRON["%s"]; if (s=="") exit; n=split(s, p, "\n"); for (i=1; i<=n; i++) { if (i>1) printf "%%s", d; printf "%%s", p[i] } }')`,
		t,
		sVar, sVar, dVar, dVar,
		sVar, dVar))
	return exprValue{prologue: prologue, value: "${" + t + "}", form: formStr}
}

// compileFetch lowers `fetch(url)` to a curl invocation that captures status
// code, body, and raw response headers separately. The result is a Response
// record snapshotted into a fresh prefix so subsequent calls don't clobber it.
func (g *Generator) compileFetch(args []exprValue, prologue []string) exprValue {
	urlVar := g.tmp("url")
	bodyTmp := g.tmp("fbody")
	hdrsTmp := g.tmp("fhdrs")
	statusTmp := g.tmp("fstatus")
	prologue = append(prologue, fmt.Sprintf("%s=%s", urlVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(`%s=$(mktemp 2>/dev/null || printf '/tmp/tt_b_%%s' "$$")`, bodyTmp))
	prologue = append(prologue, fmt.Sprintf(`%s=$(mktemp 2>/dev/null || printf '/tmp/tt_h_%%s' "$$")`, hdrsTmp))
	// curl runs once; -o writes body, -D writes headers, -w prints HTTP status.
	// On any curl failure we treat the status as 0 so the caller can detect it
	// with `r.ok` or `r.status == 0`.
	prologue = append(prologue,
		fmt.Sprintf(`%s=$(curl -sS -L -o "$%s" -D "$%s" -w '%%{http_code}' "$%s" 2>/dev/null) || %s=0`,
			statusTmp, bodyTmp, hdrsTmp, urlVar, statusTmp))
	// Materialise the Response record fields into __ret__*.
	prologue = append(prologue, fmt.Sprintf(`__ret__status=$%s`, statusTmp))
	prologue = append(prologue, fmt.Sprintf(`__ret__body=$(cat "$%s" 2>/dev/null || printf '')`, bodyTmp))
	prologue = append(prologue, fmt.Sprintf(`__ret__headers=$(cat "$%s" 2>/dev/null || printf '')`, hdrsTmp))
	prologue = append(prologue, fmt.Sprintf(`__ret__ok=$(( __ret__status >= 200 && __ret__status < 300 ))`))
	prologue = append(prologue, fmt.Sprintf(`rm -f "$%s" "$%s"`, bodyTmp, hdrsTmp))
	// Snapshot into a fresh prefix, mirroring user-function record returns.
	out := g.tmp("rec")
	for _, f := range []string{"status", "ok", "body", "headers"} {
		prologue = append(prologue, fmt.Sprintf(`%s__%s="${__ret__%s}"`, out, f, f))
	}
	return exprValue{prologue: prologue, value: out, form: formRecord}
}

// --- condition compilation --------------------------------------------------

type condValue struct {
	prologue []string
	test     string // a shell test command (or chain) usable in `if X; then`
}

// compileCond produces a sh test command for an expression in boolean context.
func (g *Generator) compileCond(e ast.Expr) condValue {
	switch e := e.(type) {
	case *ast.BoolLit:
		if e.Value {
			return condValue{test: "true"}
		}
		return condValue{test: "false"}
	case *ast.UnaryExpr:
		if e.Op == token.Bang {
			inner := g.compileCond(e.Operand)
			// POSIX rejects stacked `!` (e.g. `! ! cmd`). When the inner test
			// already starts with a `!`, wrap it in a brace group so the outer
			// negation applies to a compound command instead.
			test := inner.test
			if strings.HasPrefix(test, "!") {
				test = "{ " + test + "; }"
			}
			return condValue{prologue: inner.prologue, test: "! " + test}
		}
	case *ast.BinaryExpr:
		switch e.Op {
		case token.AndAnd:
			lv := g.compileCond(e.Lhs)
			rv := g.compileCond(e.Rhs)
			return condValue{
				prologue: concatPrologues(lv.prologue, rv.prologue),
				test:     "{ " + lv.test + "; } && { " + rv.test + "; }",
			}
		case token.OrOr:
			lv := g.compileCond(e.Lhs)
			rv := g.compileCond(e.Rhs)
			return condValue{
				prologue: concatPrologues(lv.prologue, rv.prologue),
				test:     "{ " + lv.test + "; } || { " + rv.test + "; }",
			}
		case token.Eq, token.Neq, token.Lt, token.Lte, token.Gt, token.Gte:
			return g.compileCmpCond(e)
		}
	}
	// Fallback: evaluate to a 0/1 value and test it.
	v := g.compileExpr(e)
	// For simple identifiers (bool/number locals) we can test directly.
	if len(v.prologue) == 0 && (v.form == formArith || v.form == formBool) && isSimpleIdent(v.value) {
		return condValue{test: fmt.Sprintf("[ \"$%s\" = 1 ]", v.value)}
	}
	t := g.tmp("cond")
	prologue := append([]string{}, v.prologue...)
	prologue = append(prologue, fmt.Sprintf("%s=%s", t, v.assignmentRHS()))
	return condValue{prologue: prologue, test: fmt.Sprintf("[ \"$%s\" = 1 ]", t)}
}

func (g *Generator) compileCmpCond(b *ast.BinaryExpr) condValue {
	// Null comparisons compile to a check on the optional side's null flag.
	// We dispatch BEFORE compiling both sides because the null literal has no
	// useful value form to compute.
	if cond, ok := g.tryCompileNullCond(b); ok {
		return cond
	}
	// Float (or mixed) comparisons go through awk in test position too.
	lt := g.info.Types[b.Lhs]
	rt := g.info.Types[b.Rhs]
	if lt == types.Float || rt == types.Float {
		v := g.floatCompare(b)
		return condValue{
			prologue: v.prologue,
			test:     fmt.Sprintf(`[ "$((%s))" = 1 ]`, v.value),
		}
	}
	lv := g.compileExpr(b.Lhs)
	rv := g.compileExpr(b.Rhs)
	prologue := concatPrologues(lv.prologue, rv.prologue)
	if lt == types.String {
		switch b.Op {
		case token.Eq, token.Neq:
			op := "="
			if b.Op == token.Neq {
				op = "!="
			}
			return condValue{
				prologue: prologue,
				test:     fmt.Sprintf("[ \"%s\" %s \"%s\" ]", lv.shString(), op, rv.shString()),
			}
		}
		return condValue{
			prologue: prologue,
			test:     awkStringCmpTest(b.Op, lv.shString(), rv.shString()),
		}
	}
	op := ""
	switch b.Op {
	case token.Eq:
		op = "-eq"
	case token.Neq:
		op = "-ne"
	case token.Lt:
		op = "-lt"
	case token.Lte:
		op = "-le"
	case token.Gt:
		op = "-gt"
	case token.Gte:
		op = "-ge"
	}
	return condValue{
		prologue: prologue,
		test:     fmt.Sprintf("[ %s %s %s ]", asTestNum(lv), op, asTestNum(rv)),
	}
}

// --- helpers ----------------------------------------------------------------

// asArith returns a value's representation suitable for use inside an
// arithmetic context (`$(( ... ))`). For arithmetic forms this is the raw
// expression; for string forms we coerce by trusting the user (sh arithmetic
// will error at runtime if the contents aren't numeric).
func asArith(v exprValue) string {
	switch v.form {
	case formArith, formBool:
		return v.value
	default:
		return v.value
	}
}

// asArithStr returns the value as a quotable shell fragment when the consumer
// expects a string. For arithmetic forms this wraps in $((..)).
func asArithStr(v exprValue) string {
	switch v.form {
	case formArith, formBool:
		return "$((" + v.value + "))"
	default:
		return v.value
	}
}

// asArithExpansion: used inside `[ ... -eq ... ]` where we need a $(( )) form
// or a quoted expansion. For both forms we render arithmetic-as-expansion so
// `[ ` sees a numeric literal at runtime.
func asArithExpansion(v exprValue) string {
	switch v.form {
	case formArith, formBool:
		if isIntLiteral(v.value) {
			return v.value
		}
		return "$((" + v.value + "))"
	default:
		return v.value
	}
}

// asTestNum returns the operand form for numeric test contexts like
// `[ "$i" -eq "$j" ]`. Simple identifiers use "$name" instead of the
// heavier "$((name))"; complex expressions still use $((...)).
func asTestNum(v exprValue) string {
	switch v.form {
	case formArith, formBool:
		if isIntLiteral(v.value) {
			return v.value
		}
		if isSimpleIdent(v.value) {
			return `"$` + v.value + `"`
		}
		return `"$((` + v.value + `))"`
	default:
		return v.value
	}
}

// isSimpleIdent reports whether s is a plain shell identifier (no operators,
// no parens, no special characters). Used to decide whether "$name" is safe
// in a test context.
func isSimpleIdent(s string) bool {
	if s == "" {
		return false
	}
	c := s[0]
	if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_') {
		return false
	}
	for i := 1; i < len(s); i++ {
		c = s[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
			return false
		}
	}
	return true
}

// awkStringCmpOp returns the awk comparison operator for an ordering token.
func awkStringCmpOp(k token.Kind) string {
	switch k {
	case token.Lt:
		return "<"
	case token.Lte:
		return "<="
	case token.Gt:
		return ">"
	case token.Gte:
		return ">="
	}
	return "=="
}

// awkStringCmpTest emits a shell command (suitable after `if`) that exits 0
// when the string comparison `lhs op rhs` is true. Operands are passed via
// environment variables to sidestep awk's -v escape rules.
func awkStringCmpTest(op token.Kind, lhs, rhs string) string {
	return fmt.Sprintf(
		`__a="%s" __b="%s" awk 'BEGIN{exit !(ENVIRON["__a"] %s ENVIRON["__b"])}'`,
		lhs, rhs, awkStringCmpOp(op))
}

// awkStringCmpAssign emits a shell statement that sets `target` to 1 (true) or
// 0 (false) for the string comparison `lhs op rhs`.
func awkStringCmpAssign(target string, op token.Kind, lhs, rhs string) string {
	return fmt.Sprintf(
		`%s=$(__a="%s" __b="%s" awk 'BEGIN{print (ENVIRON["__a"] %s ENVIRON["__b"]) ? 1 : 0}')`,
		target, lhs, rhs, awkStringCmpOp(op))
}

// arithPrec gives operator precedence in sh arithmetic. Higher binds tighter.
// We mirror the table in POSIX so we only emit parentheses where actually
// needed.
func arithPrec(k token.Kind) int {
	switch k {
	case token.OrOr:
		return 1
	case token.AndAnd:
		return 2
	case token.Eq, token.Neq:
		return 3
	case token.Lt, token.Lte, token.Gt, token.Gte:
		return 4
	case token.Plus, token.Minus:
		return 5
	case token.Star, token.Slash, token.Percent:
		return 6
	}
	return 100
}

// arithGroup returns the value formatted for use as one operand of `op`,
// adding parentheses only if the inner expression's precedence is lower than
// `op`'s. `leftSide` distinguishes left- vs right-hand operands so we get
// correct grouping for left-associative operators.
func arithGroup(v exprValue, op token.Kind, leftSide bool) string {
	switch v.form {
	case formArith, formBool:
		// Detect a binary operator at the top of v.value by looking for the
		// substring of any operator we may have produced. This is a heuristic;
		// we err on the side of adding parens when uncertain.
		inner, ok := topArithOp(v.value)
		if ok {
			outerP := arithPrec(op)
			innerP := arithPrec(inner)
			needParen := innerP < outerP
			if !needParen && innerP == outerP && !leftSide {
				// right-hand side of a same-precedence left-assoc op needs parens.
				needParen = true
			}
			if needParen {
				return "(" + v.value + ")"
			}
		}
		return v.value
	default:
		// String forms appear here only via asArith; quote-safe rendering.
		return v.value
	}
}

// topArithOp tries to identify the outermost binary operator in a value the
// codegen produced. It only handles values produced by arithOp/logicOp above —
// terminals (numbers, identifiers, parenthesized groups) return false.
func topArithOp(value string) (token.Kind, bool) {
	// Skip a leading `!` (unary).
	s := value
	if strings.HasPrefix(s, "!(") {
		return token.Bang, true
	}
	// Walk the string respecting parens and find a top-level operator.
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '(' {
			depth++
			continue
		}
		if c == ')' {
			depth--
			continue
		}
		if depth != 0 {
			continue
		}
		// 2-char operators first
		if i+1 < len(s) {
			two := s[i : i+2]
			switch two {
			case "==":
				return token.Eq, true
			case "!=":
				return token.Neq, true
			case "<=":
				return token.Lte, true
			case ">=":
				return token.Gte, true
			case "&&":
				return token.AndAnd, true
			case "||":
				return token.OrOr, true
			}
		}
		switch c {
		case '+':
			return token.Plus, true
		case '-':
			// could be unary; if it's the first char, treat as terminal
			if i == 0 {
				continue
			}
			return token.Minus, true
		case '*':
			return token.Star, true
		case '/':
			return token.Slash, true
		case '%':
			return token.Percent, true
		case '<':
			return token.Lt, true
		case '>':
			return token.Gt, true
		}
	}
	return 0, false
}

func arithSym(k token.Kind) string {
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
	}
	return "?"
}

// shName maps a tartalo identifier to a sh variable/function name. For now
// the mapping is identity, but reserved sh names get a __t_ prefix.
func shName(name string) string {
	if shReserved[name] {
		return "__t_" + name
	}
	return name
}

var shReserved = map[string]bool{
	"if": true, "then": true, "else": true, "fi": true,
	"for": true, "do": true, "done": true,
	"while": true, "until": true, "case": true, "esac": true,
	"function": true, "return": true,
	"local": true, "export": true, "readonly": true,
	"true": true, "false": true,
	"in": true, "select": true, "time": true,
}

// shQuoteDouble wraps `body` in double quotes, escaping the four characters
// that have meaning inside double-quoted strings.
func shQuoteDouble(body string) string {
	return `"` + body + `"`
}

// escapeForDoubleQuoted escapes literal text so it can be embedded inside a
// double-quoted shell string without the shell reinterpreting it.
func escapeForDoubleQuoted(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\\', '"', '$', '`':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
