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
- **Replace json.Marshal with direct strings.Builder in agentToolsJSON** — eliminates map[string]any allocations and reflection overhead for per-agent tool schemas
- **Replace json.Marshal with direct strings.Builder in initAgentPlatform** — eliminates map[string]any allocations and reflection overhead for toolSchemasJSON
- **Combine Pass 1 and Pass 2 into single loop per module** — eliminates redundant AST traversal over all declarations
- **Inline goFuncName in emitFunc and emitProgram main() call** — avoids string allocation for every function declaration
- **Inline goFieldName in emitFieldAssign, compileRecordLit, compileField, emitTypeDecl** — avoids string allocation for every field access/assignment/literal; ~1600ns suite improvement
- **Inline goTypeName in emitTypeDecl, emitSumTypeDecl, compileRecordLit, compileVariantLit, emitFunc** — avoids string allocation for type names
- **Optimize entry module lookup for single-module programs** — fast path `modules[0]` avoids loop for the common case

## Completed (discarded) — Complex Scripts Session

- **Conditional predeclared types** — scanning type info for usage is O(n) overhead that outweighs savings from skipping ~1KB of dead code; also buggy (missed PathParts via parsePath)
- **Larger fixed pre-growth (8192 bytes)** — over-allocates for small scripts, hurting cache locality and increasing suite_bytes by 44%
- **Precomputed indent strings in writeLine** — no measurable benefit; WriteString for short strings may be slower than multiple WriteByte
- **hasAgents check before initAgentPlatform** — adds an extra scan that doesn't improve performance
- **Cache agentToolsJSON per agent** — correct but effect too small to measure on macOS due to high variance; better to optimize the JSON building itself
- **Use strings.Builder for function types in goType** — adds overhead for the common case of 0-2 parameters; no improvement
- **Simplify estimation loop to totalDecls*500** — less accurate, over-allocates for scripts with many type/var declarations
- **Switch-based writeLine** — worse than if-chain; hurts branch prediction
- **strings.IndexByte in fastQuote** — function call overhead exceeds savings from manual loop
- **file.Grow(g.out.Len()+1024)** — no improvement, possibly hurts cache
- **Pre-allocate agents/tools slices in New** — adds allocation overhead for scripts without agents; allocs increased
- **fastQuote lookup table for single-char strings** — init() allocates 128 strings; lookup not faster than stack buffer
- **Stack buffer for tmp() names** — no improvement; string concat for short names is already efficient

## Key Learnings — Complex Scripts Session

- **Suite benchmark reduces noise** — running 10 diverse scripts in one loop gives more stable relative measurements than single-script benchmarks
- **Adaptive pre-growth is better than fixed** — 2048 is good for small scripts but causes 3-4 growths for large ones; estimating from AST size is optimal
- **Redundant AST scans are expensive** — preScanAgentBuiltins was doing full recursive traversal but compileBuiltin already handled flag-setting
- **defer has real overhead even in emitter** — not just closure alloc but runtime bookkeeping; explicit save/restore is faster
- **macOS variance is still high** — ~1-2µs per script, ~10-20µs for the suite; need 5×+ confidence for keep decisions
- **fast path for identifiers must not bypass narrowing** — compileIdent's optional-narrowing check must run before fast path returns; adding names like `key` that can be optional variables causes test failures
- **json.Marshal with map[string]any is extremely allocation-heavy** — replacing it with direct strings.Builder in agentToolsJSON and initAgentPlatform yielded the two biggest single improvements in this session
- **Reflection-based JSON encoding dominates profiles** — encoding/json.mapEncoder.encode and reflect.copyVal were top allocators before the direct JSON builder optimization
- **Combining loops saves iteration overhead** — Pass 1+2 combination saved ~500ns by eliminating one full AST traversal
- **Inlining string-building helpers avoids heap allocation** — goFuncName, goFieldName, goTypeName all allocate via `string(buf[:n])`; inlining the prefix directly into Builder writes eliminates this allocation
- **Stack buffer `string(buf[:n])` always allocates** — the `string()` call copies from stack to heap; the only way to avoid it is to not return a string (write directly to output)
- **Conditional predeclared types based on flags is buggy** — user-defined functions can return predeclared types without calling the corresponding builtins, so flag-based gating misses cases
- **Benchmark variance correlates with system temperature** — after many benchmark runs, CPU throttling causes 2-3x inflation in individual script times; suite metric is more resilient

## Remaining Opportunities (high effort / marginal gain)

- **binExpr/compileIdent/compileCall allocation avoidance**: would need major refactor to write expressions directly to output Builder instead of returning strings
- **Generator pooling**: reuse Generator across compilations to eliminate New() allocation and imports slice alloc
- **Batch writeLine calls**: combine multiple writeLine into single WriteString for common sequences (e.g., function prologue)
- **Conditional predeclared types (v2)**: track usage during type info construction in checker, not during emission
- **Inline goType for common composite types**: arrays/optionals of records/sums do string concat; could use a small cache
- **Write directly to Builder in emitTypeDecl/emitSumTypeDecl**: avoid string concat for type declarations; rare so impact is small

## Current State — Complex Scripts

- Baseline: 53,821 ns/op, 806 allocs/op, 138,755 bytes/op
- Best: ~40,362 ns/op, 657 allocs/op, 126,970 bytes/op
- Improvement: ~25.0% time, ~18.5% allocs, ~8.5% bytes
- Best individual script improvements: agent_demo -41.5%, mega -16.3%, numpy -18.9%, pandas -18.3%
- Confidence: 5.6× noise floor
- Limiting factor: macOS scheduler noise makes sub-1% changes hard to distinguish
