package codegen_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadWriteFileRoundtrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "hello.txt")
	src := `
		func main(): void {
			writeFile("` + p + `", "first line\nsecond line")
			echo(readFile("` + p + `"))
		}
	`
	sh := compile(t, src)
	out := runShell(t, sh)
	want := "first line\nsecond line\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestAppendFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "log.txt")
	src := `
		func main(): void {
			let p = "` + p + `"
			writeFile(p, "a\n")
			appendFile(p, "b\n")
			appendFile(p, "c")
			echo(readFile(p))
		}
	`
	sh := compile(t, src)
	out := runShell(t, sh)
	if out != "a\nb\nc\n" {
		t.Errorf("got %q", out)
	}
}

func TestExistsIsFileIsDir(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `
		func main(): void {
			let dir = "` + dir + `"
			let f = "` + file + `"
			let nope = "` + filepath.Join(dir, "nope") + `"

			if exists(f) { echo("f-exists") }
			if isFile(f) { echo("f-is-file") }
			if !isDir(f) { echo("f-not-dir") }

			if exists(dir) { echo("d-exists") }
			if !isFile(dir) { echo("d-not-file") }
			if isDir(dir) { echo("d-is-dir") }

			if !exists(nope) { echo("nope-not-exists") }
		}
	`
	sh := compile(t, src)
	out := runShell(t, sh)
	want := "f-exists\nf-is-file\nf-not-dir\nd-exists\nd-not-file\nd-is-dir\nnope-not-exists\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestListDir(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	src := `
		func main(): void {
			let entries = listDir("` + dir + `")
			echo(str(len(entries)))
			for e in entries { echo("- " + e) }
		}
	`
	sh := compile(t, src)
	out := runShell(t, sh)
	// ls -1 sorts lexicographically
	want := "3\n- a.txt\n- b.txt\n- c.txt\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestMkdirRemoveFile(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "nested", "child")
	src := `
		func main(): void {
			mkdir("` + sub + `")
			if isDir("` + sub + `") { echo("created") }
			let f = "` + filepath.Join(sub, "x") + `"
			writeFile(f, "hi")
			if isFile(f) { echo("wrote") }
			removeFile(f)
			if !exists(f) { echo("removed") }
			// removeFile is idempotent — call again, no error.
			removeFile(f)
			echo("done")
		}
	`
	sh := compile(t, src)
	out := runShell(t, sh)
	want := "created\nwrote\nremoved\ndone\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestPathJoin(t *testing.T) {
	src := `
		func main(): void {
			echo(pathJoin("/a/b", "c"))
			echo(pathJoin("/a/b/", "c"))
			echo(pathJoin("a", "b"))
			echo(pathJoin("/x/y", "/abs"))
			echo(pathJoin("", "rel"))
		}
	`
	sh := compile(t, src)
	out := runShell(t, sh)
	want := "/a/b/c\n/a/b/c\na/b\n/abs\n/rel\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestBasenameDirnameExtname(t *testing.T) {
	src := `
		func main(): void {
			echo(basename("/a/b/c.txt"))
			echo(dirname("/a/b/c.txt"))
			echo(extname("/a/b/c.txt"))
			echo("[" + extname("/a/b/c") + "]")
			echo("[" + extname(".hidden") + "]")
			echo(extname("archive.tar.gz"))
		}
	`
	sh := compile(t, src)
	out := runShell(t, sh)
	want := "c.txt\n/a/b\n.txt\n[]\n[.hidden]\n.gz\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestEmptyIfBranches(t *testing.T) {
	// Regression: an if (or else) with no statements used to emit invalid sh.
	sh := compile(t, `
		func main(): void {
			let x = 1
			if x > 0 {
				// intentionally empty
			}
			if x < 0 {
				echo("nope")
			} else {
				// also empty
			}
			echo("ok")
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "ok" {
		t.Errorf("got %q", got)
	}
}

func TestNumOnVariable(t *testing.T) {
	// Regression: num() of a variable used to wrap the value in `"..."` inside
	// $((...)), which sh rejects as a syntax error.
	sh := compile(t, `
		func main(): void {
			let s = "392"
			let n = num(s)
			echo(str(n + 8))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "400" {
		t.Errorf("got %q", got)
	}
}

func TestReadFileMissingAborts(t *testing.T) {
	// readFile of a non-existent path must abort the script with a non-zero
	// exit. We can't use runShell (which fatals on non-zero) so we shell out
	// directly.
	missing := filepath.Join(t.TempDir(), "definitely-does-not-exist")
	src := `
		func main(): void {
			echo(readFile("` + missing + `"))
			echo("should not print")
		}
	`
	sh := compile(t, src)
	dir := t.TempDir()
	path := filepath.Join(dir, "out.sh")
	if err := os.WriteFile(path, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exitStatus(path)
	if err == nil {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(out, "tartalo: readFile: cannot read") {
		t.Errorf("expected diagnostic, got %q", out)
	}
	if strings.Contains(out, "should not print") {
		t.Error("script continued past failed readFile")
	}
}

// exitStatus runs `sh path` and returns combined output + an error iff the
// script exited non-zero. Used for negative tests where the script is
// supposed to abort.
func exitStatus(path string) (string, error) {
	cmd := exec.Command("/bin/sh", path)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
