package nativegen_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func TestNativeUrlEncode(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	bin := build(t, `
		func main(): void {
			echo(urlEncode("a b/c?d&e=f"))
			echo(urlEncode(""))
			echo(urlEncode("plain"))
		}
	`)
	out := runBin(t, bin)
	want := "a%20b%2Fc%3Fd%26e%3Df\n\nplain\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

func TestNativeFetchTimeout(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	bin := build(t, `
		func main(): void {
			let r = fetchTimeout("`+srv.URL+`", 5)
			if r.ok { echo("ok=" + str(r.status)) } else { echo("fail") }
		}
	`)
	out := runBin(t, bin)
	if got := strings.TrimRight(out, "\n"); got != "ok=200" {
		t.Errorf("got %q", got)
	}
}

func TestNativePostJsonAndHeader(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	var seenMethod, seenCT, seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenMethod = r.Method
		seenCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		seenBody = string(b)
		w.Header().Set("X-Tartalo", "yes")
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	bin := build(t, `
		func main(): void {
			let r = postJson("`+srv.URL+`", "{\"a\":1}")
			echo(str(r.status))
			echo(header(r, "x-tartalo") ?? "(none)")
		}
	`)
	out := runBin(t, bin)
	want := "201\nyes\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
	if seenMethod != "POST" {
		t.Errorf("method: got %q", seenMethod)
	}
	if seenCT != "application/json" {
		t.Errorf("content-type: got %q", seenCT)
	}
	if seenBody != `{"a":1}` {
		t.Errorf("body: got %q", seenBody)
	}
}

func TestNativeRequestFull(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	var seenAuth, seenCustom, seenMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenCustom = r.Header.Get("X-Custom")
		seenMethod = r.Method
		w.WriteHeader(204)
	}))
	defer srv.Close()

	bin := build(t, `
		func main(): void {
			let r = request(Request{
				url: "`+srv.URL+`",
				method: "DELETE",
				headers: ["X-Custom: tartalo"],
				body: "",
				timeout: 5,
				followRedirects: true,
				insecure: false,
				user: "alice",
				password: "secret",
			})
			echo(str(r.status))
		}
	`)
	out := runBin(t, bin)
	if got := strings.TrimRight(out, "\n"); got != "204" {
		t.Errorf("got %q want 204", got)
	}
	if seenMethod != "DELETE" {
		t.Errorf("method: got %q", seenMethod)
	}
	if seenAuth != "Basic YWxpY2U6c2VjcmV0" {
		t.Errorf("authorization: got %q", seenAuth)
	}
	if seenCustom != "tartalo" {
		t.Errorf("X-Custom: got %q", seenCustom)
	}
}

func TestNativeRequestNoFollowRedirects(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			w.Header().Set("Location", "/end")
			w.WriteHeader(302)
			return
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	bin := build(t, `
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
	out := runBin(t, bin)
	if got := strings.TrimRight(out, "\n"); got != "302" {
		t.Errorf("got %q want 302", got)
	}
}
