package ast

import "testing"

func TestIsXfailTestName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// Recognised markers.
		{"xfail: bad math", true},
		{"XFAIL: bad math", true},
		{"xfail:bad math", true},
		{"expected fail: known-broken", true},
		{"Expected Fail: known-broken", true},
		{"[xfail] still wip", true},

		// Whitespace tolerated.
		{"  xfail: leading spaces", true},

		// Looks similar but not a marker.
		{"xfail (no colon)", false},
		{"expected", false},
		{"failing xfail: not at start", false},
		{"plain test name", false},
		{"", false},
	}
	for _, tc := range cases {
		got := IsXfailTestName(tc.name)
		if got != tc.want {
			t.Errorf("IsXfailTestName(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
