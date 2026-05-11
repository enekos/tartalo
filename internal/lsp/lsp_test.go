package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
)

// frame builds a Content-Length-framed LSP message from a JSON payload.
func frame(payload []byte) []byte {
	return []byte(fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload))
}

// readFrames reads all complete LSP frames out of a stream until EOF.
func readFrames(r *bufio.Reader) ([][]byte, error) {
	var out [][]byte
	for {
		var contentLength int
		gotHeader := false
		for {
			line, err := r.ReadString('\n')
			if err == io.EOF {
				if gotHeader {
					return out, fmt.Errorf("EOF after header but no body")
				}
				return out, nil
			}
			if err != nil {
				return nil, err
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				if gotHeader {
					break
				}
				continue
			}
			if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
				n, err := strconv.Atoi(strings.TrimSpace(v))
				if err != nil {
					return nil, err
				}
				contentLength = n
				gotHeader = true
			}
		}
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
		out = append(out, body)
	}
}

func TestLSPInitializeAndDiagnostics(t *testing.T) {
	// Drive the server with: initialize, didOpen (bad syntax), shutdown, exit.
	in, out := strings.Builder{}, strings.Builder{}

	in.Write(frame([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)))
	// Unbalanced braces produce a parse-time diagnostic; the loader bails
	// before the checker even runs, but we still see the lex/parse error.
	in.Write(frame([]byte(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/x.tt","text":"func main(): void { echo(\"oops\""}}}`)))
	in.Write(frame([]byte(`{"jsonrpc":"2.0","id":2,"method":"shutdown"}`)))
	in.Write(frame([]byte(`{"jsonrpc":"2.0","method":"exit"}`)))

	if err := Run(strings.NewReader(in.String()), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}

	frames, err := readFrames(bufio.NewReader(strings.NewReader(out.String())))
	if err != nil {
		t.Fatalf("readFrames: %v", err)
	}
	if len(frames) < 3 {
		t.Fatalf("expected at least 3 frames (initialize-resp, diagnostics, shutdown-resp), got %d:\n%s", len(frames), out.String())
	}

	var init struct {
		ID     int `json:"id"`
		Result struct {
			Capabilities map[string]any `json:"capabilities"`
		} `json:"result"`
	}
	if err := json.Unmarshal(frames[0], &init); err != nil {
		t.Fatalf("init resp: %v", err)
	}
	if init.ID != 1 {
		t.Fatalf("init id = %d, want 1", init.ID)
	}
	if init.Result.Capabilities["textDocumentSync"] == nil {
		t.Fatalf("missing textDocumentSync in capabilities: %v", init.Result.Capabilities)
	}

	var diag struct {
		Method string `json:"method"`
		Params struct {
			URI         string       `json:"uri"`
			Diagnostics []diagnostic `json:"diagnostics"`
		} `json:"params"`
	}
	if err := json.Unmarshal(frames[1], &diag); err != nil {
		t.Fatalf("diag frame: %v", err)
	}
	if diag.Method != "textDocument/publishDiagnostics" {
		t.Fatalf("expected publishDiagnostics, got %q", diag.Method)
	}
	if diag.Params.URI != "file:///tmp/x.tt" {
		t.Fatalf("uri = %q", diag.Params.URI)
	}
	// The malformed source should produce at least one diagnostic.
	if len(diag.Params.Diagnostics) == 0 {
		t.Fatalf("expected diagnostics for malformed source; got none")
	}
}

func TestLSPCleanSourceProducesNoDiagnostics(t *testing.T) {
	in, out := strings.Builder{}, strings.Builder{}
	in.Write(frame([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)))
	in.Write(frame([]byte(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/ok.tt","text":"func main(): void { echo(\"hi\") }"}}}`)))
	in.Write(frame([]byte(`{"jsonrpc":"2.0","method":"exit"}`)))

	if err := Run(strings.NewReader(in.String()), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	frames, err := readFrames(bufio.NewReader(strings.NewReader(out.String())))
	if err != nil {
		t.Fatalf("readFrames: %v", err)
	}
	// Find the publishDiagnostics frame and assert empty array.
	for _, f := range frames {
		var m struct {
			Method string `json:"method"`
			Params struct {
				Diagnostics []diagnostic `json:"diagnostics"`
			} `json:"params"`
		}
		if json.Unmarshal(f, &m) == nil && m.Method == "textDocument/publishDiagnostics" {
			if len(m.Params.Diagnostics) != 0 {
				t.Fatalf("expected zero diagnostics, got %d: %+v", len(m.Params.Diagnostics), m.Params.Diagnostics)
			}
			return
		}
	}
	t.Fatal("never saw a publishDiagnostics frame")
}

// TestLSPLiveTypeError exercises the v1 capability: type errors (not just
// parse errors) show up as diagnostics as the user types. The source here is
// syntactically valid but assigns a string to a number-annotated variable.
func TestLSPLiveTypeError(t *testing.T) {
	in, out := strings.Builder{}, strings.Builder{}
	src := `func main(): void { let x: number = "hello" echo("${x}") }`
	in.Write(frame([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)))
	in.Write(frame([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/typeerr.tt","text":%q}}}`,
		src,
	))))
	in.Write(frame([]byte(`{"jsonrpc":"2.0","method":"exit"}`)))

	if err := Run(strings.NewReader(in.String()), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	frames, err := readFrames(bufio.NewReader(strings.NewReader(out.String())))
	if err != nil {
		t.Fatalf("readFrames: %v", err)
	}
	for _, f := range frames {
		var m struct {
			Method string `json:"method"`
			Params struct {
				Diagnostics []diagnostic `json:"diagnostics"`
			} `json:"params"`
		}
		if json.Unmarshal(f, &m) == nil && m.Method == "textDocument/publishDiagnostics" {
			if len(m.Params.Diagnostics) == 0 {
				t.Fatalf("expected at least one type-error diagnostic; got none")
			}
			// Loose check: the message should reference number/string.
			msg := m.Params.Diagnostics[0].Message
			if !strings.Contains(strings.ToLower(msg), "number") && !strings.Contains(strings.ToLower(msg), "string") {
				t.Fatalf("diagnostic doesn't look like a type error: %q", msg)
			}
			return
		}
	}
	t.Fatal("never saw a publishDiagnostics frame")
}

// TestLSPHoverReportsType ensures hover over an Ident returns the variable's
// type. The buffer declares `let x: number = 1` and we hover on the `x`
// inside `echo("${x}")` — the response should mention `number`.
func TestLSPHoverReportsType(t *testing.T) {
	in, out := strings.Builder{}, strings.Builder{}
	// One line so we can compute the column of `x` inside the interpolation
	// without arithmetic on offsets across lines.
	//                                       1         2         3         4         5
	//                             0123456789012345678901234567890123456789012345678901234
	src := `func main(): void { let x: number = 1 echo("${x}") }`
	// The `x` inside the interpolation is at byte index 47 (0-based char 47).
	hoverChar := strings.Index(src, `${x}`) + 2

	in.Write(frame([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)))
	in.Write(frame([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/hover.tt","text":%q}}}`,
		src,
	))))
	in.Write(frame([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"textDocument/hover","params":{"textDocument":{"uri":"file:///tmp/hover.tt"},"position":{"line":0,"character":%d}}}`,
		hoverChar,
	))))
	in.Write(frame([]byte(`{"jsonrpc":"2.0","method":"exit"}`)))

	if err := Run(strings.NewReader(in.String()), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	frames, err := readFrames(bufio.NewReader(strings.NewReader(out.String())))
	if err != nil {
		t.Fatalf("readFrames: %v", err)
	}
	for _, f := range frames {
		var m struct {
			ID     int `json:"id"`
			Result struct {
				Contents struct {
					Value string `json:"value"`
				} `json:"contents"`
			} `json:"result"`
		}
		if json.Unmarshal(f, &m) != nil || m.ID != 2 {
			continue
		}
		if !strings.Contains(m.Result.Contents.Value, "number") {
			t.Fatalf("hover did not include `number`: %q", m.Result.Contents.Value)
		}
		return
	}
	t.Fatalf("no hover response found in:\n%s", out.String())
}

// TestLSPDefinitionJumpsToDecl ensures definition over an Ident returns the
// declaration's range. The buffer declares `let x: number = 1` and we resolve
// definition on the `x` inside the call — the range should land on the let.
func TestLSPDefinitionJumpsToDecl(t *testing.T) {
	in, out := strings.Builder{}, strings.Builder{}
	src := `func main(): void { let x: number = 1 echo("${x}") }`
	useChar := strings.Index(src, `${x}`) + 2
	declChar := strings.Index(src, "let x") + 4 // column of the `x` after `let `

	in.Write(frame([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)))
	in.Write(frame([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/def.tt","text":%q}}}`,
		src,
	))))
	in.Write(frame([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"textDocument/definition","params":{"textDocument":{"uri":"file:///tmp/def.tt"},"position":{"line":0,"character":%d}}}`,
		useChar,
	))))
	in.Write(frame([]byte(`{"jsonrpc":"2.0","method":"exit"}`)))

	if err := Run(strings.NewReader(in.String()), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	frames, err := readFrames(bufio.NewReader(strings.NewReader(out.String())))
	if err != nil {
		t.Fatalf("readFrames: %v", err)
	}
	for _, f := range frames {
		var m struct {
			ID     int `json:"id"`
			Result struct {
				URI   string `json:"uri"`
				Range struct {
					Start struct {
						Line int `json:"line"`
						Char int `json:"character"`
					} `json:"start"`
				} `json:"range"`
			} `json:"result"`
		}
		if json.Unmarshal(f, &m) != nil || m.ID != 2 {
			continue
		}
		if m.Result.URI != "file:///tmp/def.tt" {
			t.Fatalf("definition URI = %q, want same file", m.Result.URI)
		}
		if m.Result.Range.Start.Line != 0 || m.Result.Range.Start.Char != declChar {
			t.Fatalf("definition range = line %d char %d, want line 0 char %d",
				m.Result.Range.Start.Line, m.Result.Range.Start.Char, declChar)
		}
		return
	}
	t.Fatalf("no definition response found in:\n%s", out.String())
}

// TestLSPDocumentSymbol asks for the outline of a file with one function,
// one global, and one type. It checks names and kinds round-trip correctly
// so editor outlines render with the right icons.
func TestLSPDocumentSymbol(t *testing.T) {
	in, out := strings.Builder{}, strings.Builder{}
	src := "type Greeting = { who: string }\nconst kPi: number = 3\nfunc main(): void { echo(\"hi\") }\n"
	in.Write(frame([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)))
	in.Write(frame([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/sym.tt","text":%q}}}`,
		src,
	))))
	in.Write(frame([]byte(`{"jsonrpc":"2.0","id":2,"method":"textDocument/documentSymbol","params":{"textDocument":{"uri":"file:///tmp/sym.tt"}}}`)))
	in.Write(frame([]byte(`{"jsonrpc":"2.0","method":"exit"}`)))

	if err := Run(strings.NewReader(in.String()), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	frames, err := readFrames(bufio.NewReader(strings.NewReader(out.String())))
	if err != nil {
		t.Fatalf("readFrames: %v", err)
	}
	for _, f := range frames {
		var m struct {
			ID     int `json:"id"`
			Result []struct {
				Name string `json:"name"`
				Kind int    `json:"kind"`
			} `json:"result"`
		}
		if json.Unmarshal(f, &m) != nil || m.ID != 2 {
			continue
		}
		if len(m.Result) != 3 {
			t.Fatalf("expected 3 symbols, got %d: %+v", len(m.Result), m.Result)
		}
		want := map[string]int{
			"Greeting": 23, // Struct
			"kPi":      14, // Constant
			"main":     12, // Function
		}
		for _, sym := range m.Result {
			if want[sym.Name] != sym.Kind {
				t.Errorf("symbol %q kind = %d, want %d", sym.Name, sym.Kind, want[sym.Name])
			}
		}
		return
	}
	t.Fatalf("no documentSymbol response found in:\n%s", out.String())
}

// TestLSPCompletionIncludesBuiltinsAndLocals exercises the completion path:
// the list must include a well-known builtin (`echo`) plus a local declared
// before the cursor, and must NOT include a local declared after.
func TestLSPCompletionIncludesBuiltinsAndLocals(t *testing.T) {
	in, out := strings.Builder{}, strings.Builder{}
	// Cursor inside the function body, between `before` and `after`.
	src := "func main(): void {\n  let before: number = 1\n  \n  let after: number = 2\n}\n"
	// Position the cursor on the empty third line at column 2.
	in.Write(frame([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)))
	in.Write(frame([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/cmp.tt","text":%q}}}`,
		src,
	))))
	in.Write(frame([]byte(
		`{"jsonrpc":"2.0","id":2,"method":"textDocument/completion","params":{"textDocument":{"uri":"file:///tmp/cmp.tt"},"position":{"line":2,"character":2}}}`,
	)))
	in.Write(frame([]byte(`{"jsonrpc":"2.0","method":"exit"}`)))

	if err := Run(strings.NewReader(in.String()), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	frames, err := readFrames(bufio.NewReader(strings.NewReader(out.String())))
	if err != nil {
		t.Fatalf("readFrames: %v", err)
	}
	for _, f := range frames {
		var m struct {
			ID     int `json:"id"`
			Result struct {
				Items []struct {
					Label string `json:"label"`
					Kind  int    `json:"kind"`
				} `json:"items"`
			} `json:"result"`
		}
		if json.Unmarshal(f, &m) != nil || m.ID != 2 {
			continue
		}
		labels := map[string]bool{}
		for _, it := range m.Result.Items {
			labels[it.Label] = true
		}
		if !labels["echo"] {
			t.Errorf("expected builtin `echo` in completion list")
		}
		if !labels["main"] {
			t.Errorf("expected top-level `main` in completion list")
		}
		if !labels["before"] {
			t.Errorf("expected local `before` (declared above cursor) in completion list")
		}
		if labels["after"] {
			t.Errorf("did not expect local `after` (declared below cursor) in completion list")
		}
		return
	}
	t.Fatalf("no completion response found in:\n%s", out.String())
}

// TestLSPReferencesFindsAllUses declares `x` once and uses it twice; the
// references response (with includeDeclaration=true) should return 3 locations.
func TestLSPReferencesFindsAllUses(t *testing.T) {
	in, out := strings.Builder{}, strings.Builder{}
	src := `func main(): void { let x: number = 1 echo("${x}") echo("${x}") }`
	useChar := strings.Index(src, `${x}`) + 2 // cursor on the first use

	in.Write(frame([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)))
	in.Write(frame([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/refs.tt","text":%q}}}`,
		src,
	))))
	in.Write(frame([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"textDocument/references","params":{"textDocument":{"uri":"file:///tmp/refs.tt"},"position":{"line":0,"character":%d},"context":{"includeDeclaration":true}}}`,
		useChar,
	))))
	in.Write(frame([]byte(`{"jsonrpc":"2.0","method":"exit"}`)))

	if err := Run(strings.NewReader(in.String()), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	frames, err := readFrames(bufio.NewReader(strings.NewReader(out.String())))
	if err != nil {
		t.Fatalf("readFrames: %v", err)
	}
	for _, f := range frames {
		var m struct {
			ID     int `json:"id"`
			Result []struct {
				URI   string `json:"uri"`
				Range rng    `json:"range"`
			} `json:"result"`
		}
		if json.Unmarshal(f, &m) != nil || m.ID != 2 {
			continue
		}
		if len(m.Result) != 3 {
			t.Fatalf("expected 3 references (decl + 2 uses), got %d: %+v", len(m.Result), m.Result)
		}
		return
	}
	t.Fatalf("no references response found in:\n%s", out.String())
}

// TestLSPRenameProducesWorkspaceEdit verifies rename emits one TextEdit per
// occurrence — declaration plus two uses — all targeting the same URI.
func TestLSPRenameProducesWorkspaceEdit(t *testing.T) {
	in, out := strings.Builder{}, strings.Builder{}
	src := `func main(): void { let x: number = 1 echo("${x}") echo("${x}") }`
	useChar := strings.Index(src, `${x}`) + 2

	in.Write(frame([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)))
	in.Write(frame([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/rename.tt","text":%q}}}`,
		src,
	))))
	in.Write(frame([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"textDocument/rename","params":{"textDocument":{"uri":"file:///tmp/rename.tt"},"position":{"line":0,"character":%d},"newName":"renamed"}}`,
		useChar,
	))))
	in.Write(frame([]byte(`{"jsonrpc":"2.0","method":"exit"}`)))

	if err := Run(strings.NewReader(in.String()), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	frames, err := readFrames(bufio.NewReader(strings.NewReader(out.String())))
	if err != nil {
		t.Fatalf("readFrames: %v", err)
	}
	for _, f := range frames {
		var m struct {
			ID     int `json:"id"`
			Result struct {
				Changes map[string][]struct {
					NewText string `json:"newText"`
					Range   rng    `json:"range"`
				} `json:"changes"`
			} `json:"result"`
		}
		if json.Unmarshal(f, &m) != nil || m.ID != 2 {
			continue
		}
		edits := m.Result.Changes["file:///tmp/rename.tt"]
		if len(edits) != 3 {
			t.Fatalf("expected 3 edits (decl + 2 uses), got %d: %+v", len(edits), edits)
		}
		for _, e := range edits {
			if e.NewText != "renamed" {
				t.Errorf("edit newText = %q, want %q", e.NewText, "renamed")
			}
		}
		return
	}
	t.Fatalf("no rename response found in:\n%s", out.String())
}

// TestLSPRenameRejectsBuiltin asserts the server refuses to rename a builtin
// (`echo` here). The result must be nil — anything else would corrupt code.
func TestLSPRenameRejectsBuiltin(t *testing.T) {
	in, out := strings.Builder{}, strings.Builder{}
	src := `func main(): void { echo("hi") }`
	useChar := strings.Index(src, "echo") + 1

	in.Write(frame([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)))
	in.Write(frame([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///tmp/no.tt","text":%q}}}`,
		src,
	))))
	in.Write(frame([]byte(fmt.Sprintf(
		`{"jsonrpc":"2.0","id":2,"method":"textDocument/rename","params":{"textDocument":{"uri":"file:///tmp/no.tt"},"position":{"line":0,"character":%d},"newName":"shout"}}`,
		useChar,
	))))
	in.Write(frame([]byte(`{"jsonrpc":"2.0","method":"exit"}`)))

	if err := Run(strings.NewReader(in.String()), &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	frames, err := readFrames(bufio.NewReader(strings.NewReader(out.String())))
	if err != nil {
		t.Fatalf("readFrames: %v", err)
	}
	for _, f := range frames {
		var m struct {
			ID     int            `json:"id"`
			Result map[string]any `json:"result"`
		}
		if json.Unmarshal(f, &m) != nil || m.ID != 2 {
			continue
		}
		if m.Result != nil {
			t.Fatalf("expected nil result for builtin rename, got: %+v", m.Result)
		}
		return
	}
	t.Fatalf("no rename response found in:\n%s", out.String())
}
