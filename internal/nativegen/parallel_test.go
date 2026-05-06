package nativegen_test

import (
	"sort"
	"strings"
	"testing"
)

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

func TestNativeParallelRunsAllTasks(t *testing.T) {
	bin := build(t, `
		func main(): void {
			parallel {
				task { echo("a") }
				task { echo("b") }
				task { echo("c") }
			}
			echo("done")
		}
	`)
	out := runBin(t, bin)
	got := sortLines(out)
	want := []string{"a", "b", "c", "done"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got %v want %v", got, want)
			break
		}
	}
	// Synchronisation: `done` must follow every task.
	if !strings.HasSuffix(strings.TrimRight(out, "\n"), "done") {
		t.Errorf("done was not last; got %q", out)
	}
}

func TestNativeParallelTasksReadOuterLocals(t *testing.T) {
	bin := build(t, `
		func main(): void {
			let prefix: string = "msg-"
			parallel {
				task { echo(prefix + "1") }
				task { echo(prefix + "2") }
			}
		}
	`)
	got := sortLines(runBin(t, bin))
	want := []string{"msg-1", "msg-2"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestNativeParallelEmptyBlockIsNoOp(t *testing.T) {
	bin := build(t, `
		func main(): void {
			parallel {}
			echo("after")
		}
	`)
	if got := strings.TrimRight(runBin(t, bin), "\n"); got != "after" {
		t.Errorf("got %q", got)
	}
}

// Two sibling parallel blocks in the same function must not collide on the
// generated WaitGroup name. emitParallel allocates a fresh tmp() per block;
// this test confirms the unique-naming pathway.
func TestNativeParallelTwoSiblingBlocks(t *testing.T) {
	bin := build(t, `
		func main(): void {
			parallel {
				task { echo("p1.a") }
				task { echo("p1.b") }
			}
			echo("middle")
			parallel {
				task { echo("p2.a") }
				task { echo("p2.b") }
			}
		}
	`)
	out := runBin(t, bin)
	if !strings.Contains(out, "middle") {
		t.Fatalf("missing middle: %q", out)
	}
	parts := strings.SplitN(out, "middle", 2)
	first := sortLines(parts[0])
	second := sortLines(parts[1])
	wantFirst := []string{"p1.a", "p1.b"}
	wantSecond := []string{"p2.a", "p2.b"}
	if len(first) != 2 || first[0] != wantFirst[0] || first[1] != wantFirst[1] {
		t.Errorf("first half: got %v want %v", first, wantFirst)
	}
	if len(second) != 2 || second[0] != wantSecond[0] || second[1] != wantSecond[1] {
		t.Errorf("second half: got %v want %v", second, wantSecond)
	}
}
