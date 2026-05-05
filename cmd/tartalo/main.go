// tartalo: a small statically-typed scripting language that compiles to sh.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/codegen"
	"github.com/enekos/tartalo/internal/loader"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "tartalo: "+err.Error())
		var ce *compileErrors
		if errors.As(err, &ce) {
			os.Exit(1)
		}
		os.Exit(2)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "build":
		return cmdBuild(rest)
	case "run":
		return cmdRun(rest)
	case "check":
		return cmdCheck(rest)
	case "test":
		return cmdTest(rest)
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	default:
		printUsage(os.Stderr)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, `usage:
  tartalo build <file.tt> [-o <out.sh>]
  tartalo run   <file.tt> [-- args...]
  tartalo test  <file.tt>             # run all `+"`test \"...\" { ... }`"+` declarations
  tartalo check <file.tt>             # type-check without emitting sh
  tartalo help`)
}

// compileErrors is a typed wrapper around lex/parse/check error lists so the
// `main` function can distinguish user-program errors from internal errors.
type compileErrors struct{ errs []error }

func (c *compileErrors) Error() string {
	parts := make([]string, len(c.errs))
	for i, e := range c.errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "\n")
}

// frontEnd runs lex, parse, import-resolution, and type-check. The returned
// modules are in topological order (deps before dependents) so the codegen
// can iterate them directly.
func frontEnd(path string) ([]*loader.Module, *checker.TypeInfo, error) {
	modules, lerrs := loader.Load(path)
	if len(lerrs) > 0 {
		return nil, nil, &compileErrors{errs: lerrs}
	}
	info, cerrs := checker.New().Check(modules)
	if len(cerrs) > 0 {
		return nil, nil, &compileErrors{errs: cerrs}
	}
	return modules, info, nil
}

func compileFile(path string) (string, error) {
	modules, info, err := frontEnd(path)
	if err != nil {
		return "", err
	}
	return codegen.New(info).EmitModules(modules), nil
}

// compileFileForTest compiles in test mode: the resulting script runs every
// `test "..."` declaration in the entry module instead of invoking main.
func compileFileForTest(path string) (string, error) {
	modules, info, err := frontEnd(path)
	if err != nil {
		return "", err
	}
	return codegen.New(info).EmitModulesTest(modules), nil
}

func cmdBuild(args []string) error {
	var (
		input   string
		out     string
		hadFlag bool
	)
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-o" || a == "--output":
			if i+1 >= len(args) {
				return fmt.Errorf("build: %s requires a value", a)
			}
			out = args[i+1]
			i++
			hadFlag = true
		case strings.HasPrefix(a, "-o="):
			out = strings.TrimPrefix(a, "-o=")
			hadFlag = true
		case strings.HasPrefix(a, "--output="):
			out = strings.TrimPrefix(a, "--output=")
			hadFlag = true
		case a == "-h" || a == "--help":
			fmt.Println("usage: tartalo build <file.tt> [-o <out.sh>]")
			return nil
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("build: unknown flag %q", a)
		default:
			if input != "" {
				return fmt.Errorf("build: expected exactly one input file")
			}
			input = a
		}
	}
	_ = hadFlag
	if input == "" {
		return fmt.Errorf("build: expected an input file")
	}
	sh, err := compileFile(input)
	if err != nil {
		return err
	}
	target := out
	if target == "" {
		target = strings.TrimSuffix(input, filepath.Ext(input)) + ".sh"
	}
	if err := os.WriteFile(target, []byte(sh), 0o755); err != nil {
		return err
	}
	return nil
}

func cmdCheck(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("check: expected at least one input file")
	}
	var combined []error
	for _, in := range args {
		if strings.HasPrefix(in, "-") {
			return fmt.Errorf("check: unknown flag %q", in)
		}
		if _, _, err := frontEnd(in); err != nil {
			var ce *compileErrors
			if errors.As(err, &ce) {
				combined = append(combined, ce.errs...)
			} else {
				combined = append(combined, err)
			}
		}
	}
	if len(combined) > 0 {
		return &compileErrors{errs: combined}
	}
	return nil
}

func cmdTest(args []string) error {
	var input string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Println("usage: tartalo test <file.tt>")
			return nil
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("test: unknown flag %q", a)
		default:
			if input != "" {
				return fmt.Errorf("test: expected exactly one input file")
			}
			input = a
		}
	}
	if input == "" {
		return fmt.Errorf("test: expected an input file")
	}
	sh, err := compileFileForTest(input)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "tartalo-test-*.sh")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(sh); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	cmd := exec.Command("/bin/sh", tmp.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}

func cmdRun(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("run: expected an input file")
	}
	in := args[0]
	scriptArgs := args[1:]
	if len(scriptArgs) > 0 && scriptArgs[0] == "--" {
		scriptArgs = scriptArgs[1:]
	}
	sh, err := compileFile(in)
	if err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "tartalo-*.sh")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(sh); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	cmd := exec.Command("/bin/sh", append([]string{tmp.Name()}, scriptArgs...)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			os.Exit(ee.ExitCode())
		}
		return err
	}
	return nil
}
