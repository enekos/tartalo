#!/bin/bash
set -e

# Benchmark harness for autoresearch on Tartalo codegen performance

N="${1:-20}"

# 1. Go micro-benchmarks for compiler phases
echo "=== Go Benchmarks ==="
# Run benchmarks in isolation with -run=NONE to skip tests and get stable measurements.
BENCH_OUT=$(go test -bench=BenchmarkCodegen -benchmem -run=NONE ./internal/codegen/ 2>&1)
echo "$BENCH_OUT"

CODEGEN_NS=$(echo "$BENCH_OUT" | grep 'BenchmarkCodegen-' | awk '{print $3}')
CODEGEN_ALLOCS=$(echo "$BENCH_OUT" | grep 'BenchmarkCodegen-' | awk '{print $7}')

BENCH_OUT2=$(go test -bench=BenchmarkCompilePipeline -benchmem -run=NONE ./internal/codegen/ 2>&1)
echo "$BENCH_OUT2"

COMPILE_NS=$(echo "$BENCH_OUT2" | grep BenchmarkCompilePipeline | awk '{print $3}')
COMPILE_ALLOCS=$(echo "$BENCH_OUT2" | grep BenchmarkCompilePipeline | awk '{print $7}')

# Nativegen benchmark
BENCH_OUT3=$(go test -bench=BenchmarkNativegen -benchmem -run=NONE ./internal/nativegen/ 2>&1)
echo "$BENCH_OUT3"

NATIVEGEN_NS=$(echo "$BENCH_OUT3" | grep 'BenchmarkNativegen-' | awk '{print $3}')
NATIVEGEN_ALLOCS=$(echo "$BENCH_OUT3" | grep 'BenchmarkNativegen-' | awk '{print $7}')

# 2. End-to-end bench with the compiler CLI
echo ""
echo "=== CLI Bench (fizzbuzz) ==="
CLI_OUT=$(./tartalo bench examples/fizzbuzz.tt -n "$N" --no-verify 2>&1)
echo "$CLI_OUT"

# Extract median codegen time from CLI output (e.g., "20µs")
CODEGEN_MED=$(echo "$CLI_OUT" | grep '^codegen' | awk '{print $3}')
RUN_MED=$(echo "$CLI_OUT" | grep '^run' | awk '{print $3}')
FRONTEND_MED=$(echo "$CLI_OUT" | grep '^frontend' | awk '{print $3}')

# 3. Generated code size
echo ""
echo "=== Generated Code Metrics ==="
./tartalo build examples/fizzbuzz.tt -o /tmp/bench_fizzbuzz.sh --no-verify
SH_LINES=$(wc -l < /tmp/bench_fizzbuzz.sh | tr -d ' ')
SH_BYTES=$(wc -c < /tmp/bench_fizzbuzz.sh | tr -d ' ')
echo "fizzbuzz_lines: $SH_LINES"
echo "fizzbuzz_bytes: $SH_BYTES"

./tartalo build scripts/bench_perf.tt -o /tmp/bench_perf.sh --no-verify
PERF_LINES=$(wc -l < /tmp/bench_perf.sh | tr -d ' ')
PERF_BYTES=$(wc -c < /tmp/bench_perf.sh | tr -d ' ')
echo "perf_lines: $PERF_LINES"
echo "perf_bytes: $PERF_BYTES"

# 4. Generated script runtime performance (run multiple times for accuracy)
echo ""
echo "=== Generated Script Runtime ==="
RUN_START=$(python3 -c 'import time; print(time.time())')
for i in $(seq 1 20); do
  /bin/sh /tmp/bench_perf.sh > /dev/null 2>&1
done
RUN_END=$(python3 -c 'import time; print(time.time())')
RUN_TIME=$(python3 -c "print(int(($RUN_END - $RUN_START) * 1000))")
echo "script_runtime_ms: $RUN_TIME"

# Output metrics in METRIC format for autoresearch parsing
echo ""
echo "METRIC compile_ns=$COMPILE_NS"
echo "METRIC codegen_ns=$CODEGEN_NS"
echo "METRIC run_med_ms=$RUN_TIME"
echo "METRIC fizzbuzz_bytes=$SH_BYTES"
echo "METRIC perf_bytes=$PERF_BYTES"
echo "METRIC perf_lines=$PERF_LINES"
echo "METRIC compile_allocs=$COMPILE_ALLOCS"
echo "METRIC codegen_allocs=$CODEGEN_ALLOCS"
echo "METRIC nativegen_ns=$NATIVEGEN_NS"
echo "METRIC nativegen_allocs=$NATIVEGEN_ALLOCS"
