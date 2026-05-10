# Tartalo Autoresearch

## Objective
Optimize Tartalo compiler performance across both compilation speed and generated code runtime performance.

## Primary Metric
`composite_score_ms` — weighted combination of:
- `native_runtime_total_ms` × 2.0 (highest priority)
- `sh_runtime_total_ms` × 1.0
- `codegen_compile_total_ms` × 0.5
- `nativegen_compile_total_ms` × 0.5

Lower is better.

## Secondary Metrics
- Per-workload metrics: codegen_compile_*_ms, nativegen_compile_*_ms, sh_runtime_*_ms, native_runtime_*_ms
- Total metrics: codegen_compile_total_ms, nativegen_compile_total_ms, sh_runtime_total_ms, native_runtime_total_ms
- Binary size: native_binary_total_kb

## Benchmark
`go run scripts/bench_runner.go`

## Workloads
- perf: fib(10), arrays, strings, records (from scripts/bench_perf.tt)
- fizzbuzz: loops and conditionals
- array: array operations
- strings: string manipulation
- record: record creation and access
- match: pattern matching
- heavy: fib(20), sumTo(1000), buildString(100), work(1000)

## Scope
- `internal/codegen/` — sh code generation
- `internal/nativegen/` — native Go code generation
- Focus on generated code performance, especially native target

## Rules
- Do NOT overfit to benchmarks
- Do NOT cheat on benchmarks
- Keep correctness: all workloads must compile and produce expected output
- Monitor compilation time to avoid catastrophic regressions
