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
	// Unbalanced braces — a real parse-time error, since v0 LSP only catches
	// lex/parse problems (type errors arrive on save via `tartalo check`).
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
