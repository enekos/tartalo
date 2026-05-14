// Tartalo benchmark suite.
//
// Subsumes the previous bench_autoresearch.sh / bench_cross_target.sh /
// bench_nativegen.sh / bench_harness.sh trio. Every measurement is taken
// in-process so timings don't pay python3 / awk parsing overhead, and
// every mode emits the same METRIC key=value lines so autoresearch
// pipelines can consume any subset of the suite uniformly.
//
//	go run scripts/bench_runner.go               # suite (end-to-end workloads)
//	go run scripts/bench_runner.go suite -n 10
//	go run scripts/bench_runner.go micro         # Go-level benchmarks
//	go run scripts/bench_runner.go cross         # sh/native parity + speed
//	go run scripts/bench_runner.go all           # suite + micro + cross
//
// Flags:
//
//	-n int          iterations per measurement (default 5)
//	-warmup int     warmup iterations before timing (default 1)
//	-filter string  only run workloads whose name contains this substring
//	-format string  output: human (default), metric, json
//	-no-verify      skip the parity verification pass
//	-keep           keep generated artifacts in /tmp for inspection
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ───────────────────────────── configuration ─────────────────────────────

type workload struct {
	name      string
	path      string
	inline    string            // when non-empty, written to path on first run
	expectOut string            // substring required in stdout
	env       map[string]string // extra env for the run phase
	skipSh    bool
}

var workloads = []workload{
	{name: "perf", path: "scripts/bench_perf.tt", expectOut: "ok"},
	{name: "fizzbuzz", path: "examples/fizzbuzz.tt", expectOut: "Buzz"},
	{name: "array", path: "examples/array.tt", expectOut: "sum of first 5 primes"},
	{name: "strings", path: "examples/strings.tt", expectOut: "world"},
	{name: "record", path: "examples/record.tt", expectOut: "coffee"},
	{name: "match", path: "examples/match.tt", expectOut: "compiling", env: map[string]string{"ACTION": "build"}},
	{
		name:      "heavy",
		path:      "scripts/bench_heavy.tt",
		expectOut: "fib=6765",
		inline: `func fib(n: number): number {
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
    if i % 2 == 0 { result = result + i } else { result = result - i }
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
	},
}

// Go micro-benchmarks invoked by the "micro" mode. Each entry is a
// (pattern, pkg) pair passed to `go test -bench`.
var microBenches = []struct {
	name, pattern, pkg string
}{
	{"codegen", "^BenchmarkCodegen$", "./internal/codegen/"},
	{"compile_pipeline", "^BenchmarkCompilePipeline$", "./internal/codegen/"},
	{"lexer", "^BenchmarkLexer$", "./internal/codegen/"},
	{"parser", "^BenchmarkParser$", "./internal/codegen/"},
	{"checker", "^BenchmarkChecker$", "./internal/codegen/"},
	{"nativegen", "^BenchmarkNativegen$", "./internal/nativegen/"},
	{"nativegen_suite", "^BenchmarkNativegenSuite$", "./internal/nativegen/"},
}

// ──────────────────────────────── cli ─────────────────────────────────

type opts struct {
	iters    int
	warmup   int
	filter   string
	format   string
	noVerify bool
	keep     bool
}

func main() {
	o := opts{iters: 5, warmup: 1, format: "human"}
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	fs.IntVar(&o.iters, "n", o.iters, "iterations per measurement")
	fs.IntVar(&o.warmup, "warmup", o.warmup, "warmup iterations before timing")
	fs.StringVar(&o.filter, "filter", "", "only run workloads whose name contains this substring")
	fs.StringVar(&o.format, "format", o.format, "output format: human | metric | json")
	fs.BoolVar(&o.noVerify, "no-verify", false, "skip parity verification pass")
	fs.BoolVar(&o.keep, "keep", false, "keep generated artifacts in /tmp")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: go run scripts/bench_runner.go [flags] [suite|micro|cross|all]\n")
		fs.PrintDefaults()
	}
	mode := "suite"
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		mode = os.Args[1]
		_ = fs.Parse(os.Args[2:])
	} else {
		_ = fs.Parse(os.Args[1:])
	}
	if o.iters < 1 {
		die("-n must be ≥ 1")
	}
	if o.format != "human" && o.format != "metric" && o.format != "json" {
		die("unknown -format %q (want human|metric|json)", o.format)
	}

	if err := buildCompiler(); err != nil {
		die("failed to build compiler: %v", err)
	}
	materializeInlineSources()

	switch mode {
	case "suite":
		runSuite(o)
	case "micro":
		runMicro(o)
	case "cross":
		runCross(o)
	case "all":
		runSuite(o)
		runMicro(o)
		runCross(o)
	default:
		die("unknown mode %q (want suite|micro|cross|all)", mode)
	}
}

// ───────────────────────────── suite mode ─────────────────────────────

type suiteResult struct {
	Name             string `json:"name"`
	CodegenCompile   int64  `json:"codegen_compile_us"`
	NativegenCompile int64  `json:"nativegen_compile_us"`
	ShRuntime        int64  `json:"sh_runtime_us"`
	NativeRuntime    int64  `json:"native_runtime_us"`
	NativeBinaryB    int64  `json:"native_binary_bytes"`
	GeneratedShB     int64  `json:"generated_sh_bytes"`
}

func runSuite(o opts) {
	selected := filterWorkloads(o.filter)
	if len(selected) == 0 {
		die("no workloads match filter %q", o.filter)
	}

	// One-shot warmup so the Go build cache + linker are warm before timing.
	if o.warmup > 0 {
		warmup := selected[0]
		path := tmpPath("warmup_native")
		_ = runCmd("./tartalo", "build", warmup.path, "-o", path, "--target=native", "--no-verify")
		_ = os.Remove(path)
	}

	if !o.noVerify {
		for _, w := range selected {
			if err := verifyWorkload(w); err != nil {
				die("verify %s: %v", w.name, err)
			}
		}
	}

	results := make([]suiteResult, 0, len(selected))
	for _, w := range selected {
		r, err := benchOneWorkload(w, o)
		if err != nil {
			die("bench %s: %v", w.name, err)
		}
		results = append(results, r)
	}

	emitSuite(o, results)
}

func benchOneWorkload(w workload, o opts) (suiteResult, error) {
	r := suiteResult{Name: w.name}

	// codegen compile (sh target)
	codegenDur, err := timeN(o.iters, func() error {
		out := tmpPath("codegen_" + w.name)
		defer rmIfNotKept(out, o.keep)
		return runCmd("./tartalo", "build", w.path, "-o", out, "--no-verify")
	})
	if err != nil {
		return r, err
	}
	r.CodegenCompile = codegenDur.Microseconds()

	// One-time generated-sh size measurement.
	{
		out := tmpPath("size_" + w.name + ".sh")
		if err := runCmd("./tartalo", "build", w.path, "-o", out, "--no-verify"); err == nil {
			if st, err := os.Stat(out); err == nil {
				r.GeneratedShB = st.Size()
			}
		}
		rmIfNotKept(out, o.keep)
	}

	// nativegen compile (native target) — also captures binary size.
	nativegenDur, err := timeN(o.iters, func() error {
		out := tmpPath("nativegen_" + w.name)
		defer rmIfNotKept(out, o.keep)
		if err := runCmd("./tartalo", "build", w.path, "-o", out, "--target=native", "--no-verify"); err != nil {
			return err
		}
		if r.NativeBinaryB == 0 {
			if st, err := os.Stat(out); err == nil {
				r.NativeBinaryB = st.Size()
			}
		}
		return nil
	})
	if err != nil {
		return r, err
	}
	r.NativegenCompile = nativegenDur.Microseconds()

	// sh runtime (build once, run N times). A short warmup pass avoids
	// counting one-time costs (e.g. macOS code-signing verification on
	// freshly-built binaries, or initial cache loads) in the median.
	if !w.skipSh {
		sh := tmpPath("run_" + w.name + ".sh")
		defer rmIfNotKept(sh, o.keep)
		if err := runCmd("./tartalo", "build", w.path, "-o", sh, "--no-verify"); err != nil {
			return r, fmt.Errorf("sh build for runtime: %w", err)
		}
		for i := 0; i < o.warmup; i++ {
			_ = runWith(w.env, "/bin/sh", sh)
		}
		runDur, err := timeN(o.iters, func() error {
			return runWith(w.env, "/bin/sh", sh)
		})
		if err != nil {
			return r, err
		}
		r.ShRuntime = runDur.Microseconds()
	}

	// native runtime (build once, warmup, then run N times).
	bin := tmpPath("run_" + w.name)
	defer rmIfNotKept(bin, o.keep)
	if err := runCmd("./tartalo", "build", w.path, "-o", bin, "--target=native", "--no-verify"); err != nil {
		return r, fmt.Errorf("native build for runtime: %w", err)
	}
	for i := 0; i < o.warmup; i++ {
		_ = runWith(w.env, bin)
	}
	natDur, err := timeN(o.iters, func() error {
		return runWith(w.env, bin)
	})
	if err != nil {
		return r, err
	}
	r.NativeRuntime = natDur.Microseconds()

	return r, nil
}

func emitSuite(o opts, rs []suiteResult) {
	switch o.format {
	case "json":
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"suite": rs, "totals": suiteTotals(rs)})
	case "human":
		fmt.Printf("\nsuite — %d iterations (n=%d)\n", o.iters, o.iters)
		fmt.Printf("%-10s %12s %12s %12s %12s %12s %12s\n",
			"workload", "codegen", "nativegen", "sh-run", "native-run", "binary", "sh-size")
		for _, r := range rs {
			fmt.Printf("%-10s %12s %12s %12s %12s %12s %12s\n",
				r.Name,
				fmtMicros(r.CodegenCompile),
				fmtMicros(r.NativegenCompile),
				fmtMicros(r.ShRuntime),
				fmtMicros(r.NativeRuntime),
				fmtBytes(r.NativeBinaryB),
				fmtBytes(r.GeneratedShB),
			)
		}
		t := suiteTotals(rs)
		fmt.Printf("%-10s %12s %12s %12s %12s %12s %12s\n",
			"TOTAL",
			fmtMicros(t.CodegenCompile),
			fmtMicros(t.NativegenCompile),
			fmtMicros(t.ShRuntime),
			fmtMicros(t.NativeRuntime),
			fmtBytes(t.NativeBinaryB),
			fmtBytes(t.GeneratedShB),
		)
		fmt.Printf("composite_score_ms=%.2f\n", compositeScore(t))
	default: // metric
		for _, r := range rs {
			fmt.Printf("METRIC codegen_compile_%s_ms=%.2f\n", r.Name, msFromUs(r.CodegenCompile))
			fmt.Printf("METRIC nativegen_compile_%s_ms=%.2f\n", r.Name, msFromUs(r.NativegenCompile))
			fmt.Printf("METRIC sh_runtime_%s_ms=%.2f\n", r.Name, msFromUs(r.ShRuntime))
			fmt.Printf("METRIC native_runtime_%s_ms=%.2f\n", r.Name, msFromUs(r.NativeRuntime))
			fmt.Printf("METRIC native_binary_%s_kb=%.2f\n", r.Name, float64(r.NativeBinaryB)/1024.0)
		}
		t := suiteTotals(rs)
		fmt.Printf("METRIC codegen_compile_total_ms=%.2f\n", msFromUs(t.CodegenCompile))
		fmt.Printf("METRIC nativegen_compile_total_ms=%.2f\n", msFromUs(t.NativegenCompile))
		fmt.Printf("METRIC sh_runtime_total_ms=%.2f\n", msFromUs(t.ShRuntime))
		fmt.Printf("METRIC native_runtime_total_ms=%.2f\n", msFromUs(t.NativeRuntime))
		fmt.Printf("METRIC native_binary_total_kb=%.2f\n", float64(t.NativeBinaryB)/1024.0)
		fmt.Printf("METRIC composite_score_ms=%.2f\n", compositeScore(t))
	}
}

func suiteTotals(rs []suiteResult) suiteResult {
	var t suiteResult
	t.Name = "total"
	for _, r := range rs {
		t.CodegenCompile += r.CodegenCompile
		t.NativegenCompile += r.NativegenCompile
		t.ShRuntime += r.ShRuntime
		t.NativeRuntime += r.NativeRuntime
		t.NativeBinaryB += r.NativeBinaryB
		t.GeneratedShB += r.GeneratedShB
	}
	return t
}

func compositeScore(t suiteResult) float64 {
	return (float64(t.NativeRuntime)*2.0 +
		float64(t.ShRuntime) +
		float64(t.CodegenCompile)*0.5 +
		float64(t.NativegenCompile)*0.5) / 1000.0
}

// ───────────────────────────── micro mode ─────────────────────────────

type microResult struct {
	Name        string `json:"name"`
	NsPerOp     int64  `json:"ns_per_op"`
	BytesPerOp  int64  `json:"bytes_per_op"`
	AllocsPerOp int64  `json:"allocs_per_op"`
}

// reBenchLine matches the standard Go testing.B output:
// "BenchmarkFoo-8    1234      5678 ns/op    123 B/op    4 allocs/op"
var reBenchLine = regexp.MustCompile(`^Benchmark\S+\s+\d+\s+(\d+)\s+ns/op(?:\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op)?`)

func runMicro(o opts) {
	out := make([]microResult, 0, len(microBenches))
	for _, b := range microBenches {
		if o.filter != "" && !strings.Contains(b.name, o.filter) {
			continue
		}
		r, err := runOneMicro(b.name, b.pattern, b.pkg)
		if err != nil {
			die("micro %s: %v", b.name, err)
		}
		out = append(out, r)
	}
	emitMicro(o, out)
}

func runOneMicro(name, pattern, pkg string) (microResult, error) {
	r := microResult{Name: name}
	cmd := exec.Command("go", "test", "-bench="+pattern, "-benchmem", "-run=^$", "-benchtime=1s", pkg)
	cmd.Env = os.Environ()
	stdoutB, err := cmd.CombinedOutput()
	if err != nil {
		return r, fmt.Errorf("go test failed: %v\n%s", err, stdoutB)
	}
	for _, line := range strings.Split(string(stdoutB), "\n") {
		m := reBenchLine.FindStringSubmatch(line)
		if len(m) == 0 {
			continue
		}
		r.NsPerOp = atoi64(m[1])
		if len(m) > 2 && m[2] != "" {
			r.BytesPerOp = atoi64(m[2])
			r.AllocsPerOp = atoi64(m[3])
		}
		return r, nil
	}
	return r, fmt.Errorf("no benchmark line parsed for %q", pattern)
}

func emitMicro(o opts, rs []microResult) {
	switch o.format {
	case "json":
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"micro": rs})
	case "human":
		fmt.Printf("\nmicro — Go benchmarks\n")
		fmt.Printf("%-22s %14s %14s %12s\n", "benchmark", "ns/op", "bytes/op", "allocs/op")
		for _, r := range rs {
			fmt.Printf("%-22s %14s %14d %12d\n", r.Name, fmtNs(r.NsPerOp), r.BytesPerOp, r.AllocsPerOp)
		}
	default:
		for _, r := range rs {
			fmt.Printf("METRIC micro_%s_ns=%d\n", r.Name, r.NsPerOp)
			fmt.Printf("METRIC micro_%s_bytes=%d\n", r.Name, r.BytesPerOp)
			fmt.Printf("METRIC micro_%s_allocs=%d\n", r.Name, r.AllocsPerOp)
		}
	}
}

// ───────────────────────────── cross mode ─────────────────────────────
// For every workload, compile to both backends, run both, and check that
// stdout matches. Emits per-workload parity + timing comparison.

type crossResult struct {
	Name            string `json:"name"`
	ShCompileUs     int64  `json:"sh_compile_us"`
	NativeCompileUs int64  `json:"native_compile_us"`
	ShRunUs         int64  `json:"sh_run_us"`
	NativeRunUs     int64  `json:"native_run_us"`
	Parity          bool   `json:"parity"`
}

func runCross(o opts) {
	selected := filterWorkloads(o.filter)
	rs := make([]crossResult, 0, len(selected))
	for _, w := range selected {
		r, err := crossOneWorkload(w, o)
		if err != nil {
			die("cross %s: %v", w.name, err)
		}
		rs = append(rs, r)
	}
	emitCross(o, rs)
}

func crossOneWorkload(w workload, o opts) (crossResult, error) {
	r := crossResult{Name: w.name}

	sh := tmpPath("cross_" + w.name + ".sh")
	bin := tmpPath("cross_" + w.name)
	defer rmIfNotKept(sh, o.keep)
	defer rmIfNotKept(bin, o.keep)

	shCompile, err := timeOnce(func() error {
		return runCmd("./tartalo", "build", w.path, "-o", sh, "--no-verify")
	})
	if err != nil {
		return r, err
	}
	r.ShCompileUs = shCompile.Microseconds()

	natCompile, err := timeOnce(func() error {
		return runCmd("./tartalo", "build", w.path, "-o", bin, "--target=native", "--no-verify")
	})
	if err != nil {
		return r, err
	}
	r.NativeCompileUs = natCompile.Microseconds()

	// Warmup pass on each target (matches the suite mode's strategy).
	for i := 0; i < o.warmup; i++ {
		_, _, _ = captureWith(w.env, "/bin/sh", sh)
		_, _, _ = captureWith(w.env, bin)
	}

	shOut, shDur, err := captureWith(w.env, "/bin/sh", sh)
	if err != nil && !w.skipSh {
		return r, fmt.Errorf("sh run: %w", err)
	}
	r.ShRunUs = shDur.Microseconds()

	natOut, natDur, err := captureWith(w.env, bin)
	if err != nil {
		return r, fmt.Errorf("native run: %w", err)
	}
	r.NativeRunUs = natDur.Microseconds()

	r.Parity = w.skipSh || strings.TrimRight(shOut, "\n") == strings.TrimRight(natOut, "\n")
	if !r.Parity {
		fmt.Fprintf(os.Stderr, "parity mismatch for %s:\n--- sh ---\n%s\n--- native ---\n%s\n",
			w.name, shOut, natOut)
	}
	return r, nil
}

func emitCross(o opts, rs []crossResult) {
	switch o.format {
	case "json":
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"cross": rs})
	case "human":
		fmt.Printf("\ncross — sh ↔ native parity & speed\n")
		fmt.Printf("%-10s %12s %12s %12s %12s %8s\n",
			"workload", "sh-compile", "nat-compile", "sh-run", "nat-run", "parity")
		for _, r := range rs {
			fmt.Printf("%-10s %12s %12s %12s %12s %8s\n",
				r.Name,
				fmtMicros(r.ShCompileUs),
				fmtMicros(r.NativeCompileUs),
				fmtMicros(r.ShRunUs),
				fmtMicros(r.NativeRunUs),
				yesNo(r.Parity),
			)
		}
	default:
		for _, r := range rs {
			fmt.Printf("METRIC cross_%s_sh_compile_ms=%.2f\n", r.Name, msFromUs(r.ShCompileUs))
			fmt.Printf("METRIC cross_%s_native_compile_ms=%.2f\n", r.Name, msFromUs(r.NativeCompileUs))
			fmt.Printf("METRIC cross_%s_sh_run_ms=%.2f\n", r.Name, msFromUs(r.ShRunUs))
			fmt.Printf("METRIC cross_%s_native_run_ms=%.2f\n", r.Name, msFromUs(r.NativeRunUs))
			fmt.Printf("METRIC cross_%s_parity=%d\n", r.Name, boolInt(r.Parity))
		}
	}
}

// ──────────────────────────── infrastructure ────────────────────────────

func buildCompiler() error {
	if _, err := os.Stat("tartalo"); err == nil {
		return nil // assume usable
	}
	return runCmd("go", "build", "-o", "tartalo", "./cmd/tartalo")
}

func materializeInlineSources() {
	for _, w := range workloads {
		if w.inline == "" {
			continue
		}
		if _, err := os.Stat(w.path); err == nil {
			continue
		}
		_ = os.MkdirAll(filepath.Dir(w.path), 0o755)
		if err := os.WriteFile(w.path, []byte(w.inline), 0o644); err != nil {
			die("write %s: %v", w.path, err)
		}
	}
}

func filterWorkloads(sub string) []workload {
	if sub == "" {
		return workloads
	}
	out := make([]workload, 0, len(workloads))
	for _, w := range workloads {
		if strings.Contains(w.name, sub) {
			out = append(out, w)
		}
	}
	return out
}

func verifyWorkload(w workload) error {
	sh := tmpPath("verify_" + w.name + ".sh")
	defer os.Remove(sh)
	if err := runCmd("./tartalo", "build", w.path, "-o", sh, "--no-verify"); err != nil {
		return fmt.Errorf("sh build: %w", err)
	}
	if !w.skipSh {
		out, _, err := captureWith(w.env, "/bin/sh", sh)
		if err != nil {
			return fmt.Errorf("sh run: %w", err)
		}
		if !strings.Contains(out, w.expectOut) {
			return fmt.Errorf("sh run: want %q in stdout, got %q", w.expectOut, out)
		}
	}
	bin := tmpPath("verify_" + w.name)
	defer os.Remove(bin)
	if err := runCmd("./tartalo", "build", w.path, "-o", bin, "--target=native", "--no-verify"); err != nil {
		return fmt.Errorf("native build: %w", err)
	}
	out, _, err := captureWith(w.env, bin)
	if err != nil {
		return fmt.Errorf("native run: %w", err)
	}
	if !strings.Contains(out, w.expectOut) {
		return fmt.Errorf("native run: want %q in stdout, got %q", w.expectOut, out)
	}
	return nil
}

// timeN runs fn `iters` times and returns the median duration.
func timeN(iters int, fn func() error) (time.Duration, error) {
	xs := make([]time.Duration, 0, iters)
	for i := 0; i < iters; i++ {
		d, err := timeOnce(fn)
		if err != nil {
			return 0, err
		}
		xs = append(xs, d)
	}
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	return xs[len(xs)/2], nil
}

func timeOnce(fn func() error) (time.Duration, error) {
	t0 := time.Now()
	err := fn()
	return time.Since(t0), err
}

func runCmd(name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v\n%s", name, strings.Join(arg, " "), err, out)
	}
	return nil
}

func runWith(env map[string]string, name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	cmd.Env = mergeEnv(env)
	cmd.Stdout = nil // /dev/null
	cmd.Stderr = nil
	_ = cmd.Run()
	return nil
}

func captureWith(env map[string]string, name string, arg ...string) (string, time.Duration, error) {
	cmd := exec.Command(name, arg...)
	cmd.Env = mergeEnv(env)
	t0 := time.Now()
	out, err := cmd.CombinedOutput()
	return string(out), time.Since(t0), err
}

func mergeEnv(extra map[string]string) []string {
	if len(extra) == 0 {
		return nil
	}
	env := os.Environ()
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	return env
}

func tmpPath(name string) string { return filepath.Join(os.TempDir(), "tartalo_bench_"+name) }

func rmIfNotKept(path string, keep bool) {
	if keep {
		return
	}
	_ = os.Remove(path)
}

// ─────────────────────────────── format ────────────────────────────────

func msFromUs(us int64) float64 { return float64(us) / 1000.0 }

func fmtMicros(us int64) string {
	switch {
	case us == 0:
		return "—"
	case us < 1000:
		return fmt.Sprintf("%dµs", us)
	case us < 1_000_000:
		return fmt.Sprintf("%.2fms", float64(us)/1000)
	default:
		return fmt.Sprintf("%.2fs", float64(us)/1_000_000)
	}
}

func fmtNs(ns int64) string {
	switch {
	case ns < 1000:
		return fmt.Sprintf("%dns", ns)
	case ns < 1_000_000:
		return fmt.Sprintf("%.2fµs", float64(ns)/1000)
	default:
		return fmt.Sprintf("%.2fms", float64(ns)/1_000_000)
	}
}

func fmtBytes(b int64) string {
	if b == 0 {
		return "—"
	}
	switch {
	case b < 1024:
		return fmt.Sprintf("%dB", b)
	case b < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(b)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(b)/(1024*1024))
	}
}

func yesNo(b bool) string {
	if b {
		return "ok"
	}
	return "MISMATCH"
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func atoi64(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

func die(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "bench: "+format+"\n", a...)
	os.Exit(1)
}
