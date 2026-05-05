// Package lsp implements a minimal Language Server for tartalo.
//
// Scope (v0): JSON-RPC 2.0 over stdio. The server understands `initialize`,
// `shutdown`, `exit`, `textDocument/didOpen`, `textDocument/didChange`, and
// `textDocument/didClose`. On open and change, it lex+parses the buffer in
// memory and publishes any syntax errors as diagnostics. Type-checking is
// out of scope at this layer because it requires resolving the import graph
// against on-disk files; an editor will see syntax errors immediately and
// type errors at save time via the regular `tartalo check` flow.
package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/parser"
)

// Run drives the LSP message loop until the client closes stdin or sends
// `exit`. Each message is a Content-Length framed JSON-RPC payload.
func Run(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	w := &writer{w: out}
	for {
		msg, err := readMessage(r)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		stop, err := handle(msg, w)
		if err != nil {
			return err
		}
		if stop {
			return nil
		}
	}
}

type rawMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

func readMessage(r *bufio.Reader) (*rawMessage, error) {
	contentLength := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length:"); ok {
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return nil, fmt.Errorf("bad Content-Length: %w", err)
			}
			contentLength = n
		}
	}
	if contentLength < 0 {
		return nil, fmt.Errorf("missing Content-Length header")
	}
	body := make([]byte, contentLength)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	var m rawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

type writer struct {
	w  io.Writer
	mu sync.Mutex
}

func (wr *writer) writeFrame(payload []byte) error {
	wr.mu.Lock()
	defer wr.mu.Unlock()
	if _, err := fmt.Fprintf(wr.w, "Content-Length: %d\r\n\r\n", len(payload)); err != nil {
		return err
	}
	_, err := wr.w.Write(payload)
	return err
}

func (wr *writer) sendResponse(id json.RawMessage, result any) error {
	payload, err := json.Marshal(struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Result  any             `json:"result"`
	}{"2.0", id, result})
	if err != nil {
		return err
	}
	return wr.writeFrame(payload)
}

func (wr *writer) sendNotification(method string, params any) error {
	payload, err := json.Marshal(struct {
		JSONRPC string `json:"jsonrpc"`
		Method  string `json:"method"`
		Params  any    `json:"params"`
	}{"2.0", method, params})
	if err != nil {
		return err
	}
	return wr.writeFrame(payload)
}

// handle dispatches a single message. The bool return signals "client wants
// the server to terminate" — set after a successful `exit` notification.
func handle(msg *rawMessage, wr *writer) (bool, error) {
	switch msg.Method {
	case "initialize":
		return false, wr.sendResponse(msg.ID, map[string]any{
			"capabilities": map[string]any{
				// 1 = full document sync. Simplest correct mode given that
				// our checker re-runs the whole pipeline anyway.
				"textDocumentSync": 1,
			},
			"serverInfo": map[string]string{
				"name":    "tartalo-lsp",
				"version": "0.1",
			},
		})
	case "initialized":
		return false, nil
	case "textDocument/didOpen":
		uri, text, ok := parseDidOpen(msg.Params)
		if !ok {
			return false, nil
		}
		return false, publish(wr, uri, text)
	case "textDocument/didChange":
		uri, text, ok := parseDidChange(msg.Params)
		if !ok {
			return false, nil
		}
		return false, publish(wr, uri, text)
	case "textDocument/didClose":
		// Clear any stale diagnostics so the editor doesn't keep showing them.
		uri, ok := parseDidClose(msg.Params)
		if !ok {
			return false, nil
		}
		return false, wr.sendNotification("textDocument/publishDiagnostics", map[string]any{
			"uri":         uri,
			"diagnostics": []diagnostic{},
		})
	case "shutdown":
		return false, wr.sendResponse(msg.ID, nil)
	case "exit":
		return true, nil
	}
	// Unknown methods with an ID need a method-not-found response; pure
	// notifications are silently dropped, per the spec.
	if len(msg.ID) > 0 {
		payload, _ := json.Marshal(struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Error   map[string]any  `json:"error"`
		}{"2.0", msg.ID, map[string]any{
			"code":    -32601,
			"message": "method not found: " + msg.Method,
		}})
		return false, wr.writeFrame(payload)
	}
	return false, nil
}

func parseDidOpen(raw json.RawMessage) (string, string, bool) {
	var p struct {
		TextDocument struct {
			URI  string `json:"uri"`
			Text string `json:"text"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", "", false
	}
	return p.TextDocument.URI, p.TextDocument.Text, true
}

func parseDidChange(raw json.RawMessage) (string, string, bool) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		ContentChanges []struct {
			Text string `json:"text"`
		} `json:"contentChanges"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", "", false
	}
	if len(p.ContentChanges) == 0 {
		return p.TextDocument.URI, "", true
	}
	// In full-sync mode the last change's text is the entire new document.
	return p.TextDocument.URI, p.ContentChanges[len(p.ContentChanges)-1].Text, true
}

func parseDidClose(raw json.RawMessage) (string, bool) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", false
	}
	return p.TextDocument.URI, true
}

func publish(wr *writer, uri, text string) error {
	return wr.sendNotification("textDocument/publishDiagnostics", map[string]any{
		"uri":         uri,
		"diagnostics": diagnostics(uri, text),
	})
}

type diagnostic struct {
	Range    rng    `json:"range"`
	Severity int    `json:"severity"`
	Source   string `json:"source"`
	Message  string `json:"message"`
}

type rng struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type position struct {
	Line int `json:"line"`
	Char int `json:"character"`
}

// posRe pulls a `file:line:col: message` prefix off our error strings. The
// file segment may contain a colon (e.g. `tartalo:strings/extra.tt`), so we
// match the *last* `:N:N: ` triple instead of the first.
var posRe = regexp.MustCompile(`:(\d+):(\d+):\s*(.*)$`)

func diagnostics(uri, text string) []diagnostic {
	name := uriBasename(uri)
	toks, lerrs := lexer.New(name, text).Tokenize()
	_, perrs := parser.New(toks).Parse(name)
	out := []diagnostic{}
	for _, e := range append(lerrs, perrs...) {
		if d, ok := errToDiag(e); ok {
			out = append(out, d)
		}
	}
	return out
}

func errToDiag(e error) (diagnostic, bool) {
	m := posRe.FindStringSubmatch(e.Error())
	if m == nil {
		return diagnostic{}, false
	}
	line, _ := strconv.Atoi(m[1])
	col, _ := strconv.Atoi(m[2])
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	return diagnostic{
		Range: rng{
			Start: position{Line: line - 1, Char: col - 1},
			End:   position{Line: line - 1, Char: col},
		},
		Severity: 1,
		Source:   "tartalo",
		Message:  strings.TrimSpace(m[3]),
	}, true
}

func uriBasename(uri string) string {
	s := strings.TrimPrefix(uri, "file://")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}
