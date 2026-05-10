#!/bin/bash
set -e

# Comprehensive nativegen benchmark for autoresearch.
# Measures compilation performance across multiple complex scripts.

echo "=== Nativegen Complex Benchmarks ==="
BENCH_OUT=$(go test -bench='BenchmarkNativegenSuite$' -benchmem -run=NONE ./internal/nativegen/ 2>&1)
echo "$BENCH_OUT"

SUITE_NS=$(echo "$BENCH_OUT" | grep 'BenchmarkNativegenSuite-' | awk '{print $3}')
SUITE_BYTES=$(echo "$BENCH_OUT" | grep 'BenchmarkNativegenSuite-' | awk '{print $5}')
SUITE_ALLOCS=$(echo "$BENCH_OUT" | grep 'BenchmarkNativegenSuite-' | awk '{print $7}')

echo ""
echo "=== Per-script benchmarks ==="
BENCH2=$(go test -bench='BenchmarkNativegenOutputSize/' -benchmem -run=NONE ./internal/nativegen/ 2>&1)
echo "$BENCH2"

# Extract output sizes
MEGA_BYTES=$(echo "$BENCH2" | grep 'BenchmarkNativegenOutputSize/mega' | awk '{print $5}')
AGENT_BYTES=$(echo "$BENCH2" | grep 'BenchmarkNativegenOutputSize/agent_demo' | awk '{print $5}')
PANDAS_BYTES=$(echo "$BENCH2" | grep 'BenchmarkNativegenOutputSize/pandas' | awk '{print $5}')
NUMPY_BYTES=$(echo "$BENCH2" | grep 'BenchmarkNativegenOutputSize/numpy' | awk '{print $5}')

echo ""
echo "=== Individual time benchmarks ==="
BENCH3=$(go test -bench='BenchmarkNativegen(Hello|FizzBuzz|Array|Strings|Record|Match|Numpy|Pandas|AgentDemo|Mega)$' -benchmem -run=NONE ./internal/nativegen/ 2>&1)
echo "$BENCH3"

MEGA_NS=$(echo "$BENCH3" | grep 'BenchmarkNativegenMega-' | awk '{print $3}')
AGENT_NS=$(echo "$BENCH3" | grep 'BenchmarkNativegenAgentDemo-' | awk '{print $3}')
PANDAS_NS=$(echo "$BENCH3" | grep 'BenchmarkNativegenPandas-' | awk '{print $3}')
NUMPY_NS=$(echo "$BENCH3" | grep 'BenchmarkNativegenNumpy-' | awk '{print $3}')
SIMPLE_NS=$(echo "$BENCH3" | grep 'BenchmarkNativegenSimple-' | awk '{print $3}')

echo ""
echo "METRIC suite_ns=$SUITE_NS"
echo "METRIC suite_bytes=$SUITE_BYTES"
echo "METRIC suite_allocs=$SUITE_ALLOCS"
echo "METRIC mega_ns=$MEGA_NS"
echo "METRIC mega_output_bytes=$MEGA_BYTES"
echo "METRIC agent_ns=$AGENT_NS"
echo "METRIC agent_output_bytes=$AGENT_BYTES"
echo "METRIC pandas_ns=$PANDAS_NS"
echo "METRIC pandas_output_bytes=$PANDAS_BYTES"
echo "METRIC numpy_ns=$NUMPY_NS"
echo "METRIC numpy_output_bytes=$NUMPY_BYTES"
echo "METRIC simple_ns=$SIMPLE_NS"
