package checker_test

import "testing"

func TestEvalDeclTypeChecks(t *testing.T) {
	wantOk(t, `
		eval "basic" {
			let pred: string = "a b c"
			let ref: string = "a b d"
			score("jaccard", jaccard(pred, ref))
			score("contains", containsScore(pred, ["a"]))
			score("exact", exactMatch(pred, ref))
			expect("jaccard", 0.5)
		}
	`)
}

func TestEvalAcceptsNumberForFloatArg(t *testing.T) {
	// score / expect accept either float or number for the value/threshold —
	// the codegen widens to float64. Sanity-check both.
	wantOk(t, `
		eval "ints widen to floats" {
			score("a", 1)
			expect("a", 0)
		}
	`)
}

func TestScoreOutsideEvalIsRejected(t *testing.T) {
	wantError(t, `
		func main(): void {
			score("nope", 1.0)
		}
	`, "may only be called inside an `eval")
}

func TestExpectOutsideEvalIsRejected(t *testing.T) {
	wantError(t, `
		func main(): void {
			expect("nope", 0.5)
		}
	`, "may only be called inside an `eval")
}

func TestScoreInsideTestIsRejected(t *testing.T) {
	// `eval`-only builtins are intentionally not promoted into `test` blocks
	// even though `eval` bodies inherit test-builtin access. Tests stay
	// pass/fail; evals do scoring.
	wantError(t, `
		test "no scoring in tests" {
			score("nope", 0.5)
		}
	`, "may only be called inside an `eval")
}

func TestF1ScoreSignature(t *testing.T) {
	wantOk(t, `
		eval "f1 over arrays" {
			let preds: string[] = ["a", "b"]
			let truth: string[] = ["a", "c"]
			score("f1", f1Score(preds, truth))
		}
	`)
}

func TestExtraMetricSignatures(t *testing.T) {
	// Each new metric should accept the documented arg shape and produce
	// the documented return type so calls compose with `score(...)`.
	wantOk(t, `
		eval "metrics smoke" {
			let lev: number = levenshtein("a", "b")
			score("lev_ratio", levenshteinRatio("a", "b"))
			score("f1_tok",    f1Tokens("a b", "a c"))
			score("bleu",      bleu("a b c", "a b c"))
			score("rougeL",    rougeL("a b", "a b c"))
			score("cos",       cosineSimilarity([1.0, 0.0], [0.0, 1.0]))
			let _ = lev  // silence unused
		}
	`)
}

func TestCosineSimilarityRejectsWrongElemType(t *testing.T) {
	// cosineSimilarity takes float[], not number[]: the checker should
	// reject an int-array argument so users don't accidentally truncate.
	wantError(t, `
		eval "wrong vec elem" {
			score("c", cosineSimilarity([1, 2, 3], [1, 2, 3]))
		}
	`, "cosineSimilarity")
}

func TestEvalRejectsBadArgTypes(t *testing.T) {
	wantError(t, `
		eval "wrong types" {
			score(42, 1.0)
		}
	`, "label must be a string")

	wantError(t, `
		eval "wrong types" {
			score("ok", "not a number")
		}
	`, "value must be a float or number")
}

func TestEvalCanUseTestBuiltins(t *testing.T) {
	// Eval bodies should accept the assertion / mock builtins so authors
	// can sanity-check intermediate values and stub the LLM during dev.
	wantOk(t, `
		eval "uses test builtins" {
			mockLlm("hi", "hello")
			let out: string = llm("hi")
			check(out == "hello")
			score("ok", 1.0)
		}
	`)
}

func TestDuplicateEvalNameRejected(t *testing.T) {
	wantError(t, `
		eval "dup" { score("a", 1.0) }
		eval "dup" { score("a", 1.0) }
	`, "duplicate eval name")
}
