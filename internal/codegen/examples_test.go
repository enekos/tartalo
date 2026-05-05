package codegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/codegen"
	"github.com/enekos/tartalo/internal/loader"
)

// TestExamplesCompileAndRun is a regression net: every .tt file under
// examples/ must (a) compile cleanly, (b) compile to identical sh under
// every supported shell, (c) run to completion under both bash and dash on
// systems where dash is available.
//
// Examples that need environment, network, or specific filesystem state set
// up the right hooks below.
func TestExamplesCompileAndRun(t *testing.T) {
	repoRoot := findRepoRoot(t)
	examplesDir := filepath.Join(repoRoot, "examples")

	cases := []struct {
		entry string // path relative to examples/
		env   []string
		// substr is a fragment of stdout we expect to see; empty string means
		// we only assert the script exits 0.
		substr string
		skip   string // non-empty to skip with this reason
	}{
		{entry: "hello.tt", substr: "Hello, world!"},
		{entry: "fizzbuzz.tt", substr: "FizzBuzz"},
		{entry: "sum.tt", substr: "sum(1..10) = 55"},
		{entry: "lines.tt", substr: ""},
		{entry: "match.tt", env: []string{"ACTION=build"}, substr: "compiling"},
		{entry: "array.tt", substr: "alice"},
		{entry: "record.tt", substr: "coffee (350¢, available)"},
		{entry: "strings.tt", substr: "HELLO, WORLD!"},
		{entry: "git-summary.tt", substr: ""}, // depends on git; just assert no crash
		{entry: "files.tt", env: []string{"DIR=" + examplesDir}, substr: "total:"},
		{entry: "config.tt", substr: "host:"},
		{entry: "fetch.tt", skip: "network-dependent"},
		{entry: "api.tt", skip: "network-dependent"},
		{entry: "stats.tt", skip: "needs args/stdin; covered by unit tests"},
		{entry: "modules/main.tt", substr: "env=prod"},
	}

	shells := []string{"/bin/sh"}
	if _, err := exec.LookPath("dash"); err == nil {
		shells = append(shells, "dash")
	}
	if _, err := exec.LookPath("bash"); err == nil {
		shells = append(shells, "bash")
	}

	for _, tc := range cases {
		tc := tc
		t.Run(strings.ReplaceAll(tc.entry, "/", "_"), func(t *testing.T) {
			if tc.skip != "" {
				t.Skip(tc.skip)
			}
			path := filepath.Join(examplesDir, tc.entry)
			modules, errs := loader.Load(path)
			if len(errs) > 0 {
				t.Fatalf("loader: %v", errs)
			}
			info, errs := checker.New().Check(modules)
			if len(errs) > 0 {
				t.Fatalf("checker: %v", errs)
			}
			sh := codegen.New(info).EmitModules(modules)

			tmp := t.TempDir()
			scriptPath := filepath.Join(tmp, "out.sh")
			if err := os.WriteFile(scriptPath, []byte(sh), 0o755); err != nil {
				t.Fatal(err)
			}

			for _, shell := range shells {
				shell := shell
				t.Run(filepath.Base(shell), func(t *testing.T) {
					cmd := exec.Command(shell, scriptPath)
					if tc.env != nil {
						cmd.Env = append(os.Environ(), tc.env...)
					}
					out, err := cmd.CombinedOutput()
					if err != nil {
						t.Fatalf("%s exited %v\n--script--\n%s\n--out--\n%s",
							shell, err, sh, out)
					}
					if tc.substr != "" && !strings.Contains(string(out), tc.substr) {
						t.Errorf("expected %q in output, got:\n%s", tc.substr, out)
					}
				})
			}
		})
	}
}

// findRepoRoot walks up from the current working directory until it finds the
// `examples` directory.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "examples")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find examples/ from %s", dir)
		}
		dir = parent
	}
}

// TestPlatform documents the shells we ran against.
func TestPlatform(t *testing.T) {
	t.Logf("runtime: %s/%s", runtime.GOOS, runtime.GOARCH)
}
