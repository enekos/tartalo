// tartalo: a small statically-typed scripting language that compiles to sh.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/codegen"
	"github.com/enekos/tartalo/internal/diag"
	"github.com/enekos/tartalo/internal/format"
	"github.com/enekos/tartalo/internal/loader"
	"github.com/enekos/tartalo/internal/lsp"
	"github.com/enekos/tartalo/internal/nativegen"
	"github.com/enekos/tartalo/internal/verify"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		var ce *compileErrors
		if errors.As(err, &ce) {
			renderCompileErrors(os.Stderr, ce)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "tartalo: "+err.Error())
		os.Exit(2)
	}
}

// renderCompileErrors prints structured diagnostics with code frames.
// Falls back to the legacy file:line:col: format when stderr isn't a TTY
// (so CI logs and grep pipelines stay machine-readable) or when
// TARTALO_PLAIN_ERRORS=1 is set.
func renderCompileErrors(w io.Writer, ce *compileErrors) {
	if shouldRenderPlain() {
		for _, e := range ce.errs {
			fmt.Fprintln(w, "tartalo: "+e.Error())
		}
		return
	}
	srcs := diag.MapSources(ce.sources)
	color := isTerminal(w)
	diag.Render(w, diag.FromErrors(ce.errs), srcs, color)
}

func shouldRenderPlain() bool {
	if os.Getenv("TARTALO_PLAIN_ERRORS") == "1" {
		return true
	}
	return os.Getenv("NO_COLOR") != "" && !isTerminalFile(os.Stderr)
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return isTerminalFile(f)
}

func isTerminalFile(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "build":
		return cmdBuild(rest)
	case "run":
		return cmdRun(rest)
	case "check":
		return cmdCheck(rest)
	case "explain":
		return cmdExplain(rest)
	case "doctor":
		return cmdDoctor(rest)
	case "test":
		return cmdTest(rest)
	case "eval":
		return cmdEval(rest)
	case "fmt":
		return cmdFmt(rest)
	case "bench":
		return cmdBench(rest)
	case "lsp":
		return lsp.Run(os.Stdin, os.Stdout)
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	default:
		printUsage(os.Stderr)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `usage:
  tartalo build <file.tt> [-o <out>] [--target=sh|native] [--goos=<os>] [--goarch=<arch>] [--no-verify] [--trace]
  tartalo run   [--target=sh|native] [--no-verify] [--no-trace] <file.tt> [-- args...]
  tartalo test  [--target=sh|native] [--no-verify] <file.tt>   # run all `+"`test \"...\" { ... }`"+` declarations
  tartalo eval  <file-or-dir>                                # run all `+"`eval \"...\" { ... }`"+` declarations (native target)
  tartalo check [--json] <file.tt>...   # type-check; --json emits a structured diagnostics packet
  tartalo explain <code>                # print the long-form explanation of a diagnostic code
  tartalo doctor [--json]               # audit host tools tartalo depends on
  tartalo fmt   [-l|-d|-w] <file.tt>...   # format source (default: rewrite in place)
  tartalo bench <file.tt> [-n N] [--no-run] [--no-verify]   # time compile phases (and run) over N iterations
  tartalo lsp                             # Language Server: diagnostics, hover, definition, symbols, refs, rename, completion
  tartalo help

build defaults to --target=sh which produces a portable POSIX shell script;
use --target=native to compile to a self-contained binary via the Go toolchain
(requires `+"`go`"+` on PATH). --goos and --goarch enable cross-compilation for
the native target.

By default, build/run/test pipe the emitted sh through shellcheck before
writing or executing it. Pass --no-verify (or set TARTALO_NO_VERIFY=1) to
skip the safety check.`)
}

// compileErrors is a typed wrapper around lex/parse/check error lists so the
// `main` function can distinguish user-program errors from internal errors.
// sources holds every source the loader read so the renderer can show code
// frames even when the entry file failed to parse.
type compileErrors struct {
	errs    []error
	sources map[string]string
}

func (c *compileErrors) Error() string {
	parts := make([]string, len(c.errs))
	for i, e := range c.errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "\n")
}

// frontEnd runs lex, parse, import-resolution, and type-check. The returned
// modules are in topological order (deps before dependents) so the codegen
// can iterate them directly.
func frontEnd(path string) ([]*loader.Module, *checker.TypeInfo, error) {
	modules, sources, lerrs := loader.LoadWithSources(path)
	if len(lerrs) > 0 {
		return nil, nil, &compileErrors{errs: lerrs, sources: sources}
	}
	info, cerrs := checker.New().Check(modules)
	if len(cerrs) > 0 {
		return nil, nil, &compileErrors{errs: cerrs, sources: sources}
	}
	return modules, info, nil
}

func compileFile(path string, trace bool) (string, error) {
	modules, info, err := frontEnd(path)
	if err != nil {
		return "", err
	}
	return codegen.New(info).WithTrace(trace).EmitModules(modules), nil
}

// compileFileForTest compiles in test mode: the resulting script runs every
// `test "..."` declaration in the entry module instead of invoking main.
func compileFileForTest(path string) (string, error) {
	modules, info, err := frontEnd(path)
	if err != nil {
		return "", err
	}
	return codegen.New(info).EmitModulesTest(modules), nil
}

// nativeBuildToTemp emits a native binary into a temp dir for run/test. The
// returned path is the binary; cleanup is the caller's responsibility.
func nativeBuildToTemp(input string, mode nativegen.EmitMode, opts nativegen.BuildOptions) (string, func(), error) {
	modules, info, err := frontEnd(input)
	if err != nil {
		return "", nil, err
	}
	dir, mkerr := os.MkdirTemp("", "tartalo-native-bin-*")
	if mkerr != nil {
		return "", nil, mkerr
	}
	binName := strings.TrimSuffix(filepath.Base(input), filepath.Ext(input))
	if binName == "" {
		binName = "tartalo_program"
	}
	binPath := filepath.Join(dir, binName)
	opts.Output = binPath
	cleanup := func() { os.RemoveAll(dir) }
	switch mode {
	case nativegen.EmitTest:
		if err := nativegen.BuildTest(modules, info, opts); err != nil {
			cleanup()
			return "", nil, err
		}
	case nativegen.EmitEval:
		if err := nativegen.BuildEval(modules, info, opts); err != nil {
			cleanup()
			return "", nil, err
		}
	default:
		if err := nativegen.Build(modules, info, opts); err != nil {
			cleanup()
			return "", nil, err
		}
	}
	return binPath, cleanup, nil
}

// loadDotEnv looks for a `.env` file alongside ttPath and returns the
// `KEY=VALUE` entries it defines, filtered to keys not already present in the
// process environment. Existing env vars take precedence — matching the
// convention used by python-dotenv, dotenv-rails, and node's dotenv. Returns
// (nil, nil) when no .env file exists.
//
// Supported syntax (intentionally a small, common subset):
//   - `KEY=value`, optional `export ` prefix, blank lines and `#` comments
//   - double-quoted values with `\n`, `\r`, `\t`, `\\`, `\"` escapes
//   - single-quoted values, taken literally
//   - unquoted values: trailing ` #...` is treated as an inline comment
func loadDotEnv(ttPath string) ([]string, error) {
	envPath := filepath.Join(filepath.Dir(ttPath), ".env")
	data, err := os.ReadFile(envPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	existing := make(map[string]struct{}, len(os.Environ()))
	for _, e := range os.Environ() {
		if i := strings.IndexByte(e, '='); i > 0 {
			existing[e[:i]] = struct{}{}
		}
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		trimmed = strings.TrimPrefix(trimmed, "export ")
		trimmed = strings.TrimLeft(trimmed, " \t")
		eq := strings.IndexByte(trimmed, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(trimmed[:eq])
		if key == "" {
			continue
		}
		val := parseDotEnvValue(trimmed[eq+1:])
		if _, set := existing[key]; set {
			continue
		}
		out = append(out, key+"="+val)
	}
	return out, nil
}

// parseDotEnvValue strips surrounding whitespace, handles `"..."` and `'...'`
// quoting, and removes inline `#` comments from unquoted values. Unterminated
// quotes degrade to "treat the raw value literally" rather than erroring.
func parseDotEnvValue(v string) string {
	v = strings.TrimLeft(v, " \t")
	if v == "" {
		return v
	}
	if v[0] == '"' || v[0] == '\'' {
		q := v[0]
		end := -1
		for i := 1; i < len(v); i++ {
			if q == '"' && v[i] == '\\' && i+1 < len(v) {
				i++
				continue
			}
			if v[i] == q {
				end = i
				break
			}
		}
		if end < 0 {
			return strings.TrimRight(v, " \t")
		}
		inner := v[1:end]
		if q == '"' {
			inner = decodeDotEnvEscapes(inner)
		}
		return inner
	}
	for i := 0; i < len(v); i++ {
		if v[i] == '#' && (i == 0 || v[i-1] == ' ' || v[i-1] == '\t') {
			v = v[:i]
			break
		}
	}
	return strings.TrimRight(v, " \t")
}

func decodeDotEnvEscapes(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			switch s[i+1] {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			default:
				b.WriteByte(s[i])
				b.WriteByte(s[i+1])
			}
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// applyDotEnv loads `.env` next to ttPath (if any) and points cmd.Env at the
// merged environment. No-op when the file is absent or empty.
func applyDotEnv(cmd *exec.Cmd, ttPath string) error {
	extras, err := loadDotEnv(ttPath)
	if err != nil {
		return err
	}
	if len(extras) == 0 {
		return nil
	}
	cmd.Env = append(os.Environ(), extras...)
	return nil
}

// verifySh is the compile-output guardrail: it pipes the emitted script
// through shellcheck (POSIX sh mode) and aborts the command if anything
// survives the suppression list. Skipped when noVerify is true or when the
// TARTALO_NO_VERIFY env var is set to a non-empty value.
//
// When shellcheck isn't installed, we surface a hard error rather than
// silently passing — the whole point of this hook is to *ensure* output
// safety; "we couldn't tell" is not the same as "it's fine."
func verifySh(label, script string, noVerify bool) error {
	if noVerify || os.Getenv("TARTALO_NO_VERIFY") != "" {
		return nil
	}
	findings, err := verify.Run(script)
	if err != nil {
		if errors.Is(err, verify.ErrShellcheckMissing) {
			return fmt.Errorf("%s: shellcheck not found on PATH; install it (brew/apt/etc.) or pass --no-verify to skip the safety check", label)
		}
		return fmt.Errorf("%s: %w", label, err)
	}
	if len(findings) == 0 {
		return nil
	}
	return fmt.Errorf("%s: shellcheck found %d issue(s) in generated sh:\n%s",
		label, len(findings), verify.FormatFindings(findings))
}

func cmdBuild(args []string) error {
	var (
		input    string
		out      string
		hadFlag  bool
		noVerify bool
		trace    bool
		target   = "sh"
		goos     string
		goarch   string
		keepTemp bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-o" || a == "--output":
			if i+1 >= len(args) {
				return fmt.Errorf("build: %s requires a value", a)
			}
			out = args[i+1]
			i++
			hadFlag = true
		case strings.HasPrefix(a, "-o="):
			out = strings.TrimPrefix(a, "-o=")
			hadFlag = true
		case strings.HasPrefix(a, "--output="):
			out = strings.TrimPrefix(a, "--output=")
			hadFlag = true
		case strings.HasPrefix(a, "--target="):
			target = strings.TrimPrefix(a, "--target=")
		case a == "--target":
			if i+1 >= len(args) {
				return fmt.Errorf("build: --target requires a value")
			}
			target = args[i+1]
			i++
		case strings.HasPrefix(a, "--goos="):
			goos = strings.TrimPrefix(a, "--goos=")
		case strings.HasPrefix(a, "--goarch="):
			goarch = strings.TrimPrefix(a, "--goarch=")
		case a == "--keep-temp":
			keepTemp = true
		case a == "--no-verify":
			noVerify = true
		case a == "--trace":
			trace = true
		case a == "-h" || a == "--help":
			fmt.Println("usage: tartalo build <file.tt> [-o <out>] [--target=sh|native] [--goos=<os>] [--goarch=<arch>] [--no-verify] [--trace]")
			return nil
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("build: unknown flag %q", a)
		default:
			if input != "" {
				return fmt.Errorf("build: expected exactly one input file")
			}
			input = a
		}
	}
	_ = hadFlag
	if input == "" {
		return fmt.Errorf("build: expected an input file")
	}
	switch target {
	case "sh":
		sh, err := compileFile(input, trace)
		if err != nil {
			return err
		}
		if err := verifySh("build", sh, noVerify); err != nil {
			return err
		}
		dst := out
		if dst == "" {
			dst = strings.TrimSuffix(input, filepath.Ext(input)) + ".sh"
		}
		if err := os.WriteFile(dst, []byte(sh), 0o755); err != nil {
			return err
		}
		return nil
	case "native":
		modules, info, err := frontEnd(input)
		if err != nil {
			return err
		}
		dst := out
		if dst == "" {
			dst = strings.TrimSuffix(input, filepath.Ext(input))
			if goos == "windows" && !strings.HasSuffix(dst, ".exe") {
				dst += ".exe"
			}
		}
		return nativegen.Build(modules, info, nativegen.BuildOptions{
			Output:   dst,
			GOOS:     goos,
			GOARCH:   goarch,
			KeepTemp: keepTemp,
		})
	default:
		return fmt.Errorf("build: unknown --target %q (expected sh or native)", target)
	}
}

func cmdCheck(args []string) error {
	var (
		jsonOut bool
		inputs  []string
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			jsonOut = true
		case a == "-h" || a == "--help":
			fmt.Println("usage: tartalo check [--json] <file.tt>...")
			return nil
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("check: unknown flag %q", a)
		default:
			inputs = append(inputs, a)
		}
	}
	if len(inputs) == 0 {
		return fmt.Errorf("check: expected at least one input file")
	}
	var combined []error
	combinedSources := map[string]string{}
	for _, in := range inputs {
		if _, _, err := frontEnd(in); err != nil {
			var ce *compileErrors
			if errors.As(err, &ce) {
				combined = append(combined, ce.errs...)
				for k, v := range ce.sources {
					combinedSources[k] = v
				}
			} else {
				combined = append(combined, err)
			}
		}
	}
	if jsonOut {
		diags := diag.FromErrors(combined)
		if err := diag.EncodePacket(os.Stdout, diags); err != nil {
			return err
		}
		if len(diags) > 0 {
			os.Exit(1)
		}
		return nil
	}
	if len(combined) > 0 {
		return &compileErrors{errs: combined, sources: combinedSources}
	}
	return nil
}

// cmdExplain implements `tartalo explain <code>`. The bundled markdown for
// each stable diagnostic code lives under internal/diag/explain/. With
// `--list` the command prints a table of every documented code instead.
func cmdExplain(args []string) error {
	if len(args) == 0 {
		fmt.Println(diag.FormatExplainList())
		fmt.Println("usage: tartalo explain <code>   (e.g. tartalo explain TT-NAM001)")
		return nil
	}
	switch args[0] {
	case "-h", "--help":
		fmt.Println("usage: tartalo explain <code>")
		fmt.Println("       tartalo explain --list")
		return nil
	case "--list":
		fmt.Print(diag.FormatExplainList())
		return nil
	}
	code := strings.ToUpper(strings.TrimSpace(args[0]))
	body, ok := diag.Explain(code)
	if !ok {
		fmt.Fprintf(os.Stderr, "tartalo: no explanation bundled for %q\n", code)
		fmt.Fprintln(os.Stderr, "Run `tartalo explain --list` to see every documented code.")
		os.Exit(1)
	}
	fmt.Println(body)
	return nil
}

// cmdDoctor implements `tartalo explain`'s companion: a PATH audit for the
// host tools tartalo's emitted scripts and the native target depend on.
// Output is human-readable by default; pass --json for a structured shape.
func cmdDoctor(args []string) error {
	var jsonOut bool
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "-h", "--help":
			fmt.Println("usage: tartalo doctor [--json]")
			return nil
		default:
			return fmt.Errorf("doctor: unknown flag %q", a)
		}
	}
	report := buildDoctorReport()
	if jsonOut {
		enc := newJSONEncoder(os.Stdout)
		return enc.Encode(report)
	}
	renderDoctorReport(os.Stdout, report)
	if !report.Ok {
		os.Exit(1)
	}
	return nil
}

func cmdTest(args []string) error {
	var (
		input    string
		noVerify bool
		target   = "sh"
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Println("usage: tartalo test <file-or-dir> [--target=sh|native] [--no-verify]")
			return nil
		case a == "--no-verify":
			noVerify = true
		case strings.HasPrefix(a, "--target="):
			target = strings.TrimPrefix(a, "--target=")
		case a == "--target":
			if i+1 >= len(args) {
				return fmt.Errorf("test: --target requires a value")
			}
			target = args[i+1]
			i++
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("test: unknown flag %q", a)
		default:
			if input != "" {
				return fmt.Errorf("test: expected exactly one input file or directory")
			}
			input = a
		}
	}
	if input == "" {
		return fmt.Errorf("test: expected an input file or directory")
	}
	// Directory input: walk it, collect every .tt with at least one `test`
	// declaration, run them in lexicographic order, aggregate the result.
	if info, err := os.Stat(input); err == nil && info.IsDir() {
		return runTestDir(input, target, noVerify)
	}
	switch target {
	case "sh":
		sh, err := compileFileForTest(input)
		if err != nil {
			return err
		}
		if err := verifySh("test", sh, noVerify); err != nil {
			return err
		}
		tmp, err := os.CreateTemp("", "tartalo-test-*.sh")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.WriteString(sh); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		cmd := exec.Command("/bin/sh", tmp.Name())
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := applyDotEnv(cmd, input); err != nil {
			return err
		}
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				os.Exit(ee.ExitCode())
			}
			return err
		}
		return nil
	case "native":
		bin, cleanup, err := nativeBuildToTemp(input, nativegen.EmitTest, nativegen.BuildOptions{})
		if err != nil {
			return err
		}
		defer cleanup()
		cmd := exec.Command(bin)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := applyDotEnv(cmd, input); err != nil {
			return err
		}
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				os.Exit(ee.ExitCode())
			}
			return err
		}
		return nil
	default:
		return fmt.Errorf("test: unknown --target %q (expected sh or native)", target)
	}
}

// runTestDir walks dir, finds every `.tt` file containing at least one `test`
// declaration, runs them sequentially, and prints a per-file header plus an
// aggregate summary. Exit status is non-zero iff any file's tests failed.
//
// Each file is run as its own entry — imports are still resolved per file —
// so two unrelated test files in the same directory don't have to know about
// each other. Files inside `node_modules`, `.git`, or any directory starting
// with `.` are skipped.
func runTestDir(dir, target string, noVerify bool) error {
	files, err := discoverTestFiles(dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no .tt files with `test \"...\"` declarations under "+dir)
		return nil
	}
	totalFails := 0
	for _, f := range files {
		fmt.Println("=== " + f)
		if err := runOneTestFile(f, target, noVerify); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				totalFails++
				continue
			}
			return err
		}
	}
	fmt.Println()
	if totalFails > 0 {
		fmt.Printf("%d file(s) failed out of %d\n", totalFails, len(files))
		os.Exit(1)
	}
	fmt.Printf("all %d file(s) passed\n", len(files))
	return nil
}

// discoverTestFiles returns every `.tt` file under dir whose source contains
// at least one top-level `test "..."` declaration. The check is deliberately
// shallow (a regex over the source text) to avoid running the full frontend
// against every file just to filter — collisions with `test` inside strings
// or comments are rare and the worst outcome is a file with no actual tests
// being run, which is harmless.
func discoverTestFiles(dir string) ([]string, error) {
	testDecl := regexp.MustCompile(`(?m)^\s*test\s+"`)
	var found []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			name := filepath.Base(path)
			if path != dir && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".tt") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if testDecl.Match(src) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(found)
	return found, nil
}

// cmdEval implements `tartalo eval`. Mirrors `tartalo test` but only on the
// native target — LLM evals don't make sense in pure sh, and the eval
// harness uses Go-only constructs (sort.Slice, time.Duration, etc.). Accepts
// either a single file or a directory; the latter walks recursively and
// runs every .tt file containing at least one `eval "..."` declaration.
func cmdEval(args []string) error {
	var input string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Println("usage: tartalo eval <file-or-dir>")
			return nil
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("eval: unknown flag %q", a)
		default:
			if input != "" {
				return fmt.Errorf("eval: expected exactly one input file or directory")
			}
			input = a
		}
	}
	if input == "" {
		return fmt.Errorf("eval: expected an input file or directory")
	}
	if info, err := os.Stat(input); err == nil && info.IsDir() {
		return runEvalDir(input)
	}
	bin, cleanup, err := nativeBuildToTemp(input, nativegen.EmitEval, nativegen.BuildOptions{})
	if err != nil {
		return err
	}
	defer cleanup()
	cmd := exec.Command(bin)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := applyDotEnv(cmd, input); err != nil {
		return err
	}
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}

// runEvalDir is the eval counterpart of runTestDir. Same walk rules; one
// file at a time so a parse error in one suite doesn't block the others.
func runEvalDir(dir string) error {
	files, err := discoverEvalFiles(dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "no .tt files with `eval \"...\"` declarations under "+dir)
		return nil
	}
	totalFails := 0
	for _, f := range files {
		fmt.Println("=== " + f)
		if err := runOneEvalFile(f); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				totalFails++
				continue
			}
			return err
		}
	}
	fmt.Println()
	if totalFails > 0 {
		fmt.Printf("%d file(s) failed out of %d\n", totalFails, len(files))
		os.Exit(1)
	}
	fmt.Printf("all %d file(s) passed\n", len(files))
	return nil
}

func discoverEvalFiles(dir string) ([]string, error) {
	evalDecl := regexp.MustCompile(`(?m)^\s*eval\s+"`)
	var found []string
	err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			name := filepath.Base(path)
			if path != dir && (strings.HasPrefix(name, ".") || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(info.Name(), ".tt") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if evalDecl.Match(src) {
			found = append(found, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(found)
	return found, nil
}

func runOneEvalFile(input string) error {
	bin, cleanup, err := nativeBuildToTemp(input, nativegen.EmitEval, nativegen.BuildOptions{})
	if err != nil {
		return err
	}
	defer cleanup()
	cmd := exec.Command(bin)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := applyDotEnv(cmd, input); err != nil {
		return err
	}
	return cmd.Run()
}

// runOneTestFile compiles + executes a single test file and returns the
// child process's error verbatim (so the caller can detect non-zero exits
// and keep aggregating).
func runOneTestFile(input, target string, noVerify bool) error {
	switch target {
	case "sh":
		sh, err := compileFileForTest(input)
		if err != nil {
			return err
		}
		if err := verifySh("test", sh, noVerify); err != nil {
			return err
		}
		tmp, err := os.CreateTemp("", "tartalo-test-*.sh")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.WriteString(sh); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		cmd := exec.Command("/bin/sh", tmp.Name())
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := applyDotEnv(cmd, input); err != nil {
			return err
		}
		return cmd.Run()
	case "native":
		bin, cleanup, err := nativeBuildToTemp(input, nativegen.EmitTest, nativegen.BuildOptions{})
		if err != nil {
			return err
		}
		defer cleanup()
		cmd := exec.Command(bin)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := applyDotEnv(cmd, input); err != nil {
			return err
		}
		return cmd.Run()
	default:
		return fmt.Errorf("test: unknown --target %q (expected sh or native)", target)
	}
}

func cmdRun(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("run: expected an input file")
	}
	// Strip leading run-only flags (--no-verify, --trace/--no-trace, --target).
	// Anything after the input file is forwarded to the script. Trace is on by
	// default for `run` so users get Rust-style runtime error frames out of
	// the box; --no-trace opts out (e.g., to capture the exact stderr the
	// produced .sh would emit on its own).
	noVerify := false
	trace := true
	target := "sh"
	for len(args) > 0 {
		switch {
		case args[0] == "--no-verify":
			noVerify = true
			args = args[1:]
		case args[0] == "--trace":
			trace = true
			args = args[1:]
		case args[0] == "--no-trace":
			trace = false
			args = args[1:]
		case strings.HasPrefix(args[0], "--target="):
			target = strings.TrimPrefix(args[0], "--target=")
			args = args[1:]
		case args[0] == "--target":
			if len(args) < 2 {
				return fmt.Errorf("run: --target requires a value")
			}
			target = args[1]
			args = args[2:]
		default:
			goto doneFlags
		}
	}
doneFlags:
	if len(args) == 0 {
		return fmt.Errorf("run: expected an input file")
	}
	in := args[0]
	scriptArgs := args[1:]
	if len(scriptArgs) > 0 && scriptArgs[0] == "--" {
		scriptArgs = scriptArgs[1:]
	}
	switch target {
	case "sh":
		sh, err := compileFile(in, trace)
		if err != nil {
			return err
		}
		if err := verifySh("run", sh, noVerify); err != nil {
			return err
		}

		tmp, err := os.CreateTemp("", "tartalo-*.sh")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.WriteString(sh); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}

		cmd := exec.Command("/bin/sh", append([]string{tmp.Name()}, scriptArgs...)...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := applyDotEnv(cmd, in); err != nil {
			return err
		}
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				os.Exit(ee.ExitCode())
			}
			return err
		}
		return nil
	case "native":
		bin, cleanup, err := nativeBuildToTemp(in, nativegen.EmitRun, nativegen.BuildOptions{})
		if err != nil {
			return err
		}
		defer cleanup()
		cmd := exec.Command(bin, scriptArgs...)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := applyDotEnv(cmd, in); err != nil {
			return err
		}
		if err := cmd.Run(); err != nil {
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				os.Exit(ee.ExitCode())
			}
			return err
		}
		return nil
	default:
		return fmt.Errorf("run: unknown --target %q (expected sh or native)", target)
	}
}

// cmdFmt implements `tartalo fmt`. The default action is in-place rewrite,
// matching gofmt with `-w`. Flags:
//
//	-l     list files whose formatting differs from the canonical form
//	-d     write a unified-style diff to stdout instead of rewriting
//	-w     write back to source (the default; included for parity with gofmt)
//	--     end of flags
//
// With no file arguments, fmt reads from stdin and writes to stdout. A
// non-zero exit indicates either an unparseable input or, in -l mode, that at
// least one file would change.
func cmdFmt(args []string) error {
	var (
		listOnly bool
		diffOnly bool
		write    = true
		paths    []string
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-l":
			listOnly = true
			write = false
		case a == "-d":
			diffOnly = true
			write = false
		case a == "-w":
			write = true
		case a == "-h" || a == "--help":
			fmt.Println("usage: tartalo fmt [-l|-d|-w] <file.tt>...")
			return nil
		case a == "--":
			paths = append(paths, args[i+1:]...)
			i = len(args)
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("fmt: unknown flag %q", a)
		default:
			paths = append(paths, a)
		}
	}

	if len(paths) == 0 {
		// stdin → stdout
		src, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		out, err := format.Source("<stdin>", string(src))
		if err != nil {
			return err
		}
		_, _ = io.WriteString(os.Stdout, out)
		return nil
	}

	anyDiff := false
	for _, path := range paths {
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out, err := format.Source(filepath.Base(path), string(src))
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if string(src) == out {
			continue
		}
		anyDiff = true
		switch {
		case listOnly:
			fmt.Println(path)
		case diffOnly:
			io.WriteString(os.Stdout, simpleDiff(path, string(src), out))
		case write:
			if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
				return err
			}
		}
	}
	if listOnly && anyDiff {
		os.Exit(1)
	}
	return nil
}

// cmdBench measures how long each compile phase takes on a given source file
// and (unless --no-run is set) how long the resulting script takes to run.
// Each phase is timed across N iterations and reported as min / median / mean
// / max. Output is plain-text, one row per phase.
func cmdBench(args []string) error {
	var (
		input    string
		n        = 5
		noVerify bool
		noRun    bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-n":
			if i+1 >= len(args) {
				return fmt.Errorf("bench: -n requires a value")
			}
			v, err := parsePositiveInt(args[i+1])
			if err != nil {
				return fmt.Errorf("bench: -n: %w", err)
			}
			n = v
			i++
		case strings.HasPrefix(a, "-n="):
			v, err := parsePositiveInt(strings.TrimPrefix(a, "-n="))
			if err != nil {
				return fmt.Errorf("bench: -n: %w", err)
			}
			n = v
		case a == "--no-verify":
			noVerify = true
		case a == "--no-run":
			noRun = true
		case a == "-h" || a == "--help":
			fmt.Println("usage: tartalo bench <file.tt> [-n N] [--no-run] [--no-verify]")
			return nil
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("bench: unknown flag %q", a)
		default:
			if input != "" {
				return fmt.Errorf("bench: expected exactly one input file")
			}
			input = a
		}
	}
	if input == "" {
		return fmt.Errorf("bench: expected an input file")
	}

	phases := []string{"frontend", "codegen"}
	if !noVerify {
		phases = append(phases, "verify")
	}
	if !noRun {
		phases = append(phases, "run")
	}
	timings := map[string][]time.Duration{}

	for iter := 0; iter < n; iter++ {
		t0 := time.Now()
		modules, info, err := frontEnd(input)
		timings["frontend"] = append(timings["frontend"], time.Since(t0))
		if err != nil {
			return err
		}

		t0 = time.Now()
		sh := codegen.New(info).EmitModules(modules)
		timings["codegen"] = append(timings["codegen"], time.Since(t0))

		if !noVerify {
			t0 = time.Now()
			if err := verifySh("bench", sh, false); err != nil {
				return err
			}
			timings["verify"] = append(timings["verify"], time.Since(t0))
		}

		if !noRun {
			tmp, err := os.CreateTemp("", "tartalo-bench-*.sh")
			if err != nil {
				return err
			}
			if _, err := tmp.WriteString(sh); err != nil {
				tmp.Close()
				os.Remove(tmp.Name())
				return err
			}
			if err := tmp.Close(); err != nil {
				os.Remove(tmp.Name())
				return err
			}
			t0 = time.Now()
			cmd := exec.Command("/bin/sh", tmp.Name())
			cmd.Stdin = nil
			cmd.Stdout = io.Discard
			cmd.Stderr = io.Discard
			if err := applyDotEnv(cmd, input); err != nil {
				os.Remove(tmp.Name())
				return err
			}
			runErr := cmd.Run()
			timings["run"] = append(timings["run"], time.Since(t0))
			os.Remove(tmp.Name())
			if runErr != nil {
				return fmt.Errorf("bench: script failed on iteration %d: %w", iter+1, runErr)
			}
		}
	}

	fmt.Printf("tartalo bench: %s  (n=%d)\n", input, n)
	fmt.Printf("%-10s %10s %10s %10s %10s\n", "phase", "min", "median", "mean", "max")
	for _, p := range phases {
		ds := timings[p]
		min, med, mean, max := summarize(ds)
		fmt.Printf("%-10s %10s %10s %10s %10s\n",
			p, fmtDur(min), fmtDur(med), fmtDur(mean), fmtDur(max))
	}
	return nil
}

func parsePositiveInt(s string) (int, error) {
	v := 0
	if s == "" {
		return 0, fmt.Errorf("expected a number")
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("expected a number, got %q", s)
		}
		v = v*10 + int(c-'0')
	}
	if v <= 0 {
		return 0, fmt.Errorf("must be > 0")
	}
	return v, nil
}

func summarize(ds []time.Duration) (min, med, mean, max time.Duration) {
	if len(ds) == 0 {
		return
	}
	cp := make([]time.Duration, len(ds))
	copy(cp, ds)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	min = cp[0]
	max = cp[len(cp)-1]
	med = cp[len(cp)/2]
	var total time.Duration
	for _, d := range cp {
		total += d
	}
	mean = total / time.Duration(len(cp))
	return
}

func fmtDur(d time.Duration) string {
	switch {
	case d >= time.Second:
		return fmt.Sprintf("%.2fs", d.Seconds())
	case d >= time.Millisecond:
		return fmt.Sprintf("%.2fms", float64(d)/float64(time.Millisecond))
	default:
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
}

// simpleDiff returns a header-prefixed line-by-line diff that's good enough
// for human inspection without pulling in an external diff dependency. Lines
// only in the original are prefixed `-`, lines only in the formatted output
// are prefixed `+`, and a context header names the file.
func simpleDiff(path, before, after string) string {
	if before == after {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s (original)\n+++ %s (formatted)\n", path, path)
	bs := strings.Split(before, "\n")
	as := strings.Split(after, "\n")
	// Longest common prefix and suffix to compress identical context lines.
	pre := 0
	for pre < len(bs) && pre < len(as) && bs[pre] == as[pre] {
		pre++
	}
	suf := 0
	for suf < len(bs)-pre && suf < len(as)-pre && bs[len(bs)-1-suf] == as[len(as)-1-suf] {
		suf++
	}
	if pre > 0 {
		fmt.Fprintf(&b, "@@ ... %d unchanged lines @@\n", pre)
	}
	for _, line := range bs[pre : len(bs)-suf] {
		fmt.Fprintf(&b, "-%s\n", line)
	}
	for _, line := range as[pre : len(as)-suf] {
		fmt.Fprintf(&b, "+%s\n", line)
	}
	if suf > 0 {
		fmt.Fprintf(&b, "@@ ... %d unchanged lines @@\n", suf)
	}
	return b.String()
}
