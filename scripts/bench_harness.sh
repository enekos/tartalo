#!/bin/bash
set -e

FILE="${1:-scripts/bench_perf.tt}"
N="${2:-20}"

echo "=== Benchmarking $FILE ==="

# Run tartalo bench
echo "--- tartalo bench ---"
./tartalo bench "$FILE" -n "$N" --no-verify

# Measure output size
echo ""
echo "--- output metrics ---"
./tartalo build "$FILE" -o /tmp/bench_out.sh --no-verify
SH_LINES=$(wc -l < /tmp/bench_out.sh)
SH_BYTES=$(wc -c < /tmp/bench_out.sh)
echo "generated_lines: $SH_LINES"
echo "generated_bytes: $SH_BYTES"

# Measure compilation time directly with Go benchmark
echo ""
echo "--- Go micro-benchmark ---"
go test -bench=BenchmarkCodegen -benchtime=1s ./internal/codegen/ 2>/dev/null || echo "No BenchmarkCodegen found"
