package codegen

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/loader"
)

// collectAgentsAndSchemas walks every loaded module's declarations and (a)
// records each agent in declaration order so __tt_spawn_agent can dispatch on
// names, and (b) serialises all tool/agent schemas into a single JSON string
// consumed by toolSchemas(). Both are precomputed at compile time so callers
// pay zero runtime overhead.
func (g *Generator) collectAgentsAndSchemas(modules []*loader.Module) {
	var entries []map[string]any
	for _, m := range modules {
		for _, d := range m.File.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Kind == ast.FuncKindPlain {
				continue
			}
			if fd.Kind == ast.FuncKindAgent {
				g.agents = append(g.agents, agentRef{
					Name:   fd.Name,
					ShName: checker.MangledName(m, fd.Name),
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
	b, err := json.Marshal(entries)
	if err != nil {
		g.toolSchemasJSON = "[]"
		return
	}
	g.toolSchemasJSON = string(b)
}

// typeExprText pretty-prints a TypeExpr for the schema. Surface form is
// stable across runs so external tools can pattern-match on it.
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

// emitAgentRuntime writes the schema constant, llm/approval/trace helpers and
// the spawn-agent dispatcher to the generator output. Each block is gated on
// whether the corresponding builtin was actually compiled.
func (g *Generator) emitAgentRuntime() {
	if g.toolSchemasJSON != "" && g.toolSchemasJSON != "[]" {
		g.writeLine(fmt.Sprintf("__TT_SCHEMAS='%s'", g.toolSchemasJSON))
		g.writeLine("")
	}

	if g.usesLLM {
		g.writeLines([]string{
			`# llm(prompt) lowers to __tt_llm_call. In test mode the helper consults`,
			`# __tt_mock_llm (one rule per line: pattern<TAB>response) and aborts on`,
			`# an unmocked prompt. In normal mode it pipes the prompt to`,
			`# $TARTALO_LLM_CMD (default: claude -p) and returns the stdout.`,
			`__tt_llm_call() {`,
			`  __tt_p="$1"`,
		})
		if g.testMode {
			g.writeLines([]string{
				`  if [ -n "${__tt_mock_llm:-}" ]; then`,
				`    __tt_match=$(printf '%s' "$__tt_mock_llm" | awk -F'\t' -v p="$__tt_p" '$0!="" { if (match(p, $1)) { print $2; exit } }')`,
				`    __tt_mock_llm_calls="${__tt_mock_llm_calls}${__tt_p}` + "\n" + `"`,
				`    if [ -n "$__tt_match" ]; then printf '%s' "$__tt_match"; return 0; fi`,
				`    printf 'tartalo: unmocked llm call: %s\n' "$__tt_p" >&2; exit 1`,
				`  fi`,
			})
		}
		g.writeLines([]string{
			`  __tt_cmd="${TARTALO_LLM_CMD:-claude -p}"`,
			`  printf '%s' "$__tt_p" | sh -c "$__tt_cmd"`,
			`}`,
			"",
		})
	}

	if g.usesApproval {
		g.writeLines([]string{
			`# approval(prompt) prints the prompt on stderr, reads y/n from /dev/tty`,
			`# (falling back to stdin), and returns 1 for yes / 0 for no.`,
			`__tt_approval() {`,
			`  printf '%s [y/N] ' "$1" >&2`,
			`  __tt_ans=""`,
			`  if [ -r /dev/tty ]; then`,
			`    read __tt_ans </dev/tty || true`,
			`  else`,
			`    read __tt_ans || true`,
			`  fi`,
			`  case "$__tt_ans" in`,
			`    y|Y|yes|Yes|YES) printf 1 ;;`,
			`    *)               printf 0 ;;`,
			`  esac`,
			`}`,
			"",
		})
	}

	if g.usesTrace {
		g.writeLines([]string{
			`# trace(label, value) appends one NDJSON record to $TARTALO_TRACE if`,
			`# the env var is set. No-op otherwise.`,
			`__tt_trace() {`,
			`  if [ -n "${TARTALO_TRACE:-}" ]; then`,
			`    __tt_l=$(printf '%s' "$1" | sed 's/\\/\\\\/g; s/"/\\"/g')`,
			`    __tt_v=$(printf '%s' "$2" | sed 's/\\/\\\\/g; s/"/\\"/g; s/	/\\t/g')`,
			`    __tt_ts=$(date +%s 2>/dev/null || printf 0)`,
			`    printf '{"ts":%s,"label":"%s","value":"%s"}\n' "$__tt_ts" "$__tt_l" "$__tt_v" >> "$TARTALO_TRACE"`,
			`  fi`,
			`}`,
			"",
		})
	}

	if g.usesSpawnAgent {
		g.writeLine(`# spawnAgent(name, input) dispatches to a declared agent by name. The`)
		g.writeLine(`# compiler enumerates every agent at build time so the dispatcher is`)
		g.writeLine(`# a flat case statement — no eval, no string-to-function lookup.`)
		g.writeLine(`__tt_spawn_agent() {`)
		g.writeLine(`  __tt_an="$1"`)
		g.writeLine(`  __tt_ai="$2"`)
		g.writeLine(`  case "$__tt_an" in`)
		for _, a := range g.agents {
			g.writeLine(fmt.Sprintf(`    %s) %s "$__tt_ai" ;;`, shCaseLiteral(a.Name), a.ShName))
		}
		g.writeLine(`    *) printf 'tartalo: unknown agent: %s\n' "$__tt_an" >&2; exit 1 ;;`)
		g.writeLine(`  esac`)
		g.writeLine(`}`)
		g.writeLine("")
	}
}

// shCaseLiteral escapes a name for the LHS of a sh `case` arm.
func shCaseLiteral(name string) string {
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_':
		default:
			return "'" + escForSingleQuoted(name) + "'"
		}
	}
	return name
}

// --- builtin lowerings -------------------------------------------------------

func (g *Generator) compileLlm(args []exprValue, prologue []string) exprValue {
	g.usesLLM = true
	out := g.tmp("llm")
	prologue = append(prologue, fmt.Sprintf("%s=$(__tt_llm_call %s)", out, args[0].assignmentRHS()))
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}

func (g *Generator) compileApproval(args []exprValue, prologue []string) exprValue {
	g.usesApproval = true
	out := g.tmp("apr")
	prologue = append(prologue, fmt.Sprintf("%s=$(__tt_approval %s)", out, args[0].assignmentRHS()))
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formBool}
}

func (g *Generator) compileTrace(args []exprValue, prologue []string) exprValue {
	g.usesTrace = true
	prologue = append(prologue, fmt.Sprintf("__tt_trace %s %s", args[0].assignmentRHS(), args[1].assignmentRHS()))
	return exprValue{prologue: prologue, value: "", form: formStr}
}

func (g *Generator) compileSpawnAgent(args []exprValue, prologue []string) exprValue {
	g.usesSpawnAgent = true
	out := g.tmp("ag")
	prologue = append(prologue, fmt.Sprintf("__tt_spawn_agent %s %s", args[0].assignmentRHS(), args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(`%s="$__ret"`, out))
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}

func (g *Generator) compileToolSchemas(prologue []string) exprValue {
	if g.toolSchemasJSON == "" || g.toolSchemasJSON == "[]" {
		return exprValue{prologue: prologue, value: "[]", form: formStr}
	}
	return exprValue{prologue: prologue, value: "${__TT_SCHEMAS}", form: formStr}
}

func (g *Generator) compileMockLlm(args []exprValue, prologue []string) exprValue {
	patVar := g.tmp("mll_p")
	respVar := g.tmp("mll_r")
	prologue = append(prologue, fmt.Sprintf("%s=%s", patVar, args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf("%s=%s", respVar, args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(
		`__tt_mock_llm="${__tt_mock_llm}$%s	$%s
"`, patVar, respVar))
	return exprValue{prologue: prologue, value: "", form: formStr}
}

func (g *Generator) compileMockLlmCalls(prologue []string) exprValue {
	out := g.tmp("mllc")
	prologue = append(prologue, fmt.Sprintf(
		`%s=$(printf '%%s' "${__tt_mock_llm_calls:-}" | awk 'NF{print}')`, out))
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}
