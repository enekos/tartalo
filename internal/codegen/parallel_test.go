package codegen_test

import (
	"sort"
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/parser"
)

// sortLines splits stdout on newlines and sorts the non-empty entries. The
// sh backend backgrounds each task with `&`, so output ordering is not
// deterministic — comparing as a sorted set keeps the assertion stable.
func sortLines(s string) []string {
	xs := strings.Split(strings.TrimRight(s, "\n"), "\n")
	out := xs[:0]
	for _, l := range xs {
		if l != "" {
			out = append(out, l)
		}
	}
	sort.Strings(out)
	return out
}

func TestParallelRunsAllTasks(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			parallel {
				task { echo("a") }
				task { echo("b") }
				task { echo("c") }
			}
			echo("done")
		}
	`)
	out := runShell(t, sh)
	got := sortLines(out)
	want := []string{"a", "b", "c", "done"}
	if !equalStrings(got, want) {
		t.Errorf("got %v want %v\n--script--\n%s", got, want, sh)
	}
	// `done` must come last regardless of task ordering: parallel waits.
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), "done") {
		t.Errorf("done was not last; got %q", out)
	}
}

func TestParallelTasksReadOuterLocals(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			let prefix: string = "msg-"
			parallel {
				task { echo(prefix + "1") }
				task { echo(prefix + "2") }
			}
		}
	`)
	got := sortLines(runShell(t, sh))
	want := []string{"msg-1", "msg-2"}
	if !equalStrings(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestParallelEmptyBlockIsNoOp(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			parallel {}
			echo("after")
		}
	`)
	if got := strings.TrimRight(runShell(t, sh), "\n"); got != "after" {
		t.Errorf("got %q", got)
	}
}

func TestParallelRejectAssignToOuter(t *testing.T) {
	src := `
		func main(): void {
			let n: number = 0
			parallel {
				task { n = 1 }
			}
			echo(str(n))
		}
	`
	errs := checkOnly(t, src)
	if !containsErr(errs, "outer-scope variable") {
		t.Fatalf("expected outer-scope assignment error, got: %v", errs)
	}
}

func TestParallelRejectReturnInTask(t *testing.T) {
	src := `
		func main(): void {
			parallel {
				task { return }
			}
		}
	`
	errs := checkOnly(t, src)
	if !containsErr(errs, "return is not allowed inside a task") {
		t.Fatalf("expected return-in-task error, got: %v", errs)
	}
}

func TestParallelRejectDeferInTask(t *testing.T) {
	src := `
		func main(): void {
			parallel {
				task { defer { echo("d") } }
			}
		}
	`
	errs := checkOnly(t, src)
	if !containsErr(errs, "defer is not allowed inside a task") {
		t.Fatalf("expected defer-in-task error, got: %v", errs)
	}
}

func TestParallelRejectNestedParallel(t *testing.T) {
	src := `
		func main(): void {
			parallel {
				task {
					parallel {
						task { echo("inner") }
					}
				}
			}
		}
	`
	errs := checkOnly(t, src)
	if !containsErr(errs, "parallel cannot be nested") {
		t.Fatalf("expected nested-parallel error, got: %v", errs)
	}
}

// Stray `task` outside any `parallel` block is a parse error, surfaced
// via the parser's diagnostic channel rather than checkOnly.
func TestParallelStrayTaskIsParseError(t *testing.T) {
	src := `
		func main(): void {
			task { echo("hi") }
		}
	`
	toks, lerrs := lexer.New("t.tt", src).Tokenize()
	if len(lerrs) > 0 {
		t.Fatalf("lex: %v", lerrs)
	}
	_, perrs := parser.New(toks).Parse("t.tt")
	found := false
	for _, e := range perrs {
		if strings.Contains(e.Error(), "task can only appear inside a parallel block") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected stray-task parse error, got: %v", perrs)
	}
	// Sanity: also accepts a checker call without a panic when parser
	// emits errors — but here we stop at parser since perrs already cover it.
	_ = checker.New
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
