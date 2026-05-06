# Deferred Optimization Ideas for Tartalo Nativegen

## Completed (kept)

- **Pre-grow g.out to 2048 bytes** — avoids 9 incremental Builder growth steps
- **Pre-grow file Builder to 2048 bytes** — avoids another 9 growth steps for final assembly
- **Replace fmt.Sprintf with string concat in mangledName** — avoids allocation for every top-level identifier reference
- **Expand int64Lit lookup table to 0-199** — avoids strconv.FormatInt for common literals like 100
- **Pre-grow compileArrayLit builder** — avoids incremental growth and string concat allocations
- **Replace map-based imports with slice-based imports** — avoids map allocation and iteration overhead
- **Pre-allocate imports slice with capacity 8** — avoids append reallocations

## Completed (discarded)

- **Remove defer in emitFunc** — closure alloc not a bottleneck (Go compiler optimizes it away)
- **Simplify binExpr to string concat** — stack buffer is faster despite allocation
- **Cache compileIdent results** — cache lookup overhead dominates savings
- **Cache compileExpr results** — same issue
- **Make writeLine standalone function** — parameter passing adds overhead
- **Replace writeLine("") with WriteByte('\n')** — no measurable improvement
- **Reduce imports capacity from 8 to 4** — no consistent improvement
- **Various codegen optimizations** — wrong target (session switched to nativegen)

## Benchmark Cheating (DO NOT RE-ADD)

- **Hardcoded compileIdent fast path for "n", "i", "x", "total", "maxVal", "f10", "xs", "s"** — This is pure benchmark overfitting. It only works for the specific variable names in bench_test.go and provides no benefit to real programs. The apparent ~280ns improvement is entirely from skipping work for these 8 identifiers. DO NOT re-add this optimization.

## Key Learnings

- **runtime.kevent dominates CPU profile** on macOS (70% of time), making small optimizations hard to measure
- **Builder pre-growth is the most effective optimization** for reducing allocations
- **Lookup tables for common values** (int64Lit) provide consistent wins
- **String concatenation is often faster than fmt.Sprintf** for simple patterns
- **Slice-based deduplication beats map-based** for small collections
- **Cache overhead often exceeds savings** for small, fast functions
- **Benchmark variance is high** (~50-100ns) due to OS/scheduler noise
- **Defer closures in Go are often optimized** by the compiler and don't allocate

## Remaining Opportunities (high effort / marginal gain)

- **binExpr allocation avoidance**: string(buf[:n]) always allocates; would need to change compileBinary to write directly to output
- **compileIdent allocation avoidance**: same issue; would need to write identifiers directly to output
- **compileCall allocation avoidance**: same pattern for 1-arg calls
- **Generator pooling**: reuse Generator across compilations to eliminate New() allocation
- **Batch writeLine calls**: combine multiple writeLine into single WriteString for common sequences
- **Conditional predeclared types**: only emit Response/Process/FileInfo/PathParts when used

## Current State

- Baseline: 1828 ns/op, 42 allocs/op, 4664 bytes/op
- Current: ~1680 ns/op, 31 allocs/op, ~4840 bytes/op
- Improvement: ~8% time, ~26% allocs
- Limiting factor: runtime.kevent overhead on macOS makes further micro-optimizations hard to measure
- Note: A benchmark-cheating compileIdent fast path (hardcoded variable names) was accidentally kept in run 88 and later re-added by automated systems. It has been removed. Legitimate current best is ~1680ns.
