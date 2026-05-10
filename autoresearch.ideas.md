# Autoresearch Ideas

## Baseline Analysis
- `nativegen_compile_total_ms`: ~900ms (dominates composite score)
- `sh_runtime_total_ms`: ~350ms (heavy workload is 282ms)
- `native_runtime_total_ms`: ~27ms (already very fast)
- `codegen_compile_total_ms`: ~30ms (already very fast)

## High-Impact Targets

### 1. Reduce nativegen compilation time
Nativegen compile includes Tartalo->Go generation + `go build`. The `go build` step likely dominates.
- Check if `go build` uses `-ldflags="-s -w"` to skip symbol table and DWARF generation
- Check if we can skip `go vet` or other build steps
- Reduce generated Go code size to speed up Go compilation
- Consider using `go build -trimpath` if not already used

### 2. Optimize sh runtime for loops and string ops
The heavy workload sh runtime is 282ms. Fib(20) in sh is slow due to recursive function calls.
- Look at function call overhead in codegen - can we reduce subshell nesting?
- String concatenation in loops generates many temp variables
- Range loops (`for i in 0..n`) may be generating inefficient sequences

### 3. Optimize codegen output for common patterns
- Inline small functions (especially single-expression functions)
- Reduce temporary variable generation for intermediate expressions
- Optimize array iteration to avoid copying

## Deferred / Complex
- Native runtime is already very fast (~3ms per workload), so marginal gains there are hard
- Could add more runtime benchmarks to native target, but native is already dominated by compile time
