#!/bin/bash
set -e

# Cross-target benchmark: compiles bench_cross.tt to both sh and native,
# verifies output parity, and measures compile + runtime performance.

TT="./tartalo"
SRC="bench_cross.tt"
N_RUNS="${1:-20}"

# 1. Compile to sh
echo "=== Compiling to sh ==="
T0=$(python3 -c 'import time; print(time.time())')
"$TT" build "$SRC" -o /tmp/bench_cross_target.sh --no-verify
T1=$(python3 -c 'import time; print(time.time())')
SH_COMPILE_MS=$(python3 -c "print(int(($T1 - $T0) * 1000))")
echo "sh_compile_ms: $SH_COMPILE_MS"

# 2. Compile to native (full pipeline including go build)
echo "=== Compiling to native ==="
T0=$(python3 -c 'import time; print(time.time())')
"$TT" build "$SRC" --target=native -o /tmp/bench_cross_target
T1=$(python3 -c 'import time; print(time.time())')
NATIVE_COMPILE_MS=$(python3 -c "print(int(($T1 - $T0) * 1000))")
echo "native_compile_ms: $NATIVE_COMPILE_MS"

# 3. Run sh output multiple times
echo "=== Running sh ($N_RUNS times) ==="
SH_RUNS=()
for i in $(seq 1 $N_RUNS); do
  T0=$(python3 -c 'import time; print(time.time())')
  /bin/sh /tmp/bench_cross_target.sh > /tmp/sh_out.txt 2>&1
  T1=$(python3 -c 'import time; print(time.time())')
  SH_RUNS+=("$(python3 -c "print(int(($T1 - $T0) * 1000))")")
done
SH_RUN_MED=$(python3 -c "import sys; nums=sorted(map(int, sys.argv[1:])); print(nums[len(nums)//2])" "${SH_RUNS[@]}")
echo "sh_run_median_ms: $SH_RUN_MED"

# 4. Run native output multiple times
echo "=== Running native ($N_RUNS times) ==="
NATIVE_RUNS=()
for i in $(seq 1 $N_RUNS); do
  T0=$(python3 -c 'import time; print(time.time())')
  /tmp/bench_cross_target > /tmp/native_out.txt 2>&1
  T1=$(python3 -c 'import time; print(time.time())')
  NATIVE_RUNS+=("$(python3 -c "print(int(($T1 - $T0) * 1000))")")
done
NATIVE_RUN_MED=$(python3 -c "import sys; nums=sorted(map(int, sys.argv[1:])); print(nums[len(nums)//2])" "${NATIVE_RUNS[@]}")
echo "native_run_median_ms: $NATIVE_RUN_MED"

# 5. Verify output parity
echo "=== Verifying output parity ==="
if diff /tmp/sh_out.txt /tmp/native_out.txt > /dev/null; then
  echo "parity: OK"
  PARITY=1
else
  echo "parity: MISMATCH"
  echo "--- sh output ---"
  cat /tmp/sh_out.txt
  echo "--- native output ---"
  cat /tmp/native_out.txt
  PARITY=0
fi

# 6. Go micro-benchmarks for compiler phases
echo "=== Go micro-benchmarks ==="
BENCH1=$(go test -bench=BenchmarkCodegen -benchmem -run=NONE ./internal/codegen/ 2>&1)
echo "$BENCH1"
CODEGEN_NS=$(echo "$BENCH1" | grep 'BenchmarkCodegen-' | awk '{print $3}')
CODEGEN_ALLOCS=$(echo "$BENCH1" | grep 'BenchmarkCodegen-' | awk '{print $7}')

BENCH2=$(go test -bench=BenchmarkNativegen -benchmem -run=NONE ./internal/nativegen/ 2>&1)
echo "$BENCH2"
NATIVEGEN_NS=$(echo "$BENCH2" | grep 'BenchmarkNativegen-' | awk '{print $3}')
NATIVEGEN_ALLOCS=$(echo "$BENCH2" | grep 'BenchmarkNativegen-' | awk '{print $7}')

# Total metric
echo ""
echo "METRIC sh_compile_ms=$SH_COMPILE_MS"
echo "METRIC native_compile_ms=$NATIVE_COMPILE_MS"
echo "METRIC sh_run_median_ms=$SH_RUN_MED"
echo "METRIC native_run_median_ms=$NATIVE_RUN_MED"
echo "METRIC codegen_ns=$CODEGEN_NS"
echo "METRIC codegen_allocs=$CODEGEN_ALLOCS"
echo "METRIC nativegen_ns=$NATIVEGEN_NS"
echo "METRIC nativegen_allocs=$NATIVEGEN_ALLOCS"
echo "METRIC parity=$PARITY"

TOTAL=$((SH_COMPILE_MS + NATIVE_COMPILE_MS + SH_RUN_MED + NATIVE_RUN_MED))
echo "METRIC total_ms=$TOTAL"
