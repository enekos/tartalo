package nativegen

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/loader"
)

// BuildOptions controls a native compile.
type BuildOptions struct {
	// Output is the path of the binary to produce.
	Output string

	// GOOS / GOARCH override the host platform. Empty strings mean "host".
	GOOS   string
	GOARCH string

	// KeepTemp leaves the staging directory in place for inspection.
	KeepTemp bool
}

// ErrGoMissing is returned when the `go` toolchain isn't on PATH.
var ErrGoMissing = errors.New("go toolchain not found on PATH; install Go from https://go.dev/dl to use --target=native")

// Build emits a native binary from the supplied modules. It writes the
// generated Go source into a temp directory, runs `go build` there, and
// moves the resulting binary into place at opts.Output.
func Build(modules []*loader.Module, info *checker.TypeInfo, opts BuildOptions) error {
	return buildWithMode(modules, info, opts, EmitRun)
}

// BuildTest is the test-mode counterpart of Build: the resulting binary
// runs every `test "..." { ... }` declaration in the entry module via the
// runtime test harness and exits non-zero if any test fails.
func BuildTest(modules []*loader.Module, info *checker.TypeInfo, opts BuildOptions) error {
	return buildWithMode(modules, info, opts, EmitTest)
}

func buildWithMode(modules []*loader.Module, info *checker.TypeInfo, opts BuildOptions, mode EmitMode) error {
	if opts.Output == "" {
		return errors.New("nativegen: BuildOptions.Output is required")
	}
	if _, err := exec.LookPath("go"); err != nil {
		return ErrGoMissing
	}

	g := New(info)
	var src string
	if mode == EmitTest {
		src = g.EmitModulesTest(modules)
	} else {
		src = g.EmitModules(modules)
	}

	stage, err := os.MkdirTemp("", "tartalo-native-*")
	if err != nil {
		return fmt.Errorf("nativegen: create temp dir: %w", err)
	}
	if !opts.KeepTemp {
		defer os.RemoveAll(stage)
	}

	mainPath := filepath.Join(stage, "main.go")
	if err := os.WriteFile(mainPath, []byte(src), 0o644); err != nil {
		return fmt.Errorf("nativegen: write main.go: %w", err)
	}

	// Minimal go.mod so `go build` doesn't reach for the user's GOPATH or
	// surrounding workspace. No dependencies — the runtime helpers are
	// inlined in main.go.
	goMod := "module tartalo_native\n\ngo 1.21\n"
	if err := os.WriteFile(filepath.Join(stage, "go.mod"), []byte(goMod), 0o644); err != nil {
		return fmt.Errorf("nativegen: write go.mod: %w", err)
	}

	// Resolve the absolute output path before running `go build`, since
	// the build runs with cwd == stage.
	absOut, err := filepath.Abs(opts.Output)
	if err != nil {
		return fmt.Errorf("nativegen: resolve output path: %w", err)
	}

	cmd := exec.Command("go", "build", "-trimpath", "-o", absOut, ".")
	cmd.Dir = stage
	cmd.Env = append(os.Environ(),
		// Prevent the user's module cache from being consulted; we have no
		// dependencies. (GOFLAGS may be set by the user; we add to it.)
		"GOFLAGS=",
	)
	if opts.GOOS != "" {
		cmd.Env = append(cmd.Env, "GOOS="+opts.GOOS)
	}
	if opts.GOARCH != "" {
		cmd.Env = append(cmd.Env, "GOARCH="+opts.GOARCH)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		// Surface the generated source path in the error so the user can
		// inspect what we tried to compile when something goes wrong.
		if opts.KeepTemp {
			return fmt.Errorf("go build failed (sources kept in %s):\n%s\n%w",
				stage, string(out), err)
		}
		return fmt.Errorf("go build failed:\n%s\n%w", string(out), err)
	}
	return nil
}

// EmitSource is a debugging helper: it returns the Go source nativegen
// would compile, without actually invoking `go build`.
func EmitSource(modules []*loader.Module, info *checker.TypeInfo) string {
	return New(info).EmitModules(modules)
}

// EmitSourceTest is the test-mode counterpart of EmitSource.
func EmitSourceTest(modules []*loader.Module, info *checker.TypeInfo) string {
	return New(info).EmitModulesTest(modules)
}
