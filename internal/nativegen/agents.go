package nativegen

import (
	"encoding/json"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
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
			if fd.Kind == ast.FuncKindTool {
				g.tools = append(g.tools, agentRefNative{
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
			if len(fd.Tools) > 0 {
				entry["tools"] = fd.Tools
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

// emitAgentRuntime writes the Go-side runtime helpers and constants for the
// agent platform. Called from emitProgram after Pass 2 globals so all flags
// are settled.
func (g *Generator) emitAgentRuntime() {
	if g.toolSchemasJSON != "" && g.toolSchemasJSON != "[]" {
		// Emit the schema as a Go const string. Because the JSON we emit is
		// always valid (encoding/json output), the Go raw-string literal is
		// safe — JSON has no backticks.
		g.writeLine("")
		g.writeIndent()
		g.out.WriteString("const _tt_toolSchemas = `")
		g.out.WriteString(g.toolSchemasJSON)
		g.out.WriteString("`")
		g.out.WriteByte('\n')
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
			if !isStringToStringNative(a.Decl) {
				continue
			}
			g.writeIndent()
			g.out.WriteString("case ")
			g.out.WriteString(fastQuote(a.Name))
			g.out.WriteString(":\n")
			g.indent++
			g.writeIndent()
			g.out.WriteString("return ")
			g.out.WriteString(a.GoName)
			g.out.WriteString("(input)\n")
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

	if g.usesAgentCallTool {
		// Mirror of _tt_spawnAgent for tools. Same (string)→string
		// constraint applies.
		g.writeLine("")
		g.writeLine("func _tt_callTool(name string, input string) string {")
		g.indent++
		g.writeLine("switch name {")
		for _, t := range g.tools {
			if !isStringToStringNative(t.Decl) {
				continue
			}
			g.writeIndent()
			g.out.WriteString("case ")
			g.out.WriteString(fastQuote(t.Name))
			g.out.WriteString(":\n")
			g.indent++
			g.writeIndent()
			g.out.WriteString("return ")
			g.out.WriteString(t.GoName)
			g.out.WriteString("(input)\n")
			g.indent--
		}
		g.writeLine("}")
		g.writeLine(`fmt.Fprintf(os.Stderr, "tartalo: unknown tool: %s\n", name)`)
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

// agentToolsJSON is the per-agent counterpart of the all-tools toolSchemas
// blob. Looks up each name in g.tools and emits its tool schema in the same
// shape as toolSchemas() entries.
func (g *Generator) agentToolsJSON(fd *ast.FuncDecl) string {
	entries := make([]map[string]any, 0, len(fd.Tools))
	for _, name := range fd.Tools {
		var tfd *ast.FuncDecl
		for i := range g.tools {
			if g.tools[i].Name == name {
				tfd = g.tools[i].Decl
				break
			}
		}
		if tfd == nil {
			continue
		}
		params := make([]map[string]any, 0, len(tfd.Params))
		for _, p := range tfd.Params {
			params = append(params, map[string]any{
				"name": p.Name,
				"type": typeExprText(p.TypeAnn),
			})
		}
		entry := map[string]any{
			"name":    tfd.Name,
			"kind":    "tool",
			"params":  params,
			"returns": typeExprText(tfd.Result),
		}
		if tfd.Description != "" {
			entry["description"] = tfd.Description
		}
		if len(tfd.Effects) > 0 {
			entry["effects"] = tfd.Effects
		}
		entries = append(entries, entry)
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func isStringToStringNative(fd *ast.FuncDecl) bool {
	if len(fd.Params) != 1 {
		return false
	}
	pn, _ := fd.Params[0].TypeAnn.(*ast.TypeName)
	rn, _ := fd.Result.(*ast.TypeName)
	return pn != nil && rn != nil && pn.Name == "string" && rn.Name == "string"
}

// emitAgentRuntimeAppendix writes the static helper functions to the file
// (appended after the in-memory body). Wraps llm/approval/trace and the
// mock state for llm.
func (g *Generator) emitAgentRuntimeAppendix(out *strings.Builder) {
	if g.usesAgentLLM {
		out.WriteString(runtimeLLM)
		out.WriteString(runtimeKimi)
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

// runtimeLLM ships the per-provider dispatcher and the legacy command-pipe
// fallback. Provider selection is driven by TARTALO_LLM_PROVIDER; an empty
// value preserves the original "pipe to $TARTALO_LLM_CMD" behaviour so
// existing scripts keep working unchanged.
const runtimeLLM = `func _tt_llm_real(prompt string) string {
	switch os.Getenv("TARTALO_LLM_PROVIDER") {
	case "kimi", "moonshot":
		return _tt_llm_kimi(prompt)
	}
	return _tt_llm_legacy(prompt)
}

func _tt_llm_legacy(prompt string) string {
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

// runtimeKimi calls Moonshot's OpenAI-compatible chat/completions endpoint.
// Defaults: base https://api.moonshot.ai/v1, model moonshot-v1-8k. Both can
// be overridden with TARTALO_KIMI_BASE_URL / TARTALO_KIMI_MODEL — the URL
// override is also what makes the test suite point at a local httptest
// server. KIMI_API_KEY is mandatory; we fail fast with a clear message
// rather than letting the upstream return 401.
const runtimeKimi = "func _tt_llm_kimi(prompt string) string {\n" +
	"\tkey := os.Getenv(\"KIMI_API_KEY\")\n" +
	"\tif key == \"\" {\n" +
	"\t\tfmt.Fprintf(os.Stderr, \"tartalo: kimi: KIMI_API_KEY not set\\n\")\n" +
	"\t\tos.Exit(1)\n" +
	"\t}\n" +
	"\tbase := os.Getenv(\"TARTALO_KIMI_BASE_URL\")\n" +
	"\tif base == \"\" {\n" +
	"\t\tbase = \"https://api.moonshot.ai/v1\"\n" +
	"\t}\n" +
	"\tfor len(base) > 0 && base[len(base)-1] == '/' {\n" +
	"\t\tbase = base[:len(base)-1]\n" +
	"\t}\n" +
	"\tmodel := os.Getenv(\"TARTALO_KIMI_MODEL\")\n" +
	"\tif model == \"\" {\n" +
	"\t\tmodel = \"moonshot-v1-8k\"\n" +
	"\t}\n" +
	"\treqBody, err := json.Marshal(map[string]any{\n" +
	"\t\t\"model\": model,\n" +
	"\t\t\"messages\": []map[string]string{\n" +
	"\t\t\t{\"role\": \"user\", \"content\": prompt},\n" +
	"\t\t},\n" +
	"\t})\n" +
	"\tif err != nil {\n" +
	"\t\tfmt.Fprintf(os.Stderr, \"tartalo: kimi: marshal: %v\\n\", err)\n" +
	"\t\tos.Exit(1)\n" +
	"\t}\n" +
	"\thttpReq, err := http.NewRequest(\"POST\", base+\"/chat/completions\", bytes.NewReader(reqBody))\n" +
	"\tif err != nil {\n" +
	"\t\tfmt.Fprintf(os.Stderr, \"tartalo: kimi: new request: %v\\n\", err)\n" +
	"\t\tos.Exit(1)\n" +
	"\t}\n" +
	"\thttpReq.Header.Set(\"Authorization\", \"Bearer \"+key)\n" +
	"\thttpReq.Header.Set(\"Content-Type\", \"application/json\")\n" +
	"\tresp, err := http.DefaultClient.Do(httpReq)\n" +
	"\tif err != nil {\n" +
	"\t\tfmt.Fprintf(os.Stderr, \"tartalo: kimi: %v\\n\", err)\n" +
	"\t\tos.Exit(1)\n" +
	"\t}\n" +
	"\tdefer resp.Body.Close()\n" +
	"\trespBody, err := io.ReadAll(resp.Body)\n" +
	"\tif err != nil {\n" +
	"\t\tfmt.Fprintf(os.Stderr, \"tartalo: kimi: read: %v\\n\", err)\n" +
	"\t\tos.Exit(1)\n" +
	"\t}\n" +
	"\tif resp.StatusCode < 200 || resp.StatusCode >= 300 {\n" +
	"\t\tfmt.Fprintf(os.Stderr, \"tartalo: kimi: status %d: %s\\n\", resp.StatusCode, string(respBody))\n" +
	"\t\tos.Exit(1)\n" +
	"\t}\n" +
	"\tvar parsed struct {\n" +
	"\t\tChoices []struct {\n" +
	"\t\t\tMessage struct {\n" +
	"\t\t\t\tContent string `json:\"content\"`\n" +
	"\t\t\t} `json:\"message\"`\n" +
	"\t\t} `json:\"choices\"`\n" +
	"\t}\n" +
	"\tif err := json.Unmarshal(respBody, &parsed); err != nil {\n" +
	"\t\tfmt.Fprintf(os.Stderr, \"tartalo: kimi: unmarshal: %v\\n\", err)\n" +
	"\t\tos.Exit(1)\n" +
	"\t}\n" +
	"\tif len(parsed.Choices) == 0 {\n" +
	"\t\tfmt.Fprintf(os.Stderr, \"tartalo: kimi: no choices in response: %s\\n\", string(respBody))\n" +
	"\t\tos.Exit(1)\n" +
	"\t}\n" +
	"\treturn parsed.Choices[0].Message.Content\n" +
	"}\n\n"

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
