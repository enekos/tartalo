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
// names, (b) serialises all tool/agent schemas into a single JSON string
// consumed by toolSchemas(), and (c) pre-scans every body for calls to the
// agent-platform builtins (llm/approval/trace/spawnAgent) so the
// usesXxx flags are set before emit order matters. The pre-scan is a
// purely syntactic name match — false positives only cost an unused helper
// definition, never correctness.
func (g *Generator) collectAgentsAndSchemas(modules []*loader.Module) {
	g.preScanBuiltinUsage(modules)
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
			if fd.Kind == ast.FuncKindTool {
				g.tools = append(g.tools, agentRef{
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
			`# any unmatched prompt — never falls through to a real model in tests.`,
			`# In run mode it dispatches on $TARTALO_LLM_PROVIDER: "kimi" calls`,
			`# Moonshot's OpenAI-compatible API (requires curl + KIMI_API_KEY);`,
			`# anything else pipes the prompt to $TARTALO_LLM_CMD (default: claude -p).`,
			`# We deliberately avoid a $(...) capture in the call site so the`,
			`# test-mode mock state survives.`,
			`__tt_llm_call() {`,
			`  __tt_p="$1"`,
		})
		if g.testMode {
			g.writeLines([]string{
				`  __tt_mock_llm_calls="${__tt_mock_llm_calls}${__tt_p}` + "\n" + `"`,
				`  if [ -n "${__tt_mock_llm:-}" ]; then`,
				`    __tt_match=$(printf '%s' "$__tt_mock_llm" | awk -F'\t' -v p="$__tt_p" '$0!="" { if (match(p, $1)) { print $2; exit } }')`,
				`    if [ -n "$__tt_match" ]; then __ret="$__tt_match"; return 0; fi`,
				`  fi`,
				`  printf 'tartalo: unmocked llm call: %s\n' "$__tt_p" >&2; exit 1`,
				`}`,
				"",
			})
		} else {
			g.writeLines([]string{
				`  case "${TARTALO_LLM_PROVIDER:-}" in`,
				`    kimi|moonshot) __tt_llm_kimi "$__tt_p"; return $? ;;`,
				`  esac`,
				`  __tt_cmd="${TARTALO_LLM_CMD:-claude -p}"`,
				`  __ret=$(printf '%s' "$__tt_p" | sh -c "$__tt_cmd")`,
				`}`,
				"",
				`# __tt_llm_kimi calls Moonshot's OpenAI-compatible chat/completions`,
				`# endpoint via curl. Defaults: base https://api.moonshot.ai/v1, model`,
				`# moonshot-v1-8k. Both are overridable via TARTALO_KIMI_BASE_URL`,
				`# and TARTALO_KIMI_MODEL — the URL override is also what makes`,
				`# tests point at a local server. curl is required; KIMI_API_KEY`,
				`# is mandatory. The two awk passes JSON-encode the outgoing prompt`,
				`# and decode the assistant content out of the response without`,
				`# pulling in jq or python. Set TARTALO_LLM_STREAM=1 to switch`,
				`# to the SSE streaming variant: deltas appear on stderr as they`,
				`# arrive (so a human watching sees progress) while the full`,
				`# accumulated content still lands in __ret for the caller.`,
				`__tt_llm_kimi() {`,
				`  __tt_p="$1"`,
				`  if [ -z "${KIMI_API_KEY:-}" ]; then`,
				`    printf 'tartalo: kimi: KIMI_API_KEY not set\n' >&2; exit 1`,
				`  fi`,
				`  if ! command -v curl >/dev/null 2>&1; then`,
				`    printf 'tartalo: kimi: curl is required on the shell target (use --target=native for a no-deps build)\n' >&2; exit 1`,
				`  fi`,
				`  __tt_base="${TARTALO_KIMI_BASE_URL:-https://api.moonshot.ai/v1}"`,
				`  __tt_base="${__tt_base%/}"`,
				`  __tt_model="${TARTALO_KIMI_MODEL:-moonshot-v1-8k}"`,
				`  __tt_enc=$(printf '%s' "$__tt_p" | awk '`,
				`    { if (NR > 1) out = out "\\n"`,
				`      n = length($0)`,
				`      for (i = 1; i <= n; i++) {`,
				`        c = substr($0, i, 1)`,
				`        if      (c == "\\") out = out "\\\\"`,
				`        else if (c == "\"") out = out "\\\""`,
				`        else if (c == "\t") out = out "\\t"`,
				`        else if (c == "\r") out = out "\\r"`,
				`        else                out = out c`,
				`      }`,
				`    }`,
				`    END { printf "%s", out }`,
				`  ')`,
				`  if [ "${TARTALO_LLM_STREAM:-0}" = "1" ]; then`,
				`    __tt_body=$(printf '{"model":"%s","stream":true,"messages":[{"role":"user","content":"%s"}]}' "$__tt_model" "$__tt_enc")`,
				`    __ret=$(printf '%s' "$__tt_body" | curl -sS --no-buffer -X POST \`,
				`      -H "Authorization: Bearer ${KIMI_API_KEY}" \`,
				`      -H "Content-Type: application/json" \`,
				`      --data-binary @- \`,
				`      "${__tt_base}/chat/completions" | __tt_kimi_sse)`,
				`    return 0`,
				`  fi`,
				`  __tt_body=$(printf '{"model":"%s","messages":[{"role":"user","content":"%s"}]}' "$__tt_model" "$__tt_enc")`,
				`  __tt_resp=$(printf '%s' "$__tt_body" | curl -sS -X POST \`,
				`    -H "Authorization: Bearer ${KIMI_API_KEY}" \`,
				`    -H "Content-Type: application/json" \`,
				`    --data-binary @- \`,
				`    "${__tt_base}/chat/completions")`,
				`  __tt_curl_status=$?`,
				`  if [ "$__tt_curl_status" -ne 0 ]; then`,
				`    printf 'tartalo: kimi: curl failed (exit %s)\n' "$__tt_curl_status" >&2; exit 1`,
				`  fi`,
				`  __ret=$(printf '%s' "$__tt_resp" | awk '`,
				`    { buf = buf $0 }`,
				`    END {`,
				`      i = index(buf, "\"choices\"")`,
				`      if (i == 0) { print "__TT_KIMI_NO_CHOICES__"; exit }`,
				`      s = substr(buf, i)`,
				`      i = index(s, "\"message\"")`,
				`      if (i == 0) { print "__TT_KIMI_NO_CHOICES__"; exit }`,
				`      s = substr(s, i)`,
				`      i = index(s, "\"content\"")`,
				`      if (i == 0) { print "__TT_KIMI_NO_CHOICES__"; exit }`,
				`      s = substr(s, i + length("\"content\""))`,
				`      while (length(s) > 0 && (substr(s,1,1) == " " || substr(s,1,1) == "\t" || substr(s,1,1) == "\n" || substr(s,1,1) == "\r" || substr(s,1,1) == ":")) s = substr(s, 2)`,
				`      if (substr(s, 1, 1) != "\"") { print "__TT_KIMI_NO_CHOICES__"; exit }`,
				`      s = substr(s, 2)`,
				`      out = ""; esc = 0; n = length(s)`,
				`      for (i = 1; i <= n; i++) {`,
				`        c = substr(s, i, 1)`,
				`        if (esc) {`,
				`          esc = 0`,
				`          if      (c == "n")  out = out "\n"`,
				`          else if (c == "t")  out = out "\t"`,
				`          else if (c == "r")  out = out "\r"`,
				`          else if (c == "\"") out = out "\""`,
				`          else if (c == "\\") out = out "\\"`,
				`          else if (c == "/")  out = out "/"`,
				`          else if (c == "b")  out = out sprintf("%c", 8)`,
				`          else if (c == "f")  out = out sprintf("%c", 12)`,
				`          else if (c == "u") {`,
				`            hex = substr(s, i + 1, 4); i += 4; v = 0`,
				`            for (k = 1; k <= length(hex); k++) {`,
				`              ch = substr(hex, k, 1)`,
				`              if      (ch >= "0" && ch <= "9") v = v*16 + (index("0123456789", ch) - 1)`,
				`              else if (ch >= "a" && ch <= "f") v = v*16 + (index("abcdef", ch) - 1) + 10`,
				`              else if (ch >= "A" && ch <= "F") v = v*16 + (index("ABCDEF", ch) - 1) + 10`,
				`            }`,
				`            if      (v < 128)   out = out sprintf("%c", v)`,
				`            else if (v < 2048)  out = out sprintf("%c%c", 192 + int(v/64), 128 + (v%64))`,
				`            else                out = out sprintf("%c%c%c", 224 + int(v/4096), 128 + int((v/64)%64), 128 + (v%64))`,
				`          }`,
				`          else out = out c`,
				`        } else if (c == "\\") esc = 1`,
				`        else if (c == "\"") break`,
				`        else out = out c`,
				`      }`,
				`      printf "%s", out`,
				`    }`,
				`  ')`,
				`  if [ "$__ret" = "__TT_KIMI_NO_CHOICES__" ]; then`,
				`    printf 'tartalo: kimi: no choices in response: %s\n' "$__tt_resp" >&2; exit 1`,
				`  fi`,
				`}`,
				"",
				`# __tt_kimi_sse consumes a server-sent-events stream from stdin,`,
				`# mirrors each delta.content chunk to stderr as it arrives, and`,
				`# writes the accumulated content to stdout. The CR strip handles`,
				`# CRLF-framed events; the case head matches the (rare) CR-less`,
				`# variant some servers emit.`,
				`__tt_kimi_sse() {`,
				`  __tt_acc=""`,
				`  __tt_cr=$(printf '\r')`,
				`  while IFS= read -r __tt_line || [ -n "$__tt_line" ]; do`,
				`    __tt_line=${__tt_line%"$__tt_cr"}`,
				`    case "$__tt_line" in`,
				`      "data: [DONE]"|"data:[DONE]") break ;;`,
				`      "data: "*) __tt_payload=${__tt_line#data: } ;;`,
				`      *) continue ;;`,
				`    esac`,
				`    __tt_chunk=$(printf '%s' "$__tt_payload" | awk '`,
				`      { buf = buf $0 }`,
				`      END {`,
				`        i = index(buf, "\"delta\"")`,
				`        if (i == 0) exit`,
				`        s = substr(buf, i)`,
				`        i = index(s, "\"content\"")`,
				`        if (i == 0) exit`,
				`        s = substr(s, i + length("\"content\""))`,
				`        while (length(s) > 0 && (substr(s,1,1) == " " || substr(s,1,1) == "\t" || substr(s,1,1) == "\n" || substr(s,1,1) == "\r" || substr(s,1,1) == ":")) s = substr(s, 2)`,
				`        if (substr(s, 1, 1) != "\"") exit`,
				`        s = substr(s, 2)`,
				`        out = ""; esc = 0; n = length(s)`,
				`        for (i = 1; i <= n; i++) {`,
				`          c = substr(s, i, 1)`,
				`          if (esc) {`,
				`            esc = 0`,
				`            if      (c == "n")  out = out "\n"`,
				`            else if (c == "t")  out = out "\t"`,
				`            else if (c == "r")  out = out "\r"`,
				`            else if (c == "\"") out = out "\""`,
				`            else if (c == "\\") out = out "\\"`,
				`            else if (c == "/")  out = out "/"`,
				`            else if (c == "b")  out = out sprintf("%c", 8)`,
				`            else if (c == "f")  out = out sprintf("%c", 12)`,
				`            else if (c == "u") {`,
				`              hex = substr(s, i + 1, 4); i += 4; v = 0`,
				`              for (k = 1; k <= length(hex); k++) {`,
				`                ch = substr(hex, k, 1)`,
				`                if      (ch >= "0" && ch <= "9") v = v*16 + (index("0123456789", ch) - 1)`,
				`                else if (ch >= "a" && ch <= "f") v = v*16 + (index("abcdef", ch) - 1) + 10`,
				`                else if (ch >= "A" && ch <= "F") v = v*16 + (index("ABCDEF", ch) - 1) + 10`,
				`              }`,
				`              if      (v < 128)   out = out sprintf("%c", v)`,
				`              else if (v < 2048)  out = out sprintf("%c%c", 192 + int(v/64), 128 + (v%64))`,
				`              else                out = out sprintf("%c%c%c", 224 + int(v/4096), 128 + int((v/64)%64), 128 + (v%64))`,
				`            }`,
				`            else out = out c`,
				`          } else if (c == "\\") esc = 1`,
				`          else if (c == "\"") break`,
				`          else out = out c`,
				`        }`,
				`        printf "%s", out`,
				`      }`,
				`    ')`,
				`    [ -n "$__tt_chunk" ] && printf '%s' "$__tt_chunk" >&2`,
				`    __tt_acc="${__tt_acc}${__tt_chunk}"`,
				`  done`,
				`  printf '%s' "$__tt_acc"`,
				`}`,
				"",
			})
		}
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

	if g.usesCallTool {
		// Same shape as __tt_spawn_agent but only enumerates tools whose
		// signature is (string)→string. Tools with richer shapes stay
		// directly callable but are unreachable through callTool.
		g.writeLine(`# callTool(name, input) dispatches to a declared tool by name. Only`)
		g.writeLine(`# (string)→string tools are reachable through this dispatcher.`)
		g.writeLine(`__tt_call_tool() {`)
		g.writeLine(`  __tt_tn="$1"`)
		g.writeLine(`  __tt_ti="$2"`)
		g.writeLine(`  case "$__tt_tn" in`)
		for _, t := range g.tools {
			if !isStringToString(t.Decl) {
				continue
			}
			g.writeLine(fmt.Sprintf(`    %s) %s "$__tt_ti" ;;`, shCaseLiteral(t.Name), t.ShName))
		}
		g.writeLine(`    *) printf 'tartalo: unknown tool: %s\n' "$__tt_tn" >&2; exit 1 ;;`)
		g.writeLine(`  esac`)
		g.writeLine(`}`)
		g.writeLine("")
	}
}

// isStringToString reports whether a func/tool declaration has the
// (string) -> string shape required by name-keyed dispatchers.
func isStringToString(fd *ast.FuncDecl) bool {
	if len(fd.Params) != 1 {
		return false
	}
	pn, _ := fd.Params[0].TypeAnn.(*ast.TypeName)
	rn, _ := fd.Result.(*ast.TypeName)
	return pn != nil && rn != nil && pn.Name == "string" && rn.Name == "string"
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
	// Inside an agent body with a declared budget, decrement and check the
	// per-invocation counter before each llm() call. The check is inlined
	// (not in __tt_llm_call) because the counter is a local of the agent
	// function — making it visible to a shared helper would mean exporting
	// it as a global, losing the per-invocation reset.
	if g.currentAgent != nil && g.currentAgent.Budget > 0 {
		prologue = append(prologue,
			`if [ "$__tt_budget" -le 0 ]; then `+
				`printf 'tartalo: agent %s exceeded llm budget of %d\n' `+
				shSingleQuote(g.currentAgent.Name)+` `+
				itoa64(g.currentAgent.Budget)+` >&2; exit 1; fi`,
			"__tt_budget=$((__tt_budget - 1))",
		)
	}
	// Call the helper as a function (no subshell capture) so it can update
	// per-test mock state (__tt_mock_llm_calls). The helper writes its
	// result into __ret; we snapshot that into a fresh tmp immediately so a
	// subsequent call doesn't clobber us.
	prologue = append(prologue, fmt.Sprintf("__tt_llm_call %s", args[0].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(`%s="$__ret"`, out))
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

// compileCallTool lowers callTool(name, input) — a name-keyed tool dispatcher
// mirroring spawnAgent. Restricted at the codegen level to (string)→string
// tools; tools with richer signatures are unreachable through callTool but
// remain callable directly. Unknown names abort the script.
func (g *Generator) compileCallTool(args []exprValue, prologue []string) exprValue {
	g.usesCallTool = true
	out := g.tmp("ct")
	prologue = append(prologue, fmt.Sprintf("__tt_call_tool %s %s", args[0].assignmentRHS(), args[1].assignmentRHS()))
	prologue = append(prologue, fmt.Sprintf(`%s="$__ret"`, out))
	return exprValue{prologue: prologue, value: "${" + out + "}", form: formStr}
}

// compileAgentTools resolves to a JSON literal containing the schemas of
// the surrounding agent's `uses (...)` tools, or "[]" when called outside an
// agent or when the agent has no uses clause. Resolution happens at compile
// time — the result is a constant string per call site.
func (g *Generator) compileAgentTools(prologue []string) exprValue {
	if g.currentAgent == nil || len(g.currentAgent.Tools) == 0 {
		return exprValue{prologue: prologue, value: "[]", form: formStr}
	}
	js := g.agentToolsJSON(g.currentAgent)
	v := g.tmp("at")
	prologue = append(prologue, fmt.Sprintf(`%s='%s'`, v, escForSingleQuoted(js)))
	return exprValue{prologue: prologue, value: "${" + v + "}", form: formStr}
}

// agentToolsJSON produces the per-agent tool-schema array. Looks up each
// name in the precomputed g.tools slice and serialises matching tool decls
// in the same shape as toolSchemas() entries so consumers can prompt-inject
// either without reformatting.
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

// preScanBuiltinUsage walks every declaration body in every module and sets
// the agent-platform feature flags whenever it sees a call to one of the
// agent-platform builtins. We resolve the call site through TypeInfo.Uses so
// a user-declared function called e.g. "trace" doesn't trigger us.
func (g *Generator) preScanBuiltinUsage(modules []*loader.Module) {
	for _, m := range modules {
		for _, d := range m.File.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				g.scanBlock(d.Body)
			case *ast.TestDecl:
				g.scanBlock(d.Body)
			case *ast.VarDecl:
				g.scanExpr(d.Value)
			}
		}
	}
}

func (g *Generator) scanBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, st := range b.Stmts {
		g.scanStmt(st)
	}
}

func (g *Generator) scanStmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.DeclStmt:
		if s.Decl != nil {
			g.scanExpr(s.Decl.Value)
		}
	case *ast.ExprStmt:
		g.scanExpr(s.X)
	case *ast.AssignStmt:
		g.scanExpr(s.Value)
	case *ast.FieldAssignStmt:
		g.scanExpr(s.Target)
		g.scanExpr(s.Value)
	case *ast.ReturnStmt:
		g.scanExpr(s.Value)
	case *ast.IfStmt:
		g.scanExpr(s.Cond)
		g.scanBlock(s.Then)
		g.scanBlock(s.Else)
	case *ast.ForStmt:
		g.scanExpr(s.Iter)
		g.scanBlock(s.Body)
	case *ast.WhileStmt:
		g.scanExpr(s.Cond)
		g.scanBlock(s.Body)
	case *ast.MatchStmt:
		g.scanExpr(s.Subject)
		for _, c := range s.Cases {
			g.scanBlock(c.Body)
		}
	case *ast.DeferStmt:
		g.scanBlock(s.Body)
	case *ast.Block:
		g.scanBlock(s)
	}
}

func (g *Generator) scanExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *ast.CallExpr:
		if id, ok := e.Callee.(*ast.Ident); ok {
			if sym := g.info.Uses[id]; sym != nil && sym.IsBuiltin {
				switch id.Name {
				case "llm", "mockLlm", "mockLlmCalls":
					g.usesLLM = true
				case "approval":
					g.usesApproval = true
				case "trace":
					g.usesTrace = true
				case "spawnAgent":
					g.usesSpawnAgent = true
				case "callTool":
					g.usesCallTool = true
				}
			}
		}
		g.scanExpr(e.Callee)
		for _, a := range e.Args {
			g.scanExpr(a)
		}
	case *ast.BinaryExpr:
		g.scanExpr(e.Lhs)
		g.scanExpr(e.Rhs)
	case *ast.UnaryExpr:
		g.scanExpr(e.Operand)
	case *ast.IndexExpr:
		g.scanExpr(e.Target)
		g.scanExpr(e.Index)
	case *ast.FieldExpr:
		g.scanExpr(e.Target)
	case *ast.RangeExpr:
		g.scanExpr(e.Start)
		g.scanExpr(e.End)
	case *ast.ArrayLit:
		for _, x := range e.Elems {
			g.scanExpr(x)
		}
	case *ast.StringLit:
		for _, p := range e.Parts {
			g.scanExpr(p)
		}
	case *ast.CmdLit:
		for _, p := range e.Parts {
			g.scanExpr(p)
		}
	case *ast.CoalesceExpr:
		g.scanExpr(e.Lhs)
		g.scanExpr(e.Rhs)
	case *ast.UnwrapExpr:
		g.scanExpr(e.Operand)
	case *ast.TryExpr:
		g.scanExpr(e.Operand)
	case *ast.RecordLit:
		for _, f := range e.Fields {
			g.scanExpr(f.Value)
		}
	}
}
