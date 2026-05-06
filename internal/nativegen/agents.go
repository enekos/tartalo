package nativegen

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/loader"
)

// Agent-platform state on the native Generator. Mirrors the sh codegen's
// fields so the two backends keep the same surface area; nativegen.go's
// Generator struct stays unchanged — these are stored on a sidecar struct
// that the Generator embeds via the methods below using a single map field
// could be cleaner, but flat fields keep the runtime emission readable.

// agentRef is the (user-name, Go-func-name, decl) triple driving the
// spawn-agent dispatcher.
type agentRefNative struct {
	Name   string
	GoName string
	Decl   *ast.FuncDecl
}

// initAgentPlatform pre-walks every loaded module to (a) collect agent
// declarations for the spawn dispatcher and (b) build the toolSchemas() JSON
// blob ahead of any user code emission. Also pre-sets the usesAgentXxx
// feature flags by name-matching builtin calls so the runtime emission step
// only ships helpers that are actually needed.
func (g *Generator) initAgentPlatform(modules []*loader.Module) {
	g.preScanAgentBuiltins(modules)
	var entries []map[string]any
	for _, m := range modules {
		for _, d := range m.File.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Kind == ast.FuncKindPlain {
				continue
			}
			if fd.Kind == ast.FuncKindAgent {
				g.agents = append(g.agents, agentRefNative{
					Name:   fd.Name,
					GoName: g.goFuncName(m, fd.Name),
					Decl:   fd,
				})
			}
			params := make([]map[string]any, 0, len(fd.Params))
			for _, p := range fd.Params {
				params = append(params, map[string]any{
					"name": p.Name,
					"type": typeExprText(p.TypeAnn),
				})
			}
			entry := map[string]any{
				"name":    fd.Name,
				"kind":    fd.Kind.String(),
				"params":  params,
				"returns": typeExprText(fd.Result),
			}
			if fd.Description != "" {
				entry["description"] = fd.Description
			}
			if len(fd.Effects) > 0 {
				entry["effects"] = fd.Effects
			}
			if fd.Budget > 0 {
				entry["budget"] = fd.Budget
			}
			entries = append(entries, entry)
		}
	}
	if len(entries) == 0 {
		g.toolSchemasJSON = "[]"
		return
	}
	if b, err := json.Marshal(entries); err == nil {
		g.toolSchemasJSON = string(b)
	} else {
		g.toolSchemasJSON = "[]"
	}
}

func (g *Generator) preScanAgentBuiltins(modules []*loader.Module) {
	for _, m := range modules {
		for _, d := range m.File.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				g.scanAgentBlock(d.Body)
			case *ast.TestDecl:
				g.scanAgentBlock(d.Body)
			case *ast.VarDecl:
				g.scanAgentExpr(d.Value)
			}
		}
	}
}

func (g *Generator) scanAgentBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		g.scanAgentStmt(st)
	}
}

func (g *Generator) scanAgentStmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.DeclStmt:
		if s.Decl != nil {
			g.scanAgentExpr(s.Decl.Value)
		}
	case *ast.ExprStmt:
		g.scanAgentExpr(s.X)
	case *ast.AssignStmt:
		g.scanAgentExpr(s.Value)
	case *ast.FieldAssignStmt:
		g.scanAgentExpr(s.Target)
		g.scanAgentExpr(s.Value)
	case *ast.ReturnStmt:
		g.scanAgentExpr(s.Value)
	case *ast.IfStmt:
		g.scanAgentExpr(s.Cond)
		g.scanAgentBlock(s.Then)
		g.scanAgentBlock(s.Else)
	case *ast.ForStmt:
		g.scanAgentExpr(s.Iter)
		g.scanAgentBlock(s.Body)
	case *ast.MatchStmt:
		g.scanAgentExpr(s.Subject)
		for _, c := range s.Cases {
			g.scanAgentBlock(c.Body)
		}
	case *ast.DeferStmt:
		g.scanAgentBlock(s.Body)
	case *ast.Block:
		g.scanAgentBlock(s)
	}
}

func (g *Generator) scanAgentExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *ast.CallExpr:
		if id, ok := e.Callee.(*ast.Ident); ok {
			if sym := g.info.Uses[id]; sym != nil && sym.IsBuiltin {
				switch id.Name {
				case "llm":
					g.usesAgentLLM = true
				case "approval":
					g.usesAgentApproval = true
				case "trace":
					g.usesAgentTrace = true
				case "spawnAgent":
					g.usesAgentSpawn = true
				case "mockLlm", "mockLlmCalls":
					g.usesAgentLLM = true
					g.usesMockLlm = true
				}
			}
		}
		g.scanAgentExpr(e.Callee)
		for _, a := range e.Args {
			g.scanAgentExpr(a)
		}
	case *ast.BinaryExpr:
		g.scanAgentExpr(e.Lhs)
		g.scanAgentExpr(e.Rhs)
	case *ast.UnaryExpr:
		g.scanAgentExpr(e.Operand)
	case *ast.IndexExpr:
		g.scanAgentExpr(e.Target)
		g.scanAgentExpr(e.Index)
	case *ast.FieldExpr:
		g.scanAgentExpr(e.Target)
	case *ast.RangeExpr:
		g.scanAgentExpr(e.Start)
		g.scanAgentExpr(e.End)
	case *ast.ArrayLit:
		for _, x := range e.Elems {
			g.scanAgentExpr(x)
		}
	case *ast.StringLit:
		for _, p := range e.Parts {
			g.scanAgentExpr(p)
		}
	case *ast.CmdLit:
		for _, p := range e.Parts {
			g.scanAgentExpr(p)
		}
	case *ast.CoalesceExpr:
		g.scanAgentExpr(e.Lhs)
		g.scanAgentExpr(e.Rhs)
	case *ast.UnwrapExpr:
		g.scanAgentExpr(e.Operand)
	case *ast.TryExpr:
		g.scanAgentExpr(e.Operand)
	case *ast.RecordLit:
		for _, f := range e.Fields {
			g.scanAgentExpr(f.Value)
		}
	}
}

// typeExprText is shared with the sh codegen — but lives in two packages
// because Go forbids cross-internal-package private exports. Keep both
// implementations in sync.
func typeExprText(t ast.TypeExpr) string {
	switch tt := t.(type) {
	case *ast.TypeName:
		return tt.Name
	case *ast.ArrayType:
		return typeExprText(tt.Elem) + "[]"
	case *ast.OptionalType:
		return typeExprText(tt.Elem) + "?"
	case *ast.FuncType:
		var sb strings.Builder
		sb.WriteString("func(")
		for i, p := range tt.Params {
			if i > 0 {
				sb.WriteString(", ")
			}
			sb.WriteString(typeExprText(p))
		}
		sb.WriteString("): ")
		sb.WriteString(typeExprText(tt.Result))
		return sb.String()
	case *ast.RecordType:
		return "<record>"
	case *ast.SumType:
		return "<sum>"
	}
	return "?"
}

// goFuncName mirrors checker.MangledName for the top-level functions the
// nativegen produces. Keeps sh's case dispatch and Go's func-pointer
// dispatcher symmetric.
func (g *Generator) goFuncName(m *loader.Module, name string) string {
	return "tt_" + checker.MangledName(m, name)
}

// emitAgentRuntime writes the Go-side runtime helpers and constants for the
// agent platform. Called from emitProgram after Pass 2 globals so all flags
// are settled.
func (g *Generator) emitAgentRuntime() {
	if g.toolSchemasJSON != "" && g.toolSchemasJSON != "[]" {
		// Emit the schema as a Go const string. Because the JSON we emit is
		// always valid (encoding/json output), the Go raw-string literal is
		// safe — JSON has no backticks.
		g.writeLine("")
		g.writeLine("const _tt_toolSchemas = `" + g.toolSchemasJSON + "`")
		g.writeLine("")
	}

	if g.usesAgentSpawn {
		// Generate a switch dispatcher. Each agent's signature is
		// (string) -> string (enforced by the checker on calls; user-side
		// agents may have richer signatures, but for this v1 we expose the
		// (string) -> string subset to spawnAgent — multi-arg spawning is
		// a future extension).
		g.writeLine("")
		g.writeLine("func _tt_spawnAgent(name string, input string) string {")
		g.indent++
		g.writeLine("switch name {")
		for _, a := range g.agents {
			// Only emit a case for agents whose signature is (string) -> string;
			// other shapes don't fit the spawn protocol and stay callable
			// directly by name.
			if len(a.Decl.Params) != 1 {
				continue
			}
			tn, _ := a.Decl.Params[0].TypeAnn.(*ast.TypeName)
			rn, _ := a.Decl.Result.(*ast.TypeName)
			if tn == nil || rn == nil || tn.Name != "string" || rn.Name != "string" {
				continue
			}
			g.writeLine(fmt.Sprintf("case %q:", a.Name))
			g.indent++
			g.writeLine("return " + a.GoName + "(input)")
			g.indent--
		}
		g.writeLine("}")
		// Avoid an unused-parameter complaint by referencing input on the
		// fallthrough path; this also gives a cleaner error message.
		g.writeLine(`fmt.Fprintf(os.Stderr, "tartalo: unknown agent: %s\n", name)`)
		g.writeLine(`os.Exit(1)`)
		g.writeLine(`_ = input`)
		g.writeLine(`return ""`)
		g.indent--
		g.writeLine("}")
		g.writeLine("")
		g.addImport("fmt")
		g.addImport("os")
	}
}

// emitAgentRuntimeAppendix writes the static helper functions to the file
// (appended after the in-memory body). Wraps llm/approval/trace and the
// mock state for llm.
func (g *Generator) emitAgentRuntimeAppendix(out *strings.Builder) {
	if g.usesAgentLLM {
		out.WriteString(runtimeLLM)
		if g.emitMode == EmitTest {
			out.WriteString(dispatcherLLMTest)
			if g.usesMockLlm {
				out.WriteString(mockSettersLLM)
			}
		} else {
			out.WriteString(`func _tt_llm(prompt string) string { return _tt_llm_real(prompt) }` + "\n\n")
		}
	}
	if g.usesAgentApproval {
		out.WriteString(runtimeApproval)
	}
	if g.usesAgentTrace {
		out.WriteString(runtimeTrace)
	}
}

const runtimeLLM = `func _tt_llm_real(prompt string) string {
	cmd := os.Getenv("TARTALO_LLM_CMD")
	if cmd == "" {
		cmd = "claude -p"
	}
	var shBin string
	var shArgs []string
	if runtime.GOOS == "windows" {
		shBin = "cmd"; shArgs = []string{"/c", cmd}
	} else {
		shBin = "/bin/sh"; shArgs = []string{"-c", cmd}
	}
	c := exec.Command(shBin, shArgs...)
	c.Stdin = strings.NewReader(prompt)
	out, err := c.Output()
	if err != nil {
		fmt.Fprintf(os.Stderr, "tartalo: llm: %v\n", err)
		os.Exit(1)
	}
	return string(out)
}

`

const dispatcherLLMTest = `func _tt_llm(prompt string) string {
	_tt_mockLlmCallsLog = append(_tt_mockLlmCallsLog, prompt)
	for _, r := range _tt_mockLlmRules {
		if r.pat.MatchString(prompt) {
			return r.resp
		}
	}
	panic(_tt_testFailure{msg: "tartalo: unmocked llm call: " + prompt})
}

`

const mockSettersLLM = `type _tt_mockLlmRule struct {
	pat  *regexp.Regexp
	resp string
}

var _tt_mockLlmRules []_tt_mockLlmRule
var _tt_mockLlmCallsLog []string

func _tt_mockLlm(pat string, resp string) {
	r, err := regexp.Compile(pat)
	if err != nil {
		panic(_tt_testFailure{msg: "tartalo: mockLlm: invalid regex: " + pat})
	}
	_tt_mockLlmRules = append(_tt_mockLlmRules, _tt_mockLlmRule{pat: r, resp: resp})
}

func _tt_mockLlmCalls() []string {
	out := make([]string, len(_tt_mockLlmCallsLog))
	copy(out, _tt_mockLlmCallsLog)
	return out
}

`

const runtimeApproval = `func _tt_approval(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s [y/N] ", prompt)
	tty, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	var reader = os.Stdin
	if err == nil {
		reader = tty
		defer tty.Close()
	}
	buf := make([]byte, 16)
	n, _ := reader.Read(buf)
	if n == 0 {
		return false
	}
	switch string(buf[:n]) {
	case "y\n", "Y\n", "yes\n", "Yes\n", "YES\n", "y", "Y":
		return true
	}
	return false
}

`

const runtimeTrace = `func _tt_trace(label string, value string) {
	path := os.Getenv("TARTALO_TRACE")
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	rec := map[string]any{
		"ts":    time.Now().Unix(),
		"label": label,
		"value": value,
	}
	b, _ := json.Marshal(rec)
	f.Write(b)
	f.Write([]byte{'\n'})
}

`
