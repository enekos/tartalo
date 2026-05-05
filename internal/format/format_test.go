package format

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/codegen"
	"github.com/enekos/tartalo/internal/loader"
)

// TestIdempotent: format(format(x)) == format(x) for every example file.
// This is the strongest practical check that the formatter has converged on a
// fixed point — if it didn't, every save in an editor would dirty the file.
func TestIdempotent(t *testing.T) {
	for _, path := range exampleFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			once, err := Source(filepath.Base(path), string(src))
			if err != nil {
				t.Fatalf("first format failed: %v", err)
			}
			twice, err := Source(filepath.Base(path), once)
			if err != nil {
				t.Fatalf("second format failed: %v", err)
			}
			if once != twice {
				t.Errorf("formatter is not idempotent.\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
			}
		})
	}
}

// TestSemanticEquivalence: formatting must not change observable program
// behaviour. We rewrite the source in place inside a copy of the example
// directory and compare the generated shell script before vs after. Anything
// other than identical output indicates the formatter changed program
// semantics, which is a hard correctness bug.
func TestSemanticEquivalence(t *testing.T) {
	for _, path := range exampleFiles(t) {
		t.Run(filepath.Base(path), func(t *testing.T) {
			before, err := compileToSh(path)
			if err != nil {
				t.Skipf("source did not compile cleanly even before formatting: %v", err)
			}

			// Copy the entire examples tree to a temp dir so we can rewrite the
			// target file in place without losing its sibling import deps.
			root, err := filepath.Abs("../../examples")
			if err != nil {
				t.Fatal(err)
			}
			work := copyTree(t, root)
			rel, err := filepath.Rel(root, path)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(work, rel)

			src, err := os.ReadFile(target)
			if err != nil {
				t.Fatal(err)
			}
			formatted, err := Source(filepath.Base(target), string(src))
			if err != nil {
				t.Fatalf("format failed: %v", err)
			}
			if err := os.WriteFile(target, []byte(formatted), 0o644); err != nil {
				t.Fatal(err)
			}
			after, err := compileToSh(target)
			if err != nil {
				t.Fatalf("formatted source no longer compiles: %v\n--- formatted ---\n%s", err, formatted)
			}
			if before != after {
				t.Errorf("formatted source compiles to a different shell script.\n--- before len=%d ---\n--- after len=%d ---", len(before), len(after))
			}
		})
	}
}

// TestFixtures: a curated list of input/expected pairs, covering specific
// formatting decisions (operator spacing, parens around precedence groups,
// etc.). Easier to debug than the holistic example tests.
func TestFixtures(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "operator spacing",
			in:   "func main():void{let a=1+2*3\nlet b=(1+2)*3\necho(str(a))\necho(str(b))}",
			want: `func main(): void {
  let a = 1 + 2 * 3
  let b = (1 + 2) * 3
  echo(str(a))
  echo(str(b))
}
`,
		},
		{
			name: "preserves left-assoc parens on right",
			in:   "func main():void{let a=10-(3-1)\necho(str(a))}",
			want: `func main(): void {
  let a = 10 - (3 - 1)
  echo(str(a))
}
`,
		},
		{
			name: "comment preservation",
			in: `// header comment
func main(): void {
  // inside main
  let x = 1 // trailing
  echo(str(x))
}
`,
			want: `// header comment
func main(): void {
  // inside main
  let x = 1  // trailing
  echo(str(x))
}
`,
		},
		{
			name: "blank line between decls preserved",
			in: `func a():void{echo("a")}


func b():void{echo("b")}
`,
			want: `func a(): void {
  echo("a")
}

func b(): void {
  echo("b")
}
`,
		},
		{
			name: "import normalised",
			in:   "import   {  a  ,b ,c   }   from    \"./mod.tt\"\nfunc main():void{echo(\"\")}",
			want: `import { a, b, c } from "./mod.tt"

func main(): void {
  echo("")
}
`,
		},
		{
			name: "string escapes round-trip",
			in:   `func main(): void { echo("a\tb\n${1+1}\$\\") }`,
			want: `func main(): void {
  echo("a\tb\n${1 + 1}\$\\")
}
`,
		},
		{
			name: "match arms inline",
			in: `func main(): void {
  let s = "x"
  match s {
    "a" | "b" => echo("ab")
    _ => echo("other")
  }
}
`,
			want: `func main(): void {
  let s = "x"
  match s {
    "a" | "b" => echo("ab")
    _ => echo("other")
  }
}
`,
		},
		{
			name: "record literal inline preserved",
			in: `type Tag = { name: string, value: string }
func main(): void {
  let t = Tag{name: "n", value: "v"}
  echo(t.name)
}
`,
			want: `type Tag = {
  name: string,
  value: string,
}

func main(): void {
  let t = Tag{name: "n", value: "v"}
  echo(t.name)
}
`,
		},
		{
			name: "record literal multiline preserved",
			in: `type T = { a: number, b: number }
func main(): void {
  let r = T{
    a: 1,
    b: 2,
  }
  echo(str(r.a))
}
`,
			want: `type T = {
  a: number,
  b: number,
}

func main(): void {
  let r = T{
    a: 1,
    b: 2,
  }
  echo(str(r.a))
}
`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Source("test.tt", tc.in)
			if err != nil {
				t.Fatalf("format error: %v", err)
			}
			if got != tc.want {
				t.Errorf("mismatch.\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}

// --- helpers ----------------------------------------------------------------

func exampleFiles(t *testing.T) []string {
	t.Helper()
	root, err := filepath.Abs("../../examples")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		if filepath.Ext(path) == ".tt" {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("no example files under %s", root)
	}
	return out
}

func compileToSh(path string) (string, error) {
	mods, errs := loader.Load(path)
	if len(errs) > 0 {
		return "", errs[0]
	}
	info, cerrs := checker.New().Check(mods)
	if len(cerrs) > 0 {
		return "", cerrs[0]
	}
	return codegen.New(info).EmitModules(mods), nil
}

func writeTemp(t *testing.T, content, ext string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "fmt-*"+ext)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

// copyTree clones src into a new t.TempDir() subdir so a test can mutate files
// without affecting the source repo. Required because semantic-equivalence
// tests rewrite imports' siblings.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(out, data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}
