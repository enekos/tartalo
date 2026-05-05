package codegen_test

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func TestFetchOk(t *testing.T) {
	if !haveCurl(t) {
		t.Skip("curl not available")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Tartalo-Test", "yes")
		w.WriteHeader(200)
		w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	src := `
		func main(): void {
			let r = fetch("` + srv.URL + `")
			if r.ok {
				echo("ok=" + str(r.status))
				echo("body=" + r.body)
			} else {
				echo("fail=" + str(r.status))
			}
		}
	`
	sh := compile(t, src)
	out := runShell(t, sh)
	want := "ok=200\nbody=hello world\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestFetch404(t *testing.T) {
	if !haveCurl(t) {
		t.Skip("curl not available")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte("missing"))
	}))
	defer srv.Close()

	sh := compile(t, `
		func main(): void {
			let r = fetch("`+srv.URL+`")
			if r.ok { echo("ok") } else { echo("not ok: " + str(r.status)) }
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "not ok: 404" {
		t.Errorf("got %q", got)
	}
}

func TestFetchNetworkError(t *testing.T) {
	if !haveCurl(t) {
		t.Skip("curl not available")
	}
	// 127.0.0.1:1 should always reject connections.
	sh := compile(t, `
		func main(): void {
			let r = fetch("http://127.0.0.1:1/nope")
			echo(str(r.status))
			if r.ok { echo("yes") } else { echo("no") }
		}
	`)
	out := runShell(t, sh)
	want := "0\nno\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestFetchHeadersField(t *testing.T) {
	if !haveCurl(t) {
		t.Skip("curl not available")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "tartalo")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sh := compile(t, `
		func main(): void {
			let r = fetch("`+srv.URL+`")
			// raw headers blob includes the status line and each header on its own line
			echo(r.headers)
		}
	`)
	out := runShell(t, sh)
	if !strings.Contains(out, "X-Custom: tartalo") {
		t.Errorf("expected X-Custom header in output:\n%s", out)
	}
}

func TestFetchResponseRedeclareErrors(t *testing.T) {
	src := `
		type Response = { foo: string }
		func main(): void {}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, `redeclare predeclared type "Response"`) {
		t.Fatalf("expected redeclaration error, got: %v", errs)
	}
}

func haveCurl(t *testing.T) bool {
	t.Helper()
	_, err := exec.LookPath("curl")
	return err == nil
}
