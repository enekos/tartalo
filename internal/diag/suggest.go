package diag

import "strings"

// Suggest picks the candidate from candidates that is closest to want by
// edit distance, subject to a small sanity threshold so unrelated names
// don't get suggested. Returns "" when no candidate is close enough.
//
// We bias the threshold toward the *shorter* of want and candidate so
// 1-char names like `x` don't get a "did you mean `y`?" by virtue of a
// single edit. For very short names (<3 chars) only case-insensitive exact
// matches qualify; longer names allow ~1 edit per 3 characters.
func Suggest(want string, candidates []string) string {
	if want == "" || len(candidates) == 0 {
		return ""
	}
	best := ""
	bestDist := -1
	for _, c := range candidates {
		if c == "" || c == want {
			continue
		}
		// Cheap pre-filter: case-insensitive equality is always a great
		// suggestion regardless of length difference.
		if strings.EqualFold(c, want) {
			return c
		}
		minLen := len(want)
		if len(c) < minLen {
			minLen = len(c)
		}
		// Below 3 chars there's not enough signal to distinguish a typo
		// from an unrelated name.
		if minLen < 3 {
			continue
		}
		threshold := minLen / 3
		if threshold < 1 {
			threshold = 1
		}
		// Skip candidates whose lengths differ too much to ever be within
		// threshold — saves the DP allocation.
		if abs(len(c)-len(want)) > threshold {
			continue
		}
		d := levenshtein(want, c)
		if d > threshold {
			continue
		}
		if bestDist < 0 || d < bestDist {
			bestDist = d
			best = c
		}
	}
	return best
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// levenshtein is the standard two-row DP. Cheap enough at compiler scale.
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := curr[j-1] + 1
			sub := prev[j-1] + cost
			curr[j] = min3(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
