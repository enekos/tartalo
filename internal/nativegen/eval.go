package nativegen

import (
	"strconv"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/loader"
)

// markUsesEvalState flips the runtime-eval-state flag and pre-registers the
// imports the eval-harness blob needs (`math` for bleu/rougeL/cosineSim,
// `strings` for tokenization). Called from every builtin case that lowers to
// a `_tt_<name>(...)` helper defined inside `runtimeEvalHarness`, so the
// harness is guaranteed to compile no matter which helper the user actually
// calls — including in EmitRun mode where there's no eval block to drag in
// the same imports separately.
func (g *Generator) markUsesEvalState() {
	g.usesRuntimeEvalState = true
	g.addImport("math")
	g.addImport("strings")
}

// emitEvalFunctions emits one Go closure per `eval "..." { ... }` declaration
// in the entry module. The closures are stored alongside their display names
// in a `_tt_evals` slice that the harness drives from main(). Mirrors
// emitTestFunctions so eval bodies share all of the function-emitting
// machinery (locals, control flow, defer, etc.).
func (g *Generator) emitEvalFunctions(entry *loader.Module) {
	if entry == nil {
		return
	}
	idx := 0
	g.currentModule = entry
	for _, d := range entry.File.Decls {
		ed, ok := d.(*ast.EvalDecl)
		if !ok {
			continue
		}
		idx++
		g.writeLine("func _tt_eval_" + itoa(idx) + "() {")
		g.indent++
		for _, s := range ed.Body.Stmts {
			g.emitStmt(s)
		}
		g.indent--
		g.writeLine("}")
		g.writeLine("")
	}
}

// emitEvalRunnerCall builds the `[]_tt_evalCase{...}` slice and hands it to
// `_tt_runEvals`. The eval bodies were emitted by emitEvalFunctions; here we
// just point the runner at them in declaration order.
func (g *Generator) emitEvalRunnerCall(entry *loader.Module) {
	g.usesRuntimeEvalState = true
	g.addImport("fmt")
	g.addImport("math")
	g.addImport("os")
	g.addImport("sort")
	g.addImport("strconv")
	g.addImport("strings")
	g.addImport("time")
	if entry == nil {
		g.writeLine(`_tt_runEvals("evals", []_tt_evalCase{})`)
		return
	}
	type info struct {
		idx  int
		name string
	}
	var evals []info
	for _, d := range entry.File.Decls {
		ed, ok := d.(*ast.EvalDecl)
		if !ok {
			continue
		}
		evals = append(evals, info{idx: len(evals) + 1, name: ed.Name})
	}
	suite := entry.File.Path
	if suite == "" {
		suite = "evals"
	}
	var b strings.Builder
	b.WriteString("_tt_runEvals(")
	b.WriteString(strconv.Quote(suite))
	b.WriteString(", []_tt_evalCase{")
	for i, e := range evals {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString("{name: ")
		b.WriteString(strconv.Quote(e.name))
		b.WriteString(", fn: _tt_eval_" + itoa(e.idx) + "}")
	}
	b.WriteString("})")
	g.writeLine(b.String())
}

// runtimeEvalHarness defines the eval state, score/expect entry points,
// scoring helpers (jaccard / f1 / containsScore / exactMatch), and the
// `_tt_runEvals` runner that prints the per-eval scorecard. Emitted only
// in EmitEval mode (gated on usesRuntimeEvalState).
//
// Output shape (per eval):
//
//	eval "sentiment classification"
//	  ✓ exact_match  0.67  (mean of 3, threshold ≥ 0.50)
//	  · token_f1     0.71  (mean of 3)
//	  3 samples · 412ms
//
// Followed by an aggregate summary line.
const runtimeEvalHarness = `// _tt_evalState is the per-eval accumulator. score(label, v) appends v to
// the bucket keyed by label; expect(label, t) records a threshold gate
// evaluated at end-of-eval. Reset by _tt_evalReset() between cases.
type _tt_evalState struct {
	scores  map[string][]float64
	order   []string
	gates   map[string]float64
	gateSet map[string]bool
}

var _tt_eval = _tt_evalState{}

func _tt_evalReset() {
	_tt_eval = _tt_evalState{
		scores:  make(map[string][]float64),
		order:   nil,
		gates:   make(map[string]float64),
		gateSet: make(map[string]bool),
	}
}

func _tt_score(label string, v float64) {
	if _, ok := _tt_eval.scores[label]; !ok {
		_tt_eval.order = append(_tt_eval.order, label)
	}
	_tt_eval.scores[label] = append(_tt_eval.scores[label], v)
}

func _tt_expect(label string, threshold float64) {
	_tt_eval.gates[label] = threshold
	_tt_eval.gateSet[label] = true
	if _, ok := _tt_eval.scores[label]; !ok {
		_tt_eval.order = append(_tt_eval.order, label)
	}
}

// _tt_jaccard computes word-set Jaccard similarity. Tokenisation splits on
// any run of whitespace; comparison is case-sensitive — callers normalise
// with lower(...) when they want case-folded matching.
func _tt_jaccard(a, b string) float64 {
	A := _tt_tokSet(a)
	B := _tt_tokSet(b)
	if len(A) == 0 && len(B) == 0 {
		return 1.0
	}
	inter := 0
	for k := range A {
		if B[k] {
			inter++
		}
	}
	union := len(A) + len(B) - inter
	if union == 0 {
		return 0.0
	}
	return float64(inter) / float64(union)
}

// _tt_exactMatch returns 1.0 when a == b (byte-for-byte), else 0.0. Returned
// as a float so it composes cleanly with score/mean.
func _tt_exactMatch(a, b string) float64 {
	if a == b {
		return 1.0
	}
	return 0.0
}

// _tt_containsScore returns the fraction of terms present (as substrings) in
// text. An empty terms list scores 1.0 (vacuously satisfied).
func _tt_containsScore(text string, terms []string) float64 {
	if len(terms) == 0 {
		return 1.0
	}
	hit := 0
	for _, t := range terms {
		if t != "" && strings.Contains(text, t) {
			hit++
		}
	}
	return float64(hit) / float64(len(terms))
}

// _tt_f1Score is element-wise micro-F1 over two equal-length string arrays:
// each predicted[i] is compared against expected[i] at the token level (the
// classic precision/recall on unordered token sets), then averaged. Mismatched
// lengths scale by the longer side so a missing prediction counts as a miss.
func _tt_f1Score(predicted, expected []string) float64 {
	n := len(predicted)
	if len(expected) > n {
		n = len(expected)
	}
	if n == 0 {
		return 1.0
	}
	total := 0.0
	for i := 0; i < n; i++ {
		var p, e string
		if i < len(predicted) {
			p = predicted[i]
		}
		if i < len(expected) {
			e = expected[i]
		}
		total += _tt_f1Pair(p, e)
	}
	return total / float64(n)
}

func _tt_f1Pair(predicted, expected string) float64 {
	pred := _tt_tokSet(predicted)
	exp := _tt_tokSet(expected)
	if len(pred) == 0 && len(exp) == 0 {
		return 1.0
	}
	if len(pred) == 0 || len(exp) == 0 {
		return 0.0
	}
	tp := 0
	for k := range pred {
		if exp[k] {
			tp++
		}
	}
	if tp == 0 {
		return 0.0
	}
	prec := float64(tp) / float64(len(pred))
	rec := float64(tp) / float64(len(exp))
	return 2 * prec * rec / (prec + rec)
}

func _tt_tokSet(s string) map[string]bool {
	out := make(map[string]bool)
	for _, w := range strings.Fields(s) {
		if w != "" {
			out[w] = true
		}
	}
	return out
}

// _tt_f1Tokens is the single-pair token-level F1 used in SQuAD-style evals.
// It's a thin re-export of the per-pair helper so callers don't have to
// build a one-element array just to score one (pred, ref) pair.
func _tt_f1Tokens(predicted, expected string) float64 {
	return _tt_f1Pair(predicted, expected)
}

// _tt_levenshtein is the classic Levenshtein edit distance (insertion /
// deletion / substitution all cost 1). Implemented with two rolling rows so
// we use O(min(n, m)) memory; that matters when comparing long LLM outputs.
// Operates over UTF-8 runes — eval inputs may contain unicode and we don't
// want a single multi-byte character to count as multiple edits.
func _tt_levenshtein(a, b string) int64 {
	ar := []rune(a)
	br := []rune(b)
	if len(ar) < len(br) {
		ar, br = br, ar
	}
	if len(br) == 0 {
		return int64(len(ar))
	}
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := 0; j <= len(br); j++ {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			m := del
			if ins < m {
				m = ins
			}
			if sub < m {
				m = sub
			}
			curr[j] = m
		}
		prev, curr = curr, prev
	}
	return int64(prev[len(br)])
}

// _tt_levenshteinRatio normalizes Levenshtein into [0.0, 1.0] using the
// "1 - dist / max_len" form. Equal strings score 1.0; a string vs the empty
// string scores 0.0; intermediate similarity scales linearly with edits.
func _tt_levenshteinRatio(a, b string) float64 {
	la := len([]rune(a))
	lb := len([]rune(b))
	maxLen := la
	if lb > maxLen {
		maxLen = lb
	}
	if maxLen == 0 {
		return 1.0
	}
	d := _tt_levenshtein(a, b)
	return 1.0 - float64(d)/float64(maxLen)
}

// _tt_bleu computes sentence-level BLEU up to 4-grams with the standard
// brevity penalty and add-1 smoothing on each n-gram precision. Smoothing
// keeps the geometric mean defined when one of the n-gram precisions is
// zero (common on short hypotheses). Returns a value in [0.0, 1.0].
func _tt_bleu(hyp, ref string) float64 {
	h := strings.Fields(hyp)
	r := strings.Fields(ref)
	if len(h) == 0 || len(r) == 0 {
		return 0.0
	}
	N := 4
	if len(h) < N {
		N = len(h)
	}
	if N == 0 {
		return 0.0
	}
	logSum := 0.0
	for n := 1; n <= N; n++ {
		hCounts := _tt_ngramCounts(h, n)
		rCounts := _tt_ngramCounts(r, n)
		matched := 0
		total := 0
		for gram, cnt := range hCounts {
			total += cnt
			if rc, ok := rCounts[gram]; ok {
				if rc < cnt {
					matched += rc
				} else {
					matched += cnt
				}
			}
		}
		// Add-1 smoothing in numerator and denominator.
		p := float64(matched+1) / float64(total+1)
		if p <= 0 {
			return 0.0
		}
		logSum += math.Log(p)
	}
	geoMean := math.Exp(logSum / float64(N))
	bp := 1.0
	if len(h) < len(r) {
		bp = math.Exp(1.0 - float64(len(r))/float64(len(h)))
	}
	return bp * geoMean
}

func _tt_ngramCounts(toks []string, n int) map[string]int {
	out := make(map[string]int)
	if n <= 0 || len(toks) < n {
		return out
	}
	for i := 0; i+n <= len(toks); i++ {
		gram := strings.Join(toks[i:i+n], "\x1f")
		out[gram]++
	}
	return out
}

// _tt_rougeL is the ROUGE-L F-measure: F1 derived from the length of the
// longest common subsequence between the two token streams. Standard for
// summarization evals; insensitive to word order beyond the LCS.
func _tt_rougeL(hyp, ref string) float64 {
	h := strings.Fields(hyp)
	r := strings.Fields(ref)
	if len(h) == 0 || len(r) == 0 {
		return 0.0
	}
	prev := make([]int, len(r)+1)
	curr := make([]int, len(r)+1)
	for i := 1; i <= len(h); i++ {
		for j := 1; j <= len(r); j++ {
			if h[i-1] == r[j-1] {
				curr[j] = prev[j-1] + 1
			} else if prev[j] >= curr[j-1] {
				curr[j] = prev[j]
			} else {
				curr[j] = curr[j-1]
			}
		}
		prev, curr = curr, prev
	}
	lcs := prev[len(r)]
	if lcs == 0 {
		return 0.0
	}
	prec := float64(lcs) / float64(len(h))
	rec := float64(lcs) / float64(len(r))
	return 2 * prec * rec / (prec + rec)
}

// _tt_cosineSimilarity is the cosine of the angle between two equal-length
// float vectors. Mismatched lengths are treated as zero-padded (the longer
// vector contributes its trailing components to its own norm only). Returns
// 0.0 if either vector is all-zero.
func _tt_cosineSimilarity(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	dot := 0.0
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
	}
	na := 0.0
	for _, v := range a {
		na += v * v
	}
	nb := 0.0
	for _, v := range b {
		nb += v * v
	}
	if na == 0 || nb == 0 {
		return 0.0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

type _tt_evalCase struct {
	name string
	fn   func()
}

// _tt_aboveThreshold counts how many entries in xs meet >= t.
func _tt_aboveThreshold(xs []float64, t float64) int {
	n := 0
	for _, v := range xs {
		if v >= t {
			n++
		}
	}
	return n
}

func _tt_runEvals(suite string, evals []_tt_evalCase) {
	cPass, cFail, cSkip, cDim, cBold, cOff := _tt_colors()
	_ = cSkip
	fmt.Printf("%srunning %d eval(s) in %s%s\n\n", cDim, len(evals), suite, cOff)
	totalEvals := len(evals)
	passedEvals := 0
	failedEvals := 0
	for _, ec := range evals {
		_tt_evalReset()
		_tt_mock_reset()
		var panicMsg string
		t0 := time.Now()
		func() {
			defer func() {
				if r := recover(); r != nil {
					switch e := r.(type) {
					case _tt_testFailure:
						panicMsg = e.msg
					default:
						panicMsg = fmt.Sprintf("panic: %v", r)
					}
				}
			}()
			ec.fn()
		}()
		dur := time.Since(t0)

		// Compute per-label means and gate results.
		nSamples := 0
		anyGateFail := false
		labels := append([]string(nil), _tt_eval.order...)
		sort.SliceStable(labels, func(i, j int) bool {
			ai := _tt_eval.gateSet[labels[i]]
			aj := _tt_eval.gateSet[labels[j]]
			if ai != aj { return ai && !aj }
			return false
		})
		type row struct {
			label    string
			mean     float64
			n        int
			gated    bool
			thresh   float64
			pass     bool
			above    int
		}
		var rows []row
		for _, lab := range labels {
			vals := _tt_eval.scores[lab]
			if len(vals) > nSamples {
				nSamples = len(vals)
			}
			sum := 0.0
			for _, v := range vals {
				sum += v
			}
			var mean float64
			if len(vals) > 0 {
				mean = sum / float64(len(vals))
			}
			r := row{label: lab, mean: mean, n: len(vals)}
			if _tt_eval.gateSet[lab] {
				r.gated = true
				r.thresh = _tt_eval.gates[lab]
				r.pass = len(vals) > 0 && mean >= r.thresh
				r.above = _tt_aboveThreshold(vals, r.thresh)
				if !r.pass {
					anyGateFail = true
				}
			}
			rows = append(rows, r)
		}

		// Header line.
		statusOK := panicMsg == "" && !anyGateFail
		var hdr string
		if statusOK {
			hdr = fmt.Sprintf("%s%s✓%s eval %q", cBold, cPass, cOff, ec.name)
		} else {
			hdr = fmt.Sprintf("%s%s✗%s eval %q", cBold, cFail, cOff, ec.name)
		}
		fmt.Println(hdr)

		// Compute label-column width for alignment.
		w := 0
		for _, r := range rows {
			if len(r.label) > w {
				w = len(r.label)
			}
		}

		for _, r := range rows {
			pad := strings.Repeat(" ", w-len(r.label))
			meanStr := strconv.FormatFloat(r.mean, 'f', 2, 64)
			if !r.gated {
				suffix := ""
				if r.n > 1 {
					suffix = fmt.Sprintf("  %s(mean of %d)%s", cDim, r.n, cOff)
				} else if r.n == 0 {
					suffix = fmt.Sprintf("  %s(no samples)%s", cDim, cOff)
				}
				fmt.Printf("  %s·%s %s%s  %s%s\n", cDim, cOff, r.label, pad, meanStr, suffix)
				continue
			}
			tickC := cPass
			tick := "✓"
			if !r.pass {
				tickC = cFail
				tick = "✗"
			}
			thrStr := strconv.FormatFloat(r.thresh, 'f', 2, 64)
			detail := fmt.Sprintf("(%d/%d above %s, mean of %d)", r.above, r.n, thrStr, r.n)
			if r.n == 0 {
				detail = "(no samples)"
			}
			fmt.Printf("  %s%s%s %s%s  %s  %s%s%s\n",
				tickC, tick, cOff, r.label, pad, meanStr, cDim, detail, cOff)
		}

		if panicMsg != "" {
			for _, line := range strings.Split(panicMsg, "\n") {
				fmt.Printf("  %s%s%s\n", cFail, line, cOff)
			}
		}

		fmt.Printf("  %s%d sample(s) · %s%s\n\n", cDim, nSamples, dur.Round(time.Millisecond), cOff)

		if statusOK {
			passedEvals++
		} else {
			failedEvals++
		}
	}

	if failedEvals == 0 {
		fmt.Printf("%s%d passed%s (%d total)\n", cPass, passedEvals, cOff, totalEvals)
	} else {
		fmt.Printf("%s%d failed%s, %d passed (%d total)\n", cFail, failedEvals, cOff, passedEvals, totalEvals)
		os.Exit(1)
	}
}

`
