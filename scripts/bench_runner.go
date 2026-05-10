// Benchmark runner for Tartalo autoresearch.
// Measures compilation time (codegen + nativegen) and runtime performance
// (generated sh scripts + native binaries) across multiple workloads.
//
// Usage: go run scripts/bench_runner.go
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const iterations = 5

// benchWorkload defines a script and its expected output (for verification).
type benchWorkload struct {
	name      string
	path      string
	isInline  bool
	src       string
	expectOut string            // substring expected in stdout
	shSkip    bool              // skip sh runtime for this workload
	env       map[string]string // extra env vars for run
}

var workloads = []benchWorkload{
	{
		name:      "perf",
		path:      "bench_perf.tt",
		expectOut: "ok",
	},
	{
		name:      "fizzbuzz",
		path:      "examples/fizzbuzz.tt",
		expectOut: "Buzz",
	},
	{
		name:      "array",
		path:      "examples/array.tt",
		expectOut: "sum of first 5 primes",
	},
	{
		name:      "strings",
		path:      "examples/strings.tt",
		expectOut: "world",
	},
	{
		name:      "record",
		path:      "examples/record.tt",
		expectOut: "coffee",
	},
	{
		name:      "match",
		path:      "examples/match.tt",
		expectOut: "compiling",
		env:       map[string]string{"ACTION": "build"},
	},
	{
		name:     "heavy",
		path:     "scripts/bench_heavy.tt",
		isInline: true,
		src: `func fib(n: number): number {
  if n <= 1 { return n }
  return fib(n - 1) + fib(n - 2)
}

func sumTo(n: number): number {
  let total = 0
  for i in 0..n {
    total = total + i
  }
  return total
}

func buildString(n: number): string {
  let s = ""
  for i in 0..n {
    s = s + "x"
  }
  return s
}

func work(n: number): number {
  let result = 0
  for i in 0..n {
    if i % 2 == 0 {
      result = result + i
    } else {
      result = result - i
    }
  }
  return result
}

func main(): void {
  let f = fib(20)
  let s = sumTo(1000)
  let s2 = buildString(100)
  let w = work(1000)
  echo("fib=" + str(f) + " sum=" + str(s) + " len=" + str(len(s2)) + " work=" + str(w))
}
`,
		expectOut: "fib=6765",
	},
}

func main() {
	// Ensure compiler is built
	if err := buildCompiler(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to build compiler: %v\n", err)
		os.Exit(1)
	}

	// Warm-up: build a native binary once to populate the Go build cache
	// so subsequent benchmark iterations measure warm-cache performance.
	warmupPath := "/tmp/tartalo_bench_warmup"
	cmd := exec.Command("./tartalo", "build", "bench_perf.tt", "-o", warmupPath, "--target=native", "--no-verify")
	_, _ = cmd.CombinedOutput()
	os.Remove(warmupPath)

	// Ensure heavy benchmark file exists
	for i := range workloads {
		if workloads[i].isInline {
			dir := filepath.Dir(workloads[i].path)
			os.MkdirAll(dir, 0755)
			if err := os.WriteFile(workloads[i].path, []byte(workloads[i].src), 0644); err != nil {
				fmt.Fprintf(os.Stderr, "failed to write %s: %v\n", workloads[i].path, err)
				os.Exit(1)
			}
		}
	}

	// Verify all workloads compile and run correctly first
	for _, w := range workloads {
		if err := verifyWorkload(w); err != nil {
			fmt.Fprintf(os.Stderr, "verification failed for %s: %v\n", w.name, err)
			os.Exit(1)
		}
	}

	// Run benchmarks
	var allResults []result
	for _, w := range workloads {
		r, err := benchmarkWorkload(w)
		if err != nil {
			fmt.Fprintf(os.Stderr, "benchmark failed for %s: %v\n", w.name, err)
			os.Exit(1)
		}
		allResults = append(allResults, r)
	}

	// Aggregate results
	var (
		totalCodegenCompile   time.Duration
		totalNativegenCompile time.Duration
		totalShRuntime        time.Duration
		totalNativeRuntime    time.Duration
		totalNativeBinarySize int64
	)
	for _, r := range allResults {
		totalCodegenCompile += r.codegenCompile
		totalNativegenCompile += r.nativegenCompile
		totalShRuntime += r.shRuntime
		totalNativeRuntime += r.nativeRuntime
		totalNativeBinarySize += r.nativeBinarySize
	}

	// Print individual metrics
	for _, r := range allResults {
		fmt.Printf("METRIC codegen_compile_%s_ms=%.2f\n", r.name, float64(r.codegenCompile.Microseconds())/1000.0)
		fmt.Printf("METRIC nativegen_compile_%s_ms=%.2f\n", r.name, float64(r.nativegenCompile.Microseconds())/1000.0)
		fmt.Printf("METRIC sh_runtime_%s_ms=%.2f\n", r.name, float64(r.shRuntime.Microseconds())/1000.0)
		fmt.Printf("METRIC native_runtime_%s_ms=%.2f\n", r.name, float64(r.nativeRuntime.Microseconds())/1000.0)
		fmt.Printf("METRIC native_binary_%s_kb=%.2f\n", r.name, float64(r.nativeBinarySize)/1024.0)
	}

	// Print aggregate metrics
	fmt.Printf("METRIC codegen_compile_total_ms=%.2f\n", float64(totalCodegenCompile.Microseconds())/1000.0)
	fmt.Printf("METRIC nativegen_compile_total_ms=%.2f\n", float64(totalNativegenCompile.Microseconds())/1000.0)
	fmt.Printf("METRIC sh_runtime_total_ms=%.2f\n", float64(totalShRuntime.Microseconds())/1000.0)
	fmt.Printf("METRIC native_runtime_total_ms=%.2f\n", float64(totalNativeRuntime.Microseconds())/1000.0)
	fmt.Printf("METRIC native_binary_total_kb=%.2f\n", float64(totalNativeBinarySize)/1024.0)

	// Composite score: heavily weight native runtime (the user said ESPECIALLY runtime)
	// Also include sh runtime and compile times with lower weight
	composite := float64(totalNativeRuntime.Microseconds())*2.0 +
		float64(totalShRuntime.Microseconds()) +
		float64(totalCodegenCompile.Microseconds())*0.5 +
		float64(totalNativegenCompile.Microseconds())*0.5
	fmt.Printf("METRIC composite_score_ms=%.2f\n", composite/1000.0)
}

type result struct {
	name             string
	codegenCompile   time.Duration
	nativegenCompile time.Duration
	shRuntime        time.Duration
	nativeRuntime    time.Duration
	nativeBinarySize int64
}

func buildCompiler() error {
	cmd := exec.Command("go", "build", "-o", "tartalo", "./cmd/tartalo")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", out)
	}
	return nil
}

func verifyWorkload(w benchWorkload) error {
	// Verify sh compilation and run
	shPath := fmt.Sprintf("/tmp/tartalo_verify_%s.sh", w.name)
	cmd := exec.Command("./tartalo", "build", w.path, "-o", shPath, "--no-verify")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sh build: %v: %s", err, out)
	}
	if !w.shSkip {
		run := exec.Command("/bin/sh", shPath)
		for k, v := range w.env {
			run.Env = append(os.Environ(), k+"="+v)
		}
		out, err = run.CombinedOutput()
		if err != nil {
			return fmt.Errorf("sh run: %v: %s", err, out)
		}
		if !strings.Contains(string(out), w.expectOut) {
			return fmt.Errorf("sh run: expected %q in output, got %q", w.expectOut, string(out))
		}
	}
	os.Remove(shPath)

	// Verify native compilation and run
	binPath := fmt.Sprintf("/tmp/tartalo_verify_%s", w.name)
	cmd = exec.Command("./tartalo", "build", w.path, "-o", binPath, "--target=native", "--no-verify")
	out, err = cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("native build: %v: %s", err, out)
	}
	run := exec.Command(binPath)
	for k, v := range w.env {
		run.Env = append(os.Environ(), k+"="+v)
	}
	out, err = run.CombinedOutput()
	if err != nil {
		return fmt.Errorf("native run: %v: %s", err, out)
	}
	if !strings.Contains(string(out), w.expectOut) {
		return fmt.Errorf("native run: expected %q in output, got %q", w.expectOut, string(out))
	}
	os.Remove(binPath)

	return nil
}

func benchmarkWorkload(w benchWorkload) (result, error) {
	r := result{name: w.name}

	// Benchmark codegen compilation (sh target)
	var codegenTimes []time.Duration
	for i := 0; i < iterations; i++ {
		shPath := fmt.Sprintf("/tmp/tartalo_bench_%s_%d.sh", w.name, i)
		t0 := time.Now()
		cmd := exec.Command("./tartalo", "build", w.path, "-o", shPath, "--no-verify")
		_, err := cmd.CombinedOutput()
		if err != nil {
			return r, fmt.Errorf("codegen iter %d: %w", i, err)
		}
		codegenTimes = append(codegenTimes, time.Since(t0))
		os.Remove(shPath)
	}
	r.codegenCompile = medianDur(codegenTimes)

	// Benchmark nativegen compilation (native target)
	var nativegenTimes []time.Duration
	for i := 0; i < iterations; i++ {
		binPath := fmt.Sprintf("/tmp/tartalo_bench_%s_%d", w.name, i)
		t0 := time.Now()
		cmd := exec.Command("./tartalo", "build", w.path, "-o", binPath, "--target=native", "--no-verify")
		_, err := cmd.CombinedOutput()
		if err != nil {
			return r, fmt.Errorf("nativegen iter %d: %w", i, err)
		}
		nativegenTimes = append(nativegenTimes, time.Since(t0))

		// Measure binary size on first iteration
		if i == 0 {
			info, err := os.Stat(binPath)
			if err == nil {
				r.nativeBinarySize = info.Size()
			}
		}
		os.Remove(binPath)
	}
	r.nativegenCompile = medianDur(nativegenTimes)

	// Benchmark sh runtime
	if !w.shSkip {
		shPath := fmt.Sprintf("/tmp/tartalo_bench_run_%s.sh", w.name)
		cmd := exec.Command("./tartalo", "build", w.path, "-o", shPath, "--no-verify")
		_, err := cmd.CombinedOutput()
		if err != nil {
			return r, fmt.Errorf("sh build for runtime: %w", err)
		}
		var shTimes []time.Duration
		for i := 0; i < iterations; i++ {
			t0 := time.Now()
			run := exec.Command("/bin/sh", shPath)
			for k, v := range w.env {
				run.Env = append(os.Environ(), k+"="+v)
			}
			run.Stdout = nil
			run.Stderr = nil
			_, _ = run.CombinedOutput()
			shTimes = append(shTimes, time.Since(t0))
		}
		r.shRuntime = medianDur(shTimes)
		os.Remove(shPath)
	}

	// Benchmark native runtime
	binPath := fmt.Sprintf("/tmp/tartalo_bench_run_%s", w.name)
	cmd := exec.Command("./tartalo", "build", w.path, "-o", binPath, "--target=native", "--no-verify")
	_, err := cmd.CombinedOutput()
	if err != nil {
		return r, fmt.Errorf("native build for runtime: %w", err)
	}
	var nativeTimes []time.Duration
	for i := 0; i < iterations; i++ {
		t0 := time.Now()
		run := exec.Command(binPath)
		for k, v := range w.env {
			run.Env = append(os.Environ(), k+"="+v)
		}
		run.Stdout = nil
		run.Stderr = nil
		_, _ = run.CombinedOutput()
		nativeTimes = append(nativeTimes, time.Since(t0))
	}
	r.nativeRuntime = medianDur(nativeTimes)
	os.Remove(binPath)

	return r, nil
}

func medianDur(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	cp := make([]time.Duration, len(ds))
	copy(cp, ds)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	return cp[len(cp)/2]
}
