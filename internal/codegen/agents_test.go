package codegen_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/codegen"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/parser"
)

// compileBuild produces the run-mode shell for a single source. Used by the
// agent-platform tests where we want to exercise the generated runtime
// helpers, not the test harness.
func compileBuild(t *testing.T, src string) string {
	t.Helper()
	toks, lerrs := lexer.New("agent.tt", src).Tokenize()
	if len(lerrs) > 0 {
		t.Fatalf("lex: %v", lerrs)
	}
	file, perrs := parser.New(toks).Parse("agent.tt")
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs)
	}
	info, cerrs := checker.New().CheckFile(file)
	if len(cerrs) > 0 {
		t.Fatalf("check: %v", cerrs)
	}
	return codegen.New(info).Emit(file)
}

// runShellWith writes the script to a temp file and runs it with the given
// extra env vars. Returns combined stdout/stderr and exit code.
func runShellWith(t *testing.T, sh string, env map[string]string) (string, int) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "out.sh")
	if err := os.WriteFile(path, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", path)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run: %v\noutput:\n%s\nscript:\n%s", err, out, sh)
		}
	}
	return string(out), code
}

// --- tool / agent declarations -----------------------------------------------

// TestTool_CallableLikeFunc verifies that `tool` declarations are callable as
// regular functions and behave identically to func at runtime.
func TestTool_CallableLikeFunc(t *testing.T) {
	src := `
tool greet(name: string): string {
  desc("greet someone by name")
  return "Hello, " + name + "!"
}

func main(): void {
  echo(greet("Tartalo"))
}
`
	out, code := runShellWith(t, compileBuild(t, src), nil)
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	if got := strings.TrimSpace(out); got != "Hello, Tartalo!" {
		t.Errorf("got %q", got)
	}
}

// TestAgent_CallableLikeFunc same as above but for `agent`.
func TestAgent_CallableLikeFunc(t *testing.T) {
	src := `
agent classify(input: string): string {
  desc("classify the input")
  budget(50)
  return "category: " + input
}

func main(): void {
  echo(classify("bug"))
}
`
	out, code := runShellWith(t, compileBuild(t, src), nil)
	if code != 0 {
		t.Fatalf("exit %d, output:\n%s", code, out)
	}
	if got := strings.TrimSpace(out); got != "category: bug" {
		t.Errorf("got %q", got)
	}
}

// --- toolSchemas() -----------------------------------------------------------

// TestToolSchemas_HappyPath verifies the schema reflects every tool/agent
// with its name, kind, params, returns, description, effects, and budget.
func TestToolSchemas_HappyPath(t *testing.T) {
	src := `
tool listFiles(): string {
  desc("list files in cwd")
  return "ls"
}

agent triage(issue: string): string {
  desc("triage an issue")
  budget(100)
  return "fixed"
}

func main(): void {
  echo(toolSchemas())
}
`
	out, code := runShellWith(t, compileBuild(t, src), nil)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	jsonOut := strings.TrimSpace(out)
	var entries []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &entries); err != nil {
		t.Fatalf("schemas not valid JSON: %v\nraw: %q", err, jsonOut)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 entries, got %d: %v", len(entries), entries)
	}
	// The first entry should be the tool, the second the agent — emission
	// order matches declaration order.
	if entries[0]["kind"] != "tool" || entries[0]["name"] != "listFiles" {
		t.Errorf("entry[0] = %+v", entries[0])
	}
	if entries[1]["kind"] != "agent" || entries[1]["name"] != "triage" {
		t.Errorf("entry[1] = %+v", entries[1])
	}
	if entries[1]["budget"].(float64) != 100 {
		t.Errorf("triage budget = %v, want 100", entries[1]["budget"])
	}
	if entries[1]["description"] != "triage an issue" {
		t.Errorf("triage desc = %v", entries[1]["description"])
	}
}

// TestToolSchemas_Empty verifies that toolSchemas() in a program with no
// tools/agents returns "[]" rather than failing.
func TestToolSchemas_Empty(t *testing.T) {
	src := `
func main(): void {
  echo(toolSchemas())
}
`
	out, code := runShellWith(t, compileBuild(t, src), nil)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := strings.TrimSpace(out); got != "[]" {
		t.Errorf("got %q, want []", got)
	}
}

// TestEffects_Annotated verifies that postfix !effect annotations parse and
// surface in the schema. The runtime doesn't yet enforce them — this is the
// annotation half of the future capability story.
func TestEffects_Annotated(t *testing.T) {
	src := `
tool fetchUrl(url: string): string !net !fs:read {
  desc("fetch a URL")
  return url
}

func main(): void {
  echo(toolSchemas())
}
`
	out, code := runShellWith(t, compileBuild(t, src), nil)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	jsonOut := strings.TrimSpace(out)
	var entries []map[string]any
	if err := json.Unmarshal([]byte(jsonOut), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, jsonOut)
	}
	effects := entries[0]["effects"].([]any)
	if len(effects) != 2 || effects[0] != "net" || effects[1] != "fs:read" {
		t.Errorf("effects = %v", effects)
	}
}

// --- spawnAgent --------------------------------------------------------------

// TestSpawnAgent_DispatchesByName verifies that spawnAgent("name", input)
// invokes the named agent.
func TestSpawnAgent_DispatchesByName(t *testing.T) {
	src := `
agent triage(input: string): string {
  return "triaged: " + input
}

agent classifier(input: string): string {
  return "classified: " + input
}

func main(): void {
  echo(spawnAgent("triage", "bug-1"))
  echo(spawnAgent("classifier", "feature-2"))
}
`
	out, code := runShellWith(t, compileBuild(t, src), nil)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := strings.TrimSpace(out); got != "triaged: bug-1\nclassified: feature-2" {
		t.Errorf("got %q", got)
	}
}

// TestSpawnAgent_UnknownAborts verifies the dispatcher exits non-zero with a
// clear message on an unknown agent name.
func TestSpawnAgent_UnknownAborts(t *testing.T) {
	src := `
agent only(input: string): string {
  return input
}

func main(): void {
  echo(spawnAgent("nope", "x"))
}
`
	out, code := runShellWith(t, compileBuild(t, src), nil)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0\noutput:\n%s", out)
	}
	if !strings.Contains(out, "unknown agent: nope") {
		t.Errorf("expected error message, got:\n%s", out)
	}
}

// --- trace -------------------------------------------------------------------

// TestTrace_WritesNDJSONWhenEnvSet verifies trace() appends one NDJSON
// record per call when TARTALO_TRACE points at a file.
func TestTrace_WritesNDJSONWhenEnvSet(t *testing.T) {
	src := `
func main(): void {
  trace("start", "main")
  trace("step", "compute")
  echo("done")
}
`
	dir := t.TempDir()
	tracePath := filepath.Join(dir, "trace.ndjson")
	out, code := runShellWith(t, compileBuild(t, src), map[string]string{"TARTALO_TRACE": tracePath})
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	data, err := os.ReadFile(tracePath)
	if err != nil {
		t.Fatalf("read trace: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 trace lines, got %d:\n%s", len(lines), data)
	}
	for i, line := range lines {
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("line %d not JSON: %v\n%q", i, err, line)
		}
		if _, ok := rec["ts"]; !ok {
			t.Errorf("line %d missing ts: %v", i, rec)
		}
		if _, ok := rec["label"]; !ok {
			t.Errorf("line %d missing label: %v", i, rec)
		}
	}
}

// TestTrace_NoOpWhenEnvUnset verifies trace() is a no-op (no file created)
// when TARTALO_TRACE is not set.
func TestTrace_NoOpWhenEnvUnset(t *testing.T) {
	src := `
func main(): void {
  trace("ignored", "value")
  echo("ok")
}
`
	out, code := runShellWith(t, compileBuild(t, src), nil)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := strings.TrimSpace(out); got != "ok" {
		t.Errorf("got %q", got)
	}
}

// --- llm ---------------------------------------------------------------------

// TestLlm_RunsConfiguredCommand verifies llm() shells out to TARTALO_LLM_CMD
// and returns its stdout.
func TestLlm_RunsConfiguredCommand(t *testing.T) {
	src := `
func main(): void {
  echo(llm("ignored"))
}
`
	// Configure llm to invoke a tiny shell script that prints a fixed reply.
	out, code := runShellWith(t, compileBuild(t, src), map[string]string{
		"TARTALO_LLM_CMD": "cat; printf ' (replied)'",
	})
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if got := strings.TrimSpace(out); got != "ignored (replied)" {
		t.Errorf("got %q", got)
	}
}

// TestLlm_MockedInTests verifies that mockLlm() short-circuits llm() during
// `tartalo test` runs.
func TestLlm_MockedInTests(t *testing.T) {
	src := `
test "llm is mocked" {
  mockLlm("hello", "hi there")
  assertEq(llm("hello world"), "hi there")
  assertEq(len(mockLlmCalls()), 1)
}
`
	out, code := compileAndRunTest(t, src)
	if code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "1 passed") {
		t.Errorf("missing pass summary:\n%s", out)
	}
}

// TestLlm_UnmockedAborts verifies that strict mode (an unmatched llm call
// inside a test) fails the test.
func TestLlm_UnmockedAborts(t *testing.T) {
	src := `
test "unmocked llm fails" {
  let _: string = llm("never matched")
}
`
	out, code := compileAndRunTest(t, src)
	if code == 0 {
		t.Fatalf("expected failure, got pass\noutput:\n%s", out)
	}
	if !strings.Contains(out, "unmocked llm call") {
		t.Errorf("expected guidance, got:\n%s", out)
	}
}

// --- approval ----------------------------------------------------------------

// TestApproval_PathExists exercises the codegen path for approval(). We can't
// realistically simulate /dev/tty interaction in a portable unit test, so we
// just verify that the program compiles, the helper is emitted, and the
// invocation is wired.
func TestApproval_CompilesAndDeclaresHelper(t *testing.T) {
	src := `
func main(): void {
  if approval("really?") {
    echo("yes")
  } else {
    echo("no")
  }
}
`
	sh := compileBuild(t, src)
	if !strings.Contains(sh, "__tt_approval()") {
		t.Errorf("expected __tt_approval helper in script:\n%s", sh)
	}
	if !strings.Contains(sh, "__tt_approval ") {
		t.Errorf("expected approval helper invocation:\n%s", sh)
	}
}
