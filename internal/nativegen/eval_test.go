package nativegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/loader"
	"github.com/enekos/tartalo/internal/nativegen"
	"github.com/enekos/tartalo/internal/parser"
)

// buildEvalMode compiles `src` as an eval binary. Mirrors buildTestMode but
// uses BuildEval / EmitSourceEval. Skips when `go` isn't on PATH.
func buildEvalMode(t *testing.T, src string) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH; skipping native eval-mode build")
	}
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "prog.tt")
	if err := os.WriteFile(srcPath, []byte(src), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	toks, lerrs := lexer.New("prog.tt", src).Tokenize()
	if len(lerrs) > 0 {
		t.Fatalf("lex: %v", lerrs)
	}
	file, perrs := parser.New(toks).Parse("prog.tt")
	if len(perrs) > 0 {
		t.Fatalf("parse: %v", perrs)
	}
	mod := &loader.Module{File: file, IsEntry: true}
	info, cerrs := checker.New().Check([]*loader.Module{mod})
	if len(cerrs) > 0 {
		t.Fatalf("check: %v", cerrs)
	}
	bin := filepath.Join(dir, "evals")
	if err := nativegen.BuildEval([]*loader.Module{mod}, info, nativegen.BuildOptions{Output: bin}); err != nil {
		t.Fatalf("buildEval: %v\n--source--\n%s", err, nativegen.EmitSourceEval([]*loader.Module{mod}, info))
	}
	return bin
}

func runEvalBin(t *testing.T, bin string) (string, int) {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	out, _ := cmd.CombinedOutput()
	return string(out), cmd.ProcessState.ExitCode()
}

func TestNativeEvalPasses(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "exact match" {
			score("em", exactMatch("hi", "hi"))
			expect("em", 1.0)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "1 passed") {
		t.Errorf("expected `1 passed`, got:\n%s", out)
	}
}

func TestNativeEvalFailsBelowThreshold(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "below threshold" {
			score("low", exactMatch("a", "b"))
			expect("low", 1.0)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", out)
	}
	if !strings.Contains(out, "1 failed") {
		t.Errorf("expected `1 failed`, got:\n%s", out)
	}
}

func TestNativeEvalJaccard(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "jaccard" {
			let a: string = "the quick brown fox"
			let b: string = "the lazy brown dog"
			score("j", jaccard(a, b))
			expect("j", 0.3)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "j") {
		t.Errorf("expected jaccard label in output:\n%s", out)
	}
}

func TestNativeEvalContainsScore(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "contains" {
			let pred: string = "Paris is the capital"
			score("c", containsScore(pred, ["Paris", "capital"]))
			expect("c", 1.0)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass, got exit %d:\n%s", code, out)
	}
}

func TestNativeEvalF1Score(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "f1" {
			let preds: string[] = ["red apple", "green pear"]
			let truth: string[] = ["red apple", "yellow pear"]
			score("f1", f1Score(preds, truth))
			expect("f1", 0.5)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass, got exit %d:\n%s", code, out)
	}
}

func TestNativeEvalMultiSampleMean(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "loop" {
			let inputs: string[] = ["one", "two", "three"]
			for x in inputs {
				score("len", containsScore(x, [x]))
			}
			expect("len", 1.0)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "3 sample") {
		t.Errorf("expected `3 sample(s)`, got:\n%s", out)
	}
	if !strings.Contains(out, "3/3 above") {
		t.Errorf("expected `3/3 above`, got:\n%s", out)
	}
}

func TestNativeEvalPanicShowsMessage(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "panicking eval" {
			fail("intentional")
		}
	`)
	out, code := runEvalBin(t, bin)
	if code == 0 {
		t.Fatalf("expected non-zero exit, got 0:\n%s", out)
	}
	if !strings.Contains(out, "intentional") {
		t.Errorf("expected `intentional` in output:\n%s", out)
	}
}

func TestNativeEvalUnGatedScoreShown(t *testing.T) {
	// score(...) without expect(...) reports the value but doesn't fail.
	bin := buildEvalMode(t, `
		eval "info only" {
			score("info", 0.42)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass (no gates), got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "info") || !strings.Contains(out, "0.42") {
		t.Errorf("expected `info  0.42`, got:\n%s", out)
	}
}

// runEvalWithEcho executes an eval-mode binary that prints intermediate
// scalar values via echo(...). The test asserts on the printed lines.
// Useful for verifying the *value* of a metric, not just pass/fail.
func runEvalEcho(t *testing.T, src string) string {
	t.Helper()
	bin := buildEvalMode(t, src)
	out, _ := runEvalBin(t, bin)
	return out
}

func TestNativeEvalLevenshtein(t *testing.T) {
	out := runEvalEcho(t, `
		eval "lev" {
			echo("d=" + str(levenshtein("kitten", "sitting")))
			echo("d_eq=" + str(levenshtein("hi", "hi")))
			echo("d_empty=" + str(levenshtein("", "abcd")))
		}
	`)
	for _, want := range []string{"d=3", "d_eq=0", "d_empty=4"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

func TestNativeEvalLevenshteinRatio(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "ratio" {
			score("ratio", levenshteinRatio("kitten", "sitting"))
			expect("ratio", 0.5)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass, got exit %d:\n%s", code, out)
	}
	// 1 - 3/7 ≈ 0.5714, formatted to 0.57
	if !strings.Contains(out, "0.57") {
		t.Errorf("expected `0.57` in output, got:\n%s", out)
	}
}

func TestNativeEvalF1Tokens(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "f1 tokens" {
			score("f1", f1Tokens("the cat sat", "the cat is here"))
			expect("f1", 0.5)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass, got exit %d:\n%s", code, out)
	}
}

func TestNativeEvalBleuPerfectMatch(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "bleu identical" {
			score("b", bleu("the cat sat on the mat", "the cat sat on the mat"))
			expect("b", 1.0)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass, got exit %d:\n%s", code, out)
	}
}

func TestNativeEvalBleuPartialMatch(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "bleu partial" {
			let b: float = bleu("the cat sat", "the cat sat on the mat")
			score("b", b)
			expect("b", 0.3)  // some n-gram overlap + brevity penalty
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass, got exit %d:\n%s", code, out)
	}
}

func TestNativeEvalRougeL(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "rougeL" {
			// LCS("the cat sat", "the cat sat on the mat") = 3 tokens.
			// prec=3/3=1, rec=3/6=0.5, F1 = 2*1*0.5/1.5 ≈ 0.667
			score("r", rougeL("the cat sat", "the cat sat on the mat"))
			expect("r", 0.6)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass, got exit %d:\n%s", code, out)
	}
	if !strings.Contains(out, "0.67") {
		t.Errorf("expected ROUGE-L ≈ 0.67, got:\n%s", out)
	}
}

func TestNativeEvalCosineSimilarity(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "cosine" {
			score("identical",   cosineSimilarity([1.0, 2.0, 3.0], [1.0, 2.0, 3.0]))
			score("orthogonal",  cosineSimilarity([1.0, 0.0],      [0.0, 1.0]))
			score("opposite",    cosineSimilarity([1.0, 0.0],      [-1.0, 0.0]))
			expect("identical", 1.0)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass, got exit %d:\n%s", code, out)
	}
	// identical → 1.00, orthogonal → 0.00, opposite → -1.00
	for _, want := range []string{"identical", "orthogonal", "opposite", "1.00", "0.00", "-1.00"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got:\n%s", want, out)
		}
	}
}

func TestNativeEvalCosineZeroVector(t *testing.T) {
	// Defined behaviour: similarity of any vector against the zero vector
	// returns 0.0 (rather than NaN from a 0/0 division).
	bin := buildEvalMode(t, `
		eval "zero vec" {
			score("z", cosineSimilarity([0.0, 0.0], [1.0, 1.0]))
			expect("z", 0.0)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass, got exit %d:\n%s", code, out)
	}
}

func TestNativeEvalWithMockedLLM(t *testing.T) {
	bin := buildEvalMode(t, `
		eval "mocked llm" {
			mockLlm("greeting", "hello world")
			let out: string = llm("greeting")
			score("ok", exactMatch(out, "hello world"))
			expect("ok", 1.0)
		}
	`)
	out, code := runEvalBin(t, bin)
	if code != 0 {
		t.Fatalf("expected pass, got exit %d:\n%s", code, out)
	}
}
