package codegen_test

import (
	"io"
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

func TestFetchRequestRedeclareErrors(t *testing.T) {
	src := `
		type Request = { foo: string }
		func main(): void {}
	`
	errs := checkOnly(t, src)
	if len(errs) == 0 || !containsErr(errs, `redeclare predeclared type "Request"`) {
		t.Fatalf("expected redeclaration error, got: %v", errs)
	}
}

func TestUrlEncode(t *testing.T) {
	sh := compile(t, `
		func main(): void {
			echo(urlEncode("a b/c?d&e=f"))
			echo(urlEncode("hello world!"))
			echo(urlEncode(""))
			echo(urlEncode("plain"))
			echo(urlEncode("ABCabc012-._~"))
		}
	`)
	out := runShell(t, sh)
	want := "a%20b%2Fc%3Fd%26e%3Df\nhello%20world%21\n\nplain\nABCabc012-._~\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestHeaderLookup(t *testing.T) {
	if !haveCurl(t) {
		t.Skip("curl not available")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Custom", "tartalo")
		w.Header().Set("X-Other", " spaces ")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sh := compile(t, `
		func main(): void {
			let r = fetch("`+srv.URL+`")
			echo(header(r, "X-Custom") ?? "(none)")
			echo(header(r, "x-custom") ?? "(none)")
			echo(header(r, "X-Other") ?? "(none)")
			echo(header(r, "X-Missing") ?? "(none)")
		}
	`)
	out := runShell(t, sh)
	want := "tartalo\ntartalo\nspaces\n(none)\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestFetchTimeout(t *testing.T) {
	if !haveCurl(t) {
		t.Skip("curl not available")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	sh := compile(t, `
		func main(): void {
			let r = fetchTimeout("`+srv.URL+`", 10)
			if r.ok { echo("ok=" + str(r.status)) } else { echo("fail") }
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "ok=200" {
		t.Errorf("got %q", got)
	}
}

func TestFetchHeaders(t *testing.T) {
	if !haveCurl(t) {
		t.Skip("curl not available")
	}
	var seenAuth, seenCustom string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenCustom = r.Header.Get("X-Custom")
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sh := compile(t, `
		func main(): void {
			let r = fetchHeaders("`+srv.URL+`", ["Authorization: Bearer xyz", "X-Custom: tartalo"])
			echo(str(r.status))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "200" {
		t.Errorf("status: got %q", got)
	}
	if seenAuth != "Bearer xyz" {
		t.Errorf("Authorization: got %q want %q", seenAuth, "Bearer xyz")
	}
	if seenCustom != "tartalo" {
		t.Errorf("X-Custom: got %q want %q", seenCustom, "tartalo")
	}
}

func TestPostJson(t *testing.T) {
	if !haveCurl(t) {
		t.Skip("curl not available")
	}
	var seenMethod, seenCT, seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		seenBody = string(b)
		w.WriteHeader(201)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	sh := compile(t, `
		func main(): void {
			let r = postJson("`+srv.URL+`", "{\"name\":\"tartalo\"}")
			echo(str(r.status))
			echo(r.body)
		}
	`)
	out := runShell(t, sh)
	want := "201\n{\"ok\":true}\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
	if seenMethod != "POST" {
		t.Errorf("method: got %q", seenMethod)
	}
	if seenCT != "application/json" {
		t.Errorf("content-type: got %q", seenCT)
	}
	if seenBody != `{"name":"tartalo"}` {
		t.Errorf("body: got %q", seenBody)
	}
}

func TestPostForm(t *testing.T) {
	if !haveCurl(t) {
		t.Skip("curl not available")
	}
	var seenCT, seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		seenBody = string(b)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	sh := compile(t, `
		func main(): void {
			let r = postForm("`+srv.URL+`", "a=1&b=two")
			echo(str(r.status))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "200" {
		t.Errorf("status: got %q", got)
	}
	if seenCT != "application/x-www-form-urlencoded" {
		t.Errorf("content-type: got %q", seenCT)
	}
	if seenBody != "a=1&b=two" {
		t.Errorf("body: got %q", seenBody)
	}
}

func TestRequestPutBody(t *testing.T) {
	if !haveCurl(t) {
		t.Skip("curl not available")
	}
	var seenMethod, seenBody, seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		seenBody = string(b)
		w.WriteHeader(204)
	}))
	defer srv.Close()

	sh := compile(t, `
		func main(): void {
			let r = request(Request{
				url: "`+srv.URL+`",
				method: "PUT",
				headers: ["X-Tartalo: 1"],
				body: "payload",
				timeout: 5,
				followRedirects: true,
				insecure: false,
				user: "alice",
				password: "secret",
			})
			echo(str(r.status))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "204" {
		t.Errorf("status: got %q", got)
	}
	if seenMethod != "PUT" {
		t.Errorf("method: got %q", seenMethod)
	}
	if seenBody != "payload" {
		t.Errorf("body: got %q", seenBody)
	}
	// curl encodes basic auth as base64(user:password); alice:secret = YWxpY2U6c2VjcmV0
	if !strings.HasPrefix(seenAuth, "Basic ") || seenAuth != "Basic YWxpY2U6c2VjcmV0" {
		t.Errorf("authorization: got %q", seenAuth)
	}
}

func TestRequestNoFollowRedirects(t *testing.T) {
	if !haveCurl(t) {
		t.Skip("curl not available")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			w.Header().Set("Location", "/end")
			w.WriteHeader(302)
			return
		}
		w.WriteHeader(200)
		w.Write([]byte("end"))
	}))
	defer srv.Close()

	sh := compile(t, `
		func main(): void {
			let empty: string[] = []
			let r = request(Request{
				url: "`+srv.URL+`/start",
				method: "GET",
				headers: empty,
				body: "",
				timeout: 5,
				followRedirects: false,
				insecure: false,
				user: "",
				password: "",
			})
			echo(str(r.status))
		}
	`)
	out := runShell(t, sh)
	if got := strings.TrimRight(out, "\n"); got != "302" {
		t.Errorf("got %q want 302", got)
	}
}

func haveCurl(t *testing.T) bool {
	t.Helper()
	_, err := exec.LookPath("curl")
	return err == nil
}
