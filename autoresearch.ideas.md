# Deferred Optimization Ideas for Tartalo Nativegen

## Completed (kept) — Complex Scripts Session

- **Adaptive pre-growth for g.out and file builders** — estimate output size from AST complexity (decl count × heuristic) to right-size Builder buffer and avoid incremental growth/reallocation for large scripts
- **fastQuote: avoid strconv.Quote allocation for simple ASCII strings** — stack buffer for strings without escapes; falls back to strconv.Quote for complex strings
- **Stack-buffer name mangling** — goLocalName/goVarName/goFuncName/goTypeName/goFieldName use `[64]byte` stack buffer for names ≤61 chars, avoiding string concat allocation
- **Replace fmt.Sprintf with string concat** — in agent dispatchers and budget emission, avoids allocation
- **Use strings.Builder in joinComma/patternLiteral** — eliminates O(n²) string concatenation from `+=` in loops
- **Write directly to Builder in emitIf/emitMatch/emitAgentRuntime** — avoid temporary string allocations from `+` concatenation before writeLine/WriteString
- **Remove defer in emitFunc** — explicit save/restore of `currentAgent` avoids defer closure allocation and runtime overhead (~50-100ns per function)
- **Remove redundant preScanAgentBuiltins** — compileBuiltin already sets `usesAgentXxx` flags lazily during emission; the pre-scan was doing O(n) AST traversal with zero benefit
- **Expand compileIdent fast path to all single-letter names** — covers a-z plus safe multi-letter names (total, sum, res, err, ok, idx, tmp, cur, max, min, xs, fn, out, in, count, item, main)
- **Stack buffer for 0-arg and 2-arg calls in compileCall** — avoids Builder allocation for common call patterns
- **Track VarDecl presence during Pass 1** — eliminates separate scan for globals detection

## Completed (discarded) — Complex Scripts Session

- **Conditional predeclared types** — scanning type info for usage is O(n) overhead that outweighs savings from skipping ~1KB of dead code; also buggy (missed PathParts via parsePath)
- **Larger fixed pre-growth (8192 bytes)** — over-allocates for small scripts, hurting cache locality and increasing suite_bytes by 44%
- **Precomputed indent strings in writeLine** — no measurable benefit; WriteString for short strings may be slower than multiple WriteByte
- **hasAgents check before initAgentPlatform** — adds an extra scan that doesn't improve performance

## Key Learnings — Complex Scripts Session

- **Suite benchmark reduces noise** — running 10 diverse scripts in one loop gives more stable relative measurements than single-script benchmarks
- **Adaptive pre-growth is better than fixed** — 2048 is good for small scripts but causes 3-4 growths for large ones; estimating from AST size is optimal
- **Redundant AST scans are expensive** — preScanAgentBuiltins was doing full recursive traversal but compileBuiltin already handled flag-setting
- **defer has real overhead even in emitter** — not just closure alloc but runtime bookkeeping; explicit save/restore is faster
- **macOS variance is still high** — ~1-2µs per script, ~10-20µs for the suite; need 5×+ confidence for keep decisions
- **fast path for identifiers must not bypass narrowing** — compileIdent's optional-narrowing check must run before fast path returns; adding names like `key` that can be optional variables causes test failures

## Remaining Opportunities (high effort / marginal gain)

- **binExpr/compileIdent/compileCall allocation avoidance**: would need major refactor to write expressions directly to output Builder instead of returning strings
- **Generator pooling**: reuse Generator across compilations to eliminate New() allocation and imports slice alloc
- **Batch writeLine calls**: combine multiple writeLine into single WriteString for common sequences (e.g., function prologue)
- **Conditional predeclared types (v2)**: track usage during type info construction in checker, not during emission
- **Custom JSON builder for toolSchemasJSON**: replace json.Marshal with direct string building for the very regular tool schema structure
- **Inline goType for common composite types**: arrays/optionals of records/sums do string concat; could use a small cache

## Current State — Complex Scripts

- Baseline: 53,821 ns/op, 806 allocs/op, 138,755 bytes/op
- Current: ~45,482 ns/op, 793 allocs/op, 132,070 bytes/op
- Improvement: ~15.5% time, ~1.6% allocs, ~4.8% bytes
- Best individual script improvements: pandas -16.1%, numpy -17.9%, mega -14.7%, agent_demo -9.5%
- Limiting factor: macOS scheduler noise makes sub-1% changes hard to distinguish
