package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestParseDotEnvValue(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"bar", "bar"},
		{"  bar  ", "bar"},
		{"bar # inline comment", "bar"},
		{"bar#nope", "bar#nope"},
		{`"hello world"`, "hello world"},
		{`"a\nb\tc"`, "a\nb\tc"},
		{`"escaped\\backslash"`, `escaped\backslash`},
		{`"unterminated`, `"unterminated`},
		{`'literal\nvalue'`, `literal\nvalue`},
		{`"quoted" # comment`, "quoted"},
		{"", ""},
	}
	for _, tc := range cases {
		got := parseDotEnvValue(tc.in)
		if got != tc.want {
			t.Errorf("parseDotEnvValue(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLoadDotEnv(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "main.tt")
	if err := os.WriteFile(src, []byte("// stub\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	envBody := "" +
		"# leading comment\n" +
		"\n" +
		"FOO=bar\n" +
		"  export QUOTED=\"hello world\"\n" +
		"SINGLE='raw\\n'\n" +
		"INLINE=spaced # trailing\n" +
		"EMPTY=\n" +
		"=novalue\n" +
		"PREEXISTING=should-not-win\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(envBody), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PREEXISTING", "kept")

	got, err := loadDotEnv(src)
	if err != nil {
		t.Fatalf("loadDotEnv: %v", err)
	}
	sort.Strings(got)
	want := []string{
		"EMPTY=",
		"FOO=bar",
		"INLINE=spaced",
		"QUOTED=hello world",
		`SINGLE=raw\n`,
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("loadDotEnv mismatch\n got:  %q\n want: %q", got, want)
	}
}

func TestLoadDotEnvMissing(t *testing.T) {
	dir := t.TempDir()
	got, err := loadDotEnv(filepath.Join(dir, "main.tt"))
	if err != nil {
		t.Fatalf("expected nil error for missing .env, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil result, got %v", got)
	}
}
