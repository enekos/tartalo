package ast

import "strings"

// IsXfailTestName reports whether the given test/eval name marks the test as
// expected-to-fail. A test that matches must actually fail at runtime;
// an unexpected pass fails the suite. The supported shapes mirror Zero's
// conventions:
//
//   - prefix "xfail:" — case-insensitive, optional surrounding whitespace
//   - prefix "expected fail:" — case-insensitive
//   - prefix "[xfail]" — anywhere in the leading run of non-alphanumeric chars
//
// Any name that does not start with one of these prefixes is treated as a
// normal test.
func IsXfailTestName(name string) bool {
	s := strings.TrimSpace(name)
	low := strings.ToLower(s)
	switch {
	case strings.HasPrefix(low, "xfail:"),
		strings.HasPrefix(low, "expected fail:"),
		strings.HasPrefix(low, "[xfail]"):
		return true
	}
	return false
}
