package nativegen

import (
	"strconv"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/types"
)

// compileCall produces the Go expression text for a Tartalo call. Builtins
// dispatch to compileBuiltin for stdlib lowering; user functions become
// ordinary Go calls.
func (g *Generator) compileCall(e *ast.CallExpr) string {
	id, _ := e.Callee.(*ast.Ident)
	if id == nil {
		return "/* unsupported call */ nil"
	}
	sym := g.info.Uses[id]
	if sym != nil && sym.IsBuiltin {
		return g.compileBuiltin(sym, e)
	}
	var ft *types.Func
	if sym != nil {
		ft, _ = sym.Type.(*types.Func)
	}
	var fn string
	if sym != nil && sym.Module != nil {
		fn = "tt_" + checker.MangledName(sym.Module, sym.Name)
	} else if sym != nil {
		fn = "tt_" + sym.Name
	} else {
		fn = "tt_" + id.Name
	}
	if len(e.Args) == 0 {
		return fn + "()"
	}
	if len(e.Args) == 1 {
		argExpr := g.compileExpr(e.Args[0])
		if ft != nil && len(ft.Params) > 0 {
			argExpr = g.coerce(argExpr, g.info.Types[e.Args[0]], ft.Params[0])
		}
		totalLen := len(fn) + 1 + len(argExpr) + 1
		if totalLen <= 64 {
			var buf [64]byte
			n := copy(buf[:], fn)
			buf[n] = '('
			n++
			n += copy(buf[n:], argExpr)
			buf[n] = ')'
			n++
			return string(buf[:n])
		}
		return fn + "(" + argExpr + ")"
	}
	var b strings.Builder
	b.WriteString(fn)
	b.WriteString("(")
	for i, a := range e.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		argExpr := g.compileExpr(a)
		if ft != nil && i < len(ft.Params) {
			argExpr = g.coerce(argExpr, g.info.Types[a], ft.Params[i])
		}
		b.WriteString(argExpr)
	}
	b.WriteString(")")
	return b.String()
}

// compileBuiltin lowers each registered builtin to a Go expression. Most are
// thin wrappers around stdlib calls or runtime helpers in runtime.go.
func (g *Generator) compileBuiltin(sym *checker.Symbol, e *ast.CallExpr) string {
	var args []string
	if len(e.Args) > 0 {
		args = make([]string, len(e.Args))
		for i, a := range e.Args {
			args[i] = g.compileExpr(a)
		}
	}
	argTypes := make([]types.Type, len(e.Args))
	for i, a := range e.Args {
		argTypes[i] = g.info.Types[a]
	}
	switch sym.Name {

	// --- core ---
	case "echo":
		g.addImport("fmt")
		return "fmt.Println(" + g.toString(args[0], argTypes[0]) + ")"
	case "eprint":
		g.addImport("fmt")
		g.addImport("os")
		return "fmt.Fprintln(os.Stderr, " + g.toString(args[0], argTypes[0]) + ")"
	case "exit":
		g.addImport("os")
		return "func() { os.Exit(int(" + args[0] + ")) }()"
	case "str":
		return g.toString(args[0], argTypes[0])
	case "num":
		g.addImport("strconv")
		g.addImport("strings")
		return "func() int64 { v, _ := strconv.ParseInt(strings.TrimSpace(" + args[0] + "), 10, 64); return v }()"
	case "len":
		return "int64(len(" + args[0] + "))"
	case "env":
		g.usesRuntimeEnv = true
		g.usesRuntimePtr = true
		g.addImport("os")
		return "_tt_env(" + args[0] + ")"
	case "args":
		g.usesRuntimeArgs = true
		g.addImport("os")
		return "_tt_args()"
	case "now":
		g.usesRuntimeNow = true
		g.addImport("time")
		return "_tt_now()"
	case "sleep":
		g.addImport("time")
		return "func() { time.Sleep(time.Duration(" + args[0] + ") * time.Second) }()"

	// --- string operations ---
	case "upper":
		g.addImport("strings")
		return "strings.ToUpper(" + args[0] + ")"
	case "lower":
		g.addImport("strings")
		return "strings.ToLower(" + args[0] + ")"
	case "trim":
		g.addImport("strings")
		return "strings.TrimSpace(" + args[0] + ")"
	case "replace":
		g.addImport("strings")
		return "strings.ReplaceAll(" + args[0] + ", " + args[1] + ", " + args[2] + ")"
	case "contains":
		g.addImport("strings")
		return "strings.Contains(" + args[0] + ", " + args[1] + ")"
	case "startsWith":
		g.addImport("strings")
		return "strings.HasPrefix(" + args[0] + ", " + args[1] + ")"
	case "endsWith":
		g.addImport("strings")
		return "strings.HasSuffix(" + args[0] + ", " + args[1] + ")"
	case "split":
		g.addImport("strings")
		return "strings.Split(" + args[0] + ", " + args[1] + ")"
	case "join":
		g.addImport("strings")
		return "strings.Join(" + args[0] + ", " + args[1] + ")"
	case "slice":
		return "func() string { _s := " + args[0] + "; _i := int(" + args[1] + "); _j := int(" + args[2] +
			"); if _i < 0 { _i = 0 }; if _j > len(_s) { _j = len(_s) }; if _i > _j { _i = _j }; return _s[_i:_j] }()"

	// --- subprocess ---
	case "exec":
		g.usesRuntimeExec = true
		g.addImport("bytes")
		g.addImport("os/exec")
		g.addImport("runtime")
		return "_tt_exec(" + args[0] + ")"
	case "execTimeout":
		g.usesRuntimeExecTimeout = true
		g.addImport("bytes")
		g.addImport("context")
		g.addImport("os/exec")
		g.addImport("runtime")
		g.addImport("time")
		return "_tt_execTimeout(" + args[0] + ", " + args[1] + ")"

	// --- file I/O ---
	case "readFile":
		g.usesRuntimeFile = true
		g.addImport("fmt")
		g.addImport("io")
		g.addImport("os")
		g.addImport("strings")
		return "_tt_readFile(" + args[0] + ")"
	case "writeFile":
		g.usesRuntimeFile = true
		g.addImport("fmt")
		g.addImport("io")
		g.addImport("os")
		g.addImport("strings")
		return "func() { _tt_writeFile(" + args[0] + ", " + args[1] + ") }()"
	case "appendFile":
		g.usesRuntimeFile = true
		g.addImport("fmt")
		g.addImport("io")
		g.addImport("os")
		g.addImport("strings")
		return "func() { _tt_appendFile(" + args[0] + ", " + args[1] + ") }()"
	case "removeFile":
		g.addImport("os")
		return "func() { os.Remove(" + args[0] + ") }()"
	case "mkdir":
		g.addImport("os")
		return "func() { os.MkdirAll(" + args[0] + ", 0o755) }()"
	case "listDir":
		g.usesRuntimeFile = true
		g.addImport("fmt")
		g.addImport("io")
		g.addImport("os")
		g.addImport("strings")
		return "_tt_listDir(" + args[0] + ")"
	case "exists":
		g.addImport("os")
		return "func() bool { _, err := os.Stat(" + args[0] + "); return err == nil }()"
	case "isFile":
		g.addImport("os")
		return "func() bool { i, err := os.Stat(" + args[0] + "); return err == nil && i.Mode().IsRegular() }()"
	case "isDir":
		g.addImport("os")
		return "func() bool { i, err := os.Stat(" + args[0] + "); return err == nil && i.IsDir() }()"
	case "stat":
		g.usesRuntimeStat = true
		g.addImport("os")
		g.addImport("strconv")
		return "_tt_stat(" + args[0] + ")"
	case "readStdin":
		g.usesRuntimeFile = true
		g.addImport("fmt")
		g.addImport("io")
		g.addImport("os")
		g.addImport("strings")
		return "_tt_readStdin()"

	// --- path manipulation ---
	case "pathJoin":
		g.usesRuntimePath = true
		g.addImport("path/filepath")
		g.addImport("strings")
		return "_tt_pathJoin(" + args[0] + ", " + args[1] + ")"
	case "basename":
		g.addImport("path/filepath")
		return "filepath.Base(" + args[0] + ")"
	case "dirname":
		g.addImport("path/filepath")
		return "filepath.Dir(" + args[0] + ")"
	case "extname":
		g.usesRuntimePath = true
		g.addImport("path/filepath")
		g.addImport("strings")
		return "_tt_extname(" + args[0] + ")"
	case "parsePath":
		g.usesRuntimePath = true
		g.addImport("path/filepath")
		g.addImport("strings")
		return "_tt_parsePath(" + args[0] + ")"

	// --- time formatting ---
	case "formatTime":
		g.usesRuntimeFormatTime = true
		g.addImport("strings")
		g.addImport("time")
		return "_tt_formatTime(" + args[0] + ", " + args[1] + ")"

	// --- JSON ---
	case "jsonGet":
		g.usesRuntimeJSON = true
		g.usesRuntimePtr = true
		g.addImport("encoding/json")
		g.addImport("strconv")
		g.addImport("strings")
		return "_tt_jsonGet(" + args[0] + ", " + args[1] + ")"
	case "jsonHas":
		g.usesRuntimeJSON = true
		g.addImport("encoding/json")
		g.addImport("strconv")
		g.addImport("strings")
		return "_tt_jsonHas(" + args[0] + ", " + args[1] + ")"
	case "jsonArray":
		g.usesRuntimeJSON = true
		g.addImport("encoding/json")
		g.addImport("strconv")
		g.addImport("strings")
		return "_tt_jsonArray(" + args[0] + ", " + args[1] + ")"
	case "jsonEscape":
		g.usesRuntimeJSON = true
		g.addImport("encoding/json")
		g.addImport("strconv")
		g.addImport("strings")
		return "_tt_jsonEscape(" + args[0] + ")"

	// --- regex ---
	case "regexMatch":
		g.usesRuntimeRegex = true
		g.addImport("regexp")
		g.addImport("strings")
		return "_tt_regexMatch(" + args[0] + ", " + args[1] + ")"
	case "regexFind":
		g.usesRuntimeRegex = true
		g.addImport("regexp")
		g.addImport("strings")
		return "_tt_regexFind(" + args[0] + ", " + args[1] + ")"
	case "regexFindAll":
		g.usesRuntimeRegex = true
		g.addImport("regexp")
		g.addImport("strings")
		return "_tt_regexFindAll(" + args[0] + ", " + args[1] + ")"
	case "regexReplace":
		g.usesRuntimeRegex = true
		g.addImport("regexp")
		g.addImport("strings")
		return "_tt_regexReplace(" + args[0] + ", " + args[1] + ", " + args[2] + ")"

	// --- float helpers ---
	case "floatOf":
		g.usesRuntimeFloat = true
		g.addImport("math")
		g.addImport("strconv")
		g.addImport("strings")
		return "_tt_floatOf(" + args[0] + ")"
	case "intOf":
		g.usesRuntimeFloat = true
		g.addImport("math")
		g.addImport("strconv")
		g.addImport("strings")
		return "_tt_intOf(" + args[0] + ")"
	case "parseFloat":
		g.usesRuntimeFloat = true
		g.addImport("math")
		g.addImport("strconv")
		g.addImport("strings")
		return "_tt_parseFloat(" + args[0] + ")"
	case "formatFloat":
		g.usesRuntimeFloat = true
		g.addImport("math")
		g.addImport("strconv")
		g.addImport("strings")
		return "_tt_formatFloat(" + args[0] + ", " + args[1] + ")"
	case "floor":
		g.usesRuntimeFloat = true
		g.addImport("math")
		g.addImport("strconv")
		g.addImport("strings")
		return "_tt_floor(" + args[0] + ")"
	case "ceil":
		g.usesRuntimeFloat = true
		g.addImport("math")
		g.addImport("strconv")
		g.addImport("strings")
		return "_tt_ceil(" + args[0] + ")"
	case "round":
		g.usesRuntimeFloat = true
		g.addImport("math")
		g.addImport("strconv")
		g.addImport("strings")
		return "_tt_round(" + args[0] + ")"

	// --- higher-order ---
	case "map":
		g.usesRuntimeHigherOrder = true
		return "_tt_map(" + args[0] + ", " + args[1] + ")"
	case "filter":
		g.usesRuntimeHigherOrder = true
		return "_tt_filter(" + args[0] + ", " + args[1] + ")"
	case "reduce":
		g.usesRuntimeHigherOrder = true
		// Tartalo: reduce(arr, init, fn). The init's type is the accumulator
		// type; fn must take (acc, elem) -> acc. Coerce init to match the
		// declared accumulator type if needed (number → float widening, etc.).
		init := args[1]
		if len(e.Args) >= 3 {
			if ft, ok := argTypes[2].(*types.Func); ok && ft.Result != nil {
				init = g.coerce(init, argTypes[1], ft.Result)
			}
		}
		return "_tt_reduce(" + args[0] + ", " + init + ", " + args[2] + ")"

	// --- HTTP ---
	case "fetch":
		g.usesRuntimeFetch = true
		g.addImport("io")
		g.addImport("net/http")
		g.addImport("strings")
		g.addImport("time")
		return "_tt_fetch(" + args[0] + ")"

	// --- test assertions (only legal inside `test "..." { ... }`) ---
	case "assertEq":
		g.usesRuntimeTestState = true
		g.addImport("fmt")
		return "_tt_assertEq(" + assertArg(args[0], argTypes[0], argTypes[1]) + ", " +
			assertArg(args[1], argTypes[1], argTypes[0]) + ", " + g.callLoc(e) + ")"
	case "assertNe":
		g.usesRuntimeTestState = true
		g.addImport("fmt")
		return "_tt_assertNe(" + assertArg(args[0], argTypes[0], argTypes[1]) + ", " +
			assertArg(args[1], argTypes[1], argTypes[0]) + ", " + g.callLoc(e) + ")"
	case "check":
		g.usesRuntimeTestState = true
		return "_tt_check(" + args[0] + ", " + g.callLoc(e) + ")"
	case "fail":
		g.usesRuntimeTestState = true
		return "_tt_fail(" + args[0] + ", " + g.callLoc(e) + ")"
	case "skip":
		g.usesRuntimeTestState = true
		return "_tt_skip(" + args[0] + ")"

	// --- mock setters / inspectors (test-only; checker enforces scope) ---
	case "mockExec":
		g.usesMockExec = true
		g.usesRuntimeExec = true
		g.addImport("regexp")
		return "_tt_mockExec(" + args[0] + ", " + args[1] + ")"
	case "mockExecCalls":
		g.usesMockExec = true
		g.usesRuntimeExec = true
		g.addImport("regexp")
		return "_tt_mockExecCalls()"
	case "mockFetch":
		g.usesMockFetch = true
		g.usesRuntimeFetch = true
		g.addImport("regexp")
		// fetch's runtime impl pulls in its own imports when called; if
		// the test only mocks fetch (and never invokes it), we still need
		// the impl so the dispatcher has somewhere to fall through to.
		g.addImport("io")
		g.addImport("net/http")
		g.addImport("strings")
		g.addImport("time")
		return "_tt_mockFetch(" + args[0] + ", " + args[1] + ")"
	case "mockFetchCalls":
		g.usesMockFetch = true
		g.usesRuntimeFetch = true
		g.addImport("regexp")
		g.addImport("io")
		g.addImport("net/http")
		g.addImport("strings")
		g.addImport("time")
		return "_tt_mockFetchCalls()"
	case "mockEnv":
		g.usesMockEnv = true
		g.usesRuntimeEnv = true
		g.usesRuntimePtr = true
		g.addImport("os")
		// The value parameter is `string?`. Auto-wrap a bare string and
		// coerce a `null` literal to `(*string)(nil)` so callers can write
		// `mockEnv("HOME", "x")` or `mockEnv("HOME", null)` naturally.
		val := g.coerce(args[1], argTypes[1], &types.Optional{Elem: types.String})
		return "_tt_mockEnv(" + args[0] + ", " + val + ")"
	case "mockReadFile":
		g.usesMockReadFile = true
		g.usesRuntimeFile = true
		g.addImport("regexp")
		g.addImport("fmt")
		g.addImport("io")
		g.addImport("os")
		g.addImport("strings")
		return "_tt_mockReadFile(" + args[0] + ", " + args[1] + ")"
	case "mockReadFileCalls":
		g.usesMockReadFile = true
		g.usesRuntimeFile = true
		g.addImport("regexp")
		g.addImport("fmt")
		g.addImport("io")
		g.addImport("os")
		g.addImport("strings")
		return "_tt_mockReadFileCalls()"
	case "mockNow":
		g.usesMockNow = true
		g.usesRuntimeNow = true
		g.addImport("time")
		return "_tt_mockNow(" + args[0] + ")"
	case "mockArgs":
		g.usesMockArgs = true
		g.usesRuntimeArgs = true
		g.addImport("os")
		return "_tt_mockArgs(" + args[0] + ")"
	case "mockReadStdin":
		g.usesMockStdin = true
		g.usesRuntimeFile = true
		g.addImport("fmt")
		g.addImport("io")
		g.addImport("os")
		g.addImport("strings")
		return "_tt_mockReadStdin(" + args[0] + ")"
	}

	return `func() interface{} { panic("tartalo native: builtin not yet supported: ` + sym.Name + `") }()`
}

// callLoc renders a `file:line:col` literal for the call site, used by the
// assertion helpers to print clickable locations on failure.
func (g *Generator) callLoc(e *ast.CallExpr) string {
	p := e.LParenPos
	return strconv.Quote(p.File + ":" + itoa(p.Line) + ":" + itoa(p.Col))
}

// assertArg widens a numeric assertion argument to float64 when its peer is
// a float — this matches the sh backend's awk-based cross-numeric compare.
// Other types pass through unchanged.
func assertArg(expr string, self, peer types.Type) string {
	if self == types.Number && peer == types.Float {
		return "float64(" + expr + ")"
	}
	return expr
}
