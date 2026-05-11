// Package lsp implements a Language Server for tartalo.
//
// Scope (v1): JSON-RPC 2.0 over stdio. The server handles `initialize`,
// `shutdown`, `exit`, the `didOpen` / `didChange` / `didClose` text
// notifications, plus `textDocument/hover` and `textDocument/definition`.
// Diagnostics are produced by running the full lex+parse+check pipeline on
// the in-memory buffer (via an overlay map fed to the loader) so type
// errors show up as the user types — not just lex/parse problems.
//
// State is keyed by document URI. We treat the URI's path component as the
// canonical absolute path on disk; for `file://` URIs that's straightforward,
// and imports resolve from the same directory the user would see at the
// command line.
package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/checker"
	"github.com/enekos/tartalo/internal/diag"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/loader"
	"github.com/enekos/tartalo/internal/parser"
	"github.com/enekos/tartalo/internal/token"
	"github.com/enekos/tartalo/internal/types"
)

// Run drives the LSP message loop until the client closes stdin or sends
// `exit`. Each message is a Content-Length framed JSON-RPC payload.
func Run(in io.Reader, out io.Writer) error {
	r := bufio.NewReader(in)
	s := &server{
		out:  &writer{w: out},
		docs: map[string]*docState{},
	}
	for {
		msg, err := readMessage(r)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		stop, err := s.handle(msg)
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

// server holds the cross-message state for a single LSP session. docs is
// indexed by URI; each entry remembers the last text plus, when the parse +
// check succeeded, the loaded modules and TypeInfo. hover/definition use
// that cached info so they don't have to re-run the pipeline on every
// request.
type server struct {
	out  *writer
	mu   sync.Mutex
	docs map[string]*docState
}

type docState struct {
	text    string
	absPath string           // canonical absolute path derived from the URI
	entry   *loader.Module   // the module whose AbsPath matches absPath (nil if parse failed)
	modules []*loader.Module // full transitive module set (deps before dependents)
	info    *checker.TypeInfo
}

// handle dispatches a single message. The bool return signals "client wants
// the server to terminate" — set after a successful `exit` notification.
func (s *server) handle(msg *rawMessage) (bool, error) {
	switch msg.Method {
	case "initialize":
		return false, s.out.sendResponse(msg.ID, map[string]any{
			"capabilities": map[string]any{
				// 1 = full document sync. Simplest correct mode given that
				// our checker re-runs the whole pipeline anyway.
				"textDocumentSync":       1,
				"hoverProvider":          true,
				"definitionProvider":     true,
				"documentSymbolProvider": true,
				"referencesProvider":     true,
				"renameProvider":         true,
				"completionProvider": map[string]any{
					// `.` triggers field completion; `:` is a soft hint for
					// type annotation contexts. The client still asks for
					// completions on any identifier prefix.
					"triggerCharacters": []string{".", ":"},
				},
			},
			"serverInfo": map[string]string{
				"name":    "tartalo-lsp",
				"version": "0.3",
			},
		})
	case "initialized":
		return false, nil
	case "textDocument/didOpen":
		uri, text, ok := parseDidOpen(msg.Params)
		if !ok {
			return false, nil
		}
		return false, s.publish(uri, text)
	case "textDocument/didChange":
		uri, text, ok := parseDidChange(msg.Params)
		if !ok {
			return false, nil
		}
		return false, s.publish(uri, text)
	case "textDocument/didClose":
		uri, ok := parseDidClose(msg.Params)
		if !ok {
			return false, nil
		}
		s.mu.Lock()
		delete(s.docs, uri)
		s.mu.Unlock()
		// Clear any stale diagnostics so the editor doesn't keep showing them.
		return false, s.out.sendNotification("textDocument/publishDiagnostics", map[string]any{
			"uri":         uri,
			"diagnostics": []diagnostic{},
		})
	case "textDocument/hover":
		uri, line, char, ok := parseTextDocumentPosition(msg.Params)
		if !ok {
			return false, s.out.sendResponse(msg.ID, nil)
		}
		return false, s.out.sendResponse(msg.ID, s.hover(uri, line, char))
	case "textDocument/definition":
		uri, line, char, ok := parseTextDocumentPosition(msg.Params)
		if !ok {
			return false, s.out.sendResponse(msg.ID, nil)
		}
		return false, s.out.sendResponse(msg.ID, s.definition(uri, line, char))
	case "textDocument/documentSymbol":
		uri, ok := parseTextDocumentURI(msg.Params)
		if !ok {
			return false, s.out.sendResponse(msg.ID, []any{})
		}
		return false, s.out.sendResponse(msg.ID, s.documentSymbol(uri))
	case "textDocument/completion":
		uri, line, char, ok := parseTextDocumentPosition(msg.Params)
		if !ok {
			return false, s.out.sendResponse(msg.ID, map[string]any{"isIncomplete": false, "items": []any{}})
		}
		return false, s.out.sendResponse(msg.ID, s.completion(uri, line, char))
	case "textDocument/references":
		uri, line, char, ok := parseTextDocumentPosition(msg.Params)
		if !ok {
			return false, s.out.sendResponse(msg.ID, []any{})
		}
		includeDecl := parseReferencesIncludeDecl(msg.Params)
		return false, s.out.sendResponse(msg.ID, s.references(uri, line, char, includeDecl))
	case "textDocument/rename":
		uri, line, char, newName, ok := parseRename(msg.Params)
		if !ok {
			return false, s.out.sendResponse(msg.ID, nil)
		}
		return false, s.out.sendResponse(msg.ID, s.rename(uri, line, char, newName))
	case "shutdown":
		return false, s.out.sendResponse(msg.ID, nil)
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
		return false, s.out.writeFrame(payload)
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

func parseTextDocumentURI(raw json.RawMessage) (string, bool) {
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

func parseReferencesIncludeDecl(raw json.RawMessage) bool {
	var p struct {
		Context struct {
			IncludeDeclaration bool `json:"includeDeclaration"`
		} `json:"context"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return false
	}
	return p.Context.IncludeDeclaration
}

func parseRename(raw json.RawMessage) (string, int, int, string, bool) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position struct {
			Line int `json:"line"`
			Char int `json:"character"`
		} `json:"position"`
		NewName string `json:"newName"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", 0, 0, "", false
	}
	return p.TextDocument.URI, p.Position.Line, p.Position.Char, p.NewName, true
}

func parseTextDocumentPosition(raw json.RawMessage) (string, int, int, bool) {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position struct {
			Line int `json:"line"`
			Char int `json:"character"`
		} `json:"position"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return "", 0, 0, false
	}
	return p.TextDocument.URI, p.Position.Line, p.Position.Char, true
}

// publish runs the full front-end against the buffer and emits diagnostics.
// On a successful check it caches the modules + TypeInfo on the docState so
// hover/definition can serve answers without re-running the pipeline.
func (s *server) publish(uri, text string) error {
	abs, ok := uriToAbsPath(uri)
	if !ok {
		// Non-file URIs (e.g. untitled:) — fall back to in-memory lex+parse
		// only, since the loader needs a path to anchor imports from.
		return s.out.sendNotification("textDocument/publishDiagnostics", map[string]any{
			"uri":         uri,
			"diagnostics": diagnosticsStandalone(uri, text),
		})
	}

	overlay := map[string]string{abs: text}
	// Merge in any other open docs so the loader sees the same edits the
	// user has in the editor for imported files too.
	s.mu.Lock()
	for _, d := range s.docs {
		if d.absPath != "" && d.absPath != abs {
			overlay[d.absPath] = d.text
		}
	}
	s.mu.Unlock()

	modules, _, lerrs := loader.LoadOverlay(abs, overlay)

	state := &docState{text: text, absPath: abs, modules: modules}
	for _, m := range modules {
		if m.AbsPath == abs {
			state.entry = m
			break
		}
	}

	// Only run the checker when the load phase produced no errors and we
	// have a valid module graph. Running it on a half-parsed tree would
	// panic (the checker dereferences m.File.Decls unconditionally).
	var cerrs []error
	if len(lerrs) == 0 && len(modules) > 0 && allParsed(modules) {
		info, errs := checker.New().Check(modules)
		state.info = info
		cerrs = errs
	}

	s.mu.Lock()
	s.docs[uri] = state
	s.mu.Unlock()

	diags := []diagnostic{}
	for _, e := range append(lerrs, cerrs...) {
		if d, ok := errToLSPDiag(e); ok {
			// Only publish diagnostics whose position points at the open
			// file. Errors in imported files belong to that file's URI and
			// would be confusing to surface here.
			if d.file == "" || d.file == filepath.Base(abs) {
				diags = append(diags, d.diag)
			}
		}
	}
	return s.out.sendNotification("textDocument/publishDiagnostics", map[string]any{
		"uri":         uri,
		"diagnostics": diags,
	})
}

// allParsed reports whether every module has a non-nil File. The checker
// assumes that invariant; passing a module with a nil File would deref-panic.
func allParsed(mods []*loader.Module) bool {
	for _, m := range mods {
		if m.File == nil {
			return false
		}
	}
	return true
}

// diagnosticsStandalone is the fallback path for non-file URIs: lex+parse
// the buffer alone, no checker, no imports.
func diagnosticsStandalone(uri, text string) []diagnostic {
	name := uriBasename(uri)
	toks, lerrs := lexer.New(name, text).Tokenize()
	_, perrs := parser.New(toks).Parse(name)
	out := []diagnostic{}
	for _, e := range append(lerrs, perrs...) {
		if d, ok := errToLSPDiag(e); ok {
			out = append(out, d.diag)
		}
	}
	return out
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

// posRe pulls a `file:line:col: message` prefix off legacy error strings
// (those not produced via diag.Diag). Kept as a fallback for any producer
// not yet converted.
var posRe = regexp.MustCompile(`^(.*?):(\d+):(\d+):\s*(.*)$`)

// taggedDiag carries the source file basename alongside the wire-shape so
// publish() can filter diagnostics from imported files out of the entry
// document's diagnostic set.
type taggedDiag struct {
	file string
	diag diagnostic
}

func errToLSPDiag(e error) (taggedDiag, bool) {
	if d := diag.As(e); d != nil {
		return taggedDiag{file: d.Pos.File, diag: diagFromDiag(d)}, true
	}
	// Legacy fallback: parse the file:line:col: prefix.
	m := posRe.FindStringSubmatch(e.Error())
	if m == nil {
		return taggedDiag{}, false
	}
	file := m[1]
	// The regex's leading .*? swallows a "read /abs/path: ..." or similar
	// prefix; take just the basename so it matches the entry file's File.
	if i := strings.LastIndex(file, "/"); i >= 0 {
		file = file[i+1:]
	}
	line, _ := strconv.Atoi(m[2])
	col, _ := strconv.Atoi(m[3])
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	return taggedDiag{
		file: file,
		diag: diagnostic{
			Range: rng{
				Start: position{Line: line - 1, Char: col - 1},
				End:   position{Line: line - 1, Char: col},
			},
			Severity: 1,
			Source:   "tartalo",
			Message:  strings.TrimSpace(m[4]),
		},
	}, true
}

// diagFromDiag converts a structured *diag.Diag into the LSP wire shape.
// When the diag carries a hint or suggestion, fold them into the message
// (LSP doesn't have a separate hint channel — `relatedInformation` is the
// closest, but it requires URIs and ranges per note). Single message with
// help/suggestion folded in is what most editors render legibly.
func diagFromDiag(d *diag.Diag) diagnostic {
	startLine, startChar := zeroBased(d.Pos.Line, d.Pos.Col)
	endLine, endChar := startLine, startChar+1
	if d.End.Line > 0 {
		endLine, endChar = zeroBased(d.End.Line, d.End.Col)
	}
	msg := d.Msg
	if d.Suggest != "" {
		msg += "\nsuggestion: " + d.Suggest
	}
	if d.Hint != "" {
		msg += "\nhelp: " + d.Hint
	}
	severity := 1
	if d.Severity == diag.Warning {
		severity = 2
	}
	return diagnostic{
		Range: rng{
			Start: position{Line: startLine, Char: startChar},
			End:   position{Line: endLine, Char: endChar},
		},
		Severity: severity,
		Source:   "tartalo",
		Message:  msg,
	}
}

func zeroBased(line, col int) (int, int) {
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	return line - 1, col - 1
}

// hover returns LSP hover content for the symbol at (line, char). The result
// is a `{contents, range}` object the LSP renders as a tooltip. Returns nil
// when the cursor isn't on a meaningful node — LSP treats nil as "no hover."
func (s *server) hover(uri string, line, char int) any {
	s.mu.Lock()
	st, ok := s.docs[uri]
	s.mu.Unlock()
	if !ok || st.entry == nil || st.info == nil {
		return nil
	}
	node := nodeAt(st.entry.File, line+1, char+1)
	if node == nil {
		return nil
	}
	text, hoverRange, ok := hoverFor(node, st.info)
	if !ok {
		return nil
	}
	return map[string]any{
		"contents": map[string]any{
			"kind":  "markdown",
			"value": "```tartalo\n" + text + "\n```",
		},
		"range": hoverRange,
	}
}

// hoverFor builds the hover text and selection range for an AST node. It
// prefers symbol-derived information (function signatures, parameter types)
// when present and falls back to the expression's inferred type otherwise.
func hoverFor(node ast.Node, info *checker.TypeInfo) (string, rng, bool) {
	switch n := node.(type) {
	case *ast.Ident:
		if sym, ok := info.Uses[n]; ok {
			return symbolHover(sym), identRange(n), true
		}
		if e, ok := node.(ast.Expr); ok {
			if t, ok := info.Types[e]; ok {
				return n.Name + ": " + types.Format(t), identRange(n), true
			}
		}
	case *ast.FieldExpr:
		if t, ok := info.Types[n]; ok {
			startLine, startChar := zeroBased(n.NamePos.Line, n.NamePos.Col)
			return n.Name + ": " + types.Format(t), rng{
				Start: position{Line: startLine, Char: startChar},
				End:   position{Line: startLine, Char: startChar + len(n.Name)},
			}, true
		}
	}
	if e, ok := node.(ast.Expr); ok {
		if t, ok := info.Types[e]; ok {
			return types.Format(t), nodeRange(node), true
		}
	}
	return "", rng{}, false
}

// symbolHover formats a Symbol as a one-line declaration string. Functions
// get the full `func name(p: T, ...): R` shape; everything else falls back
// to `let name: T` / `const name: T` style.
func symbolHover(sym *checker.Symbol) string {
	if sym.IsFunc {
		if f, ok := sym.Type.(*types.Func); ok {
			return formatFuncSignature(sym.Name, f)
		}
	}
	prefix := "let"
	if sym.IsConst {
		prefix = "const"
	}
	if sym.IsParam {
		prefix = "param"
	}
	if sym.IsBuiltin {
		prefix = "builtin"
	}
	return prefix + " " + sym.Name + ": " + types.Format(sym.Type)
}

func formatFuncSignature(name string, f *types.Func) string {
	var b strings.Builder
	b.WriteString("func ")
	b.WriteString(name)
	b.WriteString("(")
	for i, p := range f.Params {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(types.Format(p))
	}
	b.WriteString("): ")
	b.WriteString(types.Format(f.Result))
	return b.String()
}

// identRange returns the LSP range covering an identifier's name. Idents are
// the only AST node whose textual length we can compute precisely from the
// AST alone (Name has known length); everything else falls back to a
// single-character range at the start position.
func identRange(id *ast.Ident) rng {
	startLine, startChar := zeroBased(id.NamePos.Line, id.NamePos.Col)
	return rng{
		Start: position{Line: startLine, Char: startChar},
		End:   position{Line: startLine, Char: startChar + len(id.Name)},
	}
}

func nodeRange(n ast.Node) rng {
	startLine, startChar := zeroBased(n.Pos().Line, n.Pos().Col)
	return rng{
		Start: position{Line: startLine, Char: startChar},
		End:   position{Line: startLine, Char: startChar + 1},
	}
}

// definition returns the source location of the symbol at (line, char). Only
// idents resolve to a declaration in v0; FieldExpr would need the checker to
// track per-field positions on records, which it doesn't yet.
func (s *server) definition(uri string, line, char int) any {
	s.mu.Lock()
	st, ok := s.docs[uri]
	s.mu.Unlock()
	if !ok || st.entry == nil || st.info == nil {
		return nil
	}
	node := nodeAt(st.entry.File, line+1, char+1)
	id, ok := node.(*ast.Ident)
	if !ok {
		return nil
	}
	sym, ok := st.info.Uses[id]
	if !ok {
		return nil
	}
	if sym.IsBuiltin || sym.DeclPos.Line == 0 {
		return nil
	}
	defURI := uri
	if sym.Module != nil && sym.Module.AbsPath != "" && sym.Module.AbsPath != st.absPath {
		defURI = absPathToURI(sym.Module.AbsPath)
	}
	startLine, startChar := zeroBased(sym.DeclPos.Line, sym.DeclPos.Col)
	return map[string]any{
		"uri": defURI,
		"range": rng{
			Start: position{Line: startLine, Char: startChar},
			End:   position{Line: startLine, Char: startChar + len(sym.Name)},
		},
	}
}

// nodeAt walks the file's AST and returns the narrowest node whose extent
// covers (line, col) — both 1-based. "Extent" is approximate: for Idents and
// FieldExpr we use Pos.Col + len(Name), for everything else we descend into
// children and bottom out on whichever leaf had a matching start position.
func nodeAt(f *ast.File, line, col int) ast.Node {
	if f == nil {
		return nil
	}
	v := &locator{line: line, col: col}
	for _, d := range f.Decls {
		v.visitDecl(d)
	}
	return v.best
}

type locator struct {
	line, col int
	best      ast.Node
}

func (v *locator) hitIdent(id *ast.Ident) {
	if id == nil {
		return
	}
	if id.NamePos.Line == v.line && v.col >= id.NamePos.Col && v.col < id.NamePos.Col+len(id.Name) {
		v.best = id
	}
}

func (v *locator) hitFieldName(fe *ast.FieldExpr) {
	if fe.NamePos.Line == v.line && v.col >= fe.NamePos.Col && v.col < fe.NamePos.Col+len(fe.Name) {
		v.best = fe
	}
}

func (v *locator) visitDecl(d ast.Decl) {
	switch d := d.(type) {
	case *ast.VarDecl:
		v.visitExpr(d.Value)
	case *ast.FuncDecl:
		if d.Body != nil {
			v.visitBlock(d.Body)
		}
	case *ast.TestDecl:
		if d.Body != nil {
			v.visitBlock(d.Body)
		}
	case *ast.EvalDecl:
		if d.Body != nil {
			v.visitBlock(d.Body)
		}
	}
}

func (v *locator) visitBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		v.visitStmt(s)
	}
}

func (v *locator) visitStmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.DeclStmt:
		if s.Decl != nil {
			v.visitExpr(s.Decl.Value)
		}
	case *ast.ExprStmt:
		v.visitExpr(s.X)
	case *ast.AssignStmt:
		v.visitExpr(s.Value)
	case *ast.FieldAssignStmt:
		v.visitExpr(s.Target)
		v.visitExpr(s.Value)
	case *ast.ReturnStmt:
		v.visitExpr(s.Value)
	case *ast.IfStmt:
		v.visitExpr(s.Cond)
		v.visitBlock(s.Then)
		v.visitBlock(s.Else)
	case *ast.ForStmt:
		v.visitExpr(s.Iter)
		v.visitBlock(s.Body)
	case *ast.WhileStmt:
		v.visitExpr(s.Cond)
		v.visitBlock(s.Body)
	case *ast.DeferStmt:
		v.visitBlock(s.Body)
	case *ast.ParallelStmt:
		v.visitBlock(s.Body)
	case *ast.TaskStmt:
		v.visitBlock(s.Body)
	case *ast.SpawnStmt:
		v.visitExpr(s.Call)
	case *ast.Block:
		v.visitBlock(s)
	case *ast.MatchStmt:
		v.visitExpr(s.Subject)
		for _, c := range s.Cases {
			v.visitBlock(c.Body)
		}
	}
}

func (v *locator) visitExpr(e ast.Expr) {
	if e == nil {
		return
	}
	switch e := e.(type) {
	case *ast.Ident:
		v.hitIdent(e)
	case *ast.BinaryExpr:
		v.visitExpr(e.Lhs)
		v.visitExpr(e.Rhs)
	case *ast.UnaryExpr:
		v.visitExpr(e.Operand)
	case *ast.CallExpr:
		v.visitExpr(e.Callee)
		for _, a := range e.Args {
			v.visitExpr(a)
		}
	case *ast.FieldExpr:
		v.visitExpr(e.Target)
		// Descend first; if the cursor was inside the target, that's a
		// more specific hit. Otherwise check the field name itself.
		if v.best == nil || (v.best != nil && !insideField(v.best, e)) {
			v.hitFieldName(e)
		}
	case *ast.IndexExpr:
		v.visitExpr(e.Target)
		v.visitExpr(e.Index)
	case *ast.RangeExpr:
		v.visitExpr(e.Start)
		v.visitExpr(e.End)
	case *ast.ArrayLit:
		for _, el := range e.Elems {
			v.visitExpr(el)
		}
	case *ast.CoalesceExpr:
		v.visitExpr(e.Lhs)
		v.visitExpr(e.Rhs)
	case *ast.UnwrapExpr:
		v.visitExpr(e.Operand)
	case *ast.TryExpr:
		v.visitExpr(e.Operand)
	case *ast.RecordLit:
		v.visitExpr(e.Spread)
		for _, fi := range e.Fields {
			v.visitExpr(fi.Value)
		}
	case *ast.CastExpr:
		v.visitExpr(e.Operand)
	case *ast.FuncLit:
		if e.Body != nil {
			v.visitBlock(e.Body)
		}
	case *ast.StringLit:
		for _, p := range e.Parts {
			v.visitExpr(p)
		}
	case *ast.CmdLit:
		for _, p := range e.Parts {
			v.visitExpr(p)
		}
	}
}

// insideField is a guard: when descending a FieldExpr we'd rather report the
// target ident than the field name if the cursor sits on the target. The
// visitor sets v.best as it descends; we use this to decide whether the
// already-recorded hit is "inside" the FieldExpr's target subtree.
func insideField(best ast.Node, fe *ast.FieldExpr) bool {
	if best == nil {
		return false
	}
	return best.Pos().Line < fe.NamePos.Line ||
		(best.Pos().Line == fe.NamePos.Line && best.Pos().Col < fe.NamePos.Col)
}

// uriToAbsPath turns a `file://` URI into a canonical absolute path. Returns
// false for non-file URIs (untitled:, inmemory:, etc.) — callers should fall
// back to standalone lex+parse for those.
func uriToAbsPath(uri string) (string, bool) {
	if !strings.HasPrefix(uri, "file://") {
		return "", false
	}
	p := strings.TrimPrefix(uri, "file://")
	// URI may percent-encode spaces and similar; decode best-effort.
	if dec, err := url.PathUnescape(p); err == nil {
		p = dec
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", false
	}
	return abs, true
}

func absPathToURI(p string) string {
	return "file://" + p
}

func uriBasename(uri string) string {
	s := strings.TrimPrefix(uri, "file://")
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// docState returns the cached state for a URI, or nil if the document was
// never opened or has since been closed. Holds the mutex only long enough
// to read the pointer; the returned *docState is treated as immutable.
func (s *server) docState(uri string) *docState {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.docs[uri]
}

// LSP SymbolKind constants we actually emit. The full enum lives in the LSP
// spec; here we just name the ones we care about so the call sites read
// like English.
const (
	symbolKindFunction = 12
	symbolKindVariable = 13
	symbolKindConstant = 14
	symbolKindStruct   = 23 // "Struct" in the LSP enum; Tartalo records map cleanly here
	symbolKindEnum     = 10 // sum types
	symbolKindMethod   = 6  // tests/evals — they're parameterless callables
)

// LSP CompletionItemKind constants used below.
const (
	completionKindFunction = 3
	completionKindVariable = 6
	completionKindStruct   = 22
	completionKindEnum     = 13
	completionKindKeyword  = 14
)

// documentSymbol returns the top-level declarations of a file as a flat list
// of DocumentSymbol records. The LSP also accepts a hierarchical form with
// nested children; flat is enough for a v0 outline.
func (s *server) documentSymbol(uri string) []any {
	st := s.docState(uri)
	if st == nil || st.entry == nil || st.entry.File == nil {
		return []any{}
	}
	out := []any{}
	for _, d := range st.entry.File.Decls {
		if sym, ok := declToDocumentSymbol(d); ok {
			out = append(out, sym)
		}
	}
	return out
}

// declToDocumentSymbol builds a DocumentSymbol for one top-level decl. Range
// covers the whole declaration when we can find an end position (functions
// have a body with RBrace); for declarations without a body the range
// collapses to the selectionRange.
func declToDocumentSymbol(d ast.Decl) (map[string]any, bool) {
	var (
		name    string
		kind    int
		namePos token.Pos
		fullEnd token.Pos
	)
	switch d := d.(type) {
	case *ast.FuncDecl:
		name, namePos = d.Name, d.NamePos
		kind = symbolKindFunction
		if d.Body != nil {
			fullEnd = d.Body.RBrace
		}
	case *ast.VarDecl:
		name, namePos = d.Name, d.NamePos
		kind = symbolKindVariable
		if d.IsConst {
			kind = symbolKindConstant
		}
	case *ast.TypeDecl:
		name, namePos = d.Name, d.NamePos
		kind = symbolKindStruct
		if _, isSum := d.Spec.(*ast.SumType); isSum {
			kind = symbolKindEnum
		}
	case *ast.TestDecl:
		name, namePos = d.Name, d.NamePos
		kind = symbolKindMethod
		if d.Body != nil {
			fullEnd = d.Body.RBrace
		}
	case *ast.EvalDecl:
		name, namePos = d.Name, d.NamePos
		kind = symbolKindMethod
		if d.Body != nil {
			fullEnd = d.Body.RBrace
		}
	default:
		return nil, false
	}
	if name == "" {
		return nil, false
	}
	selStart, selChar := zeroBased(namePos.Line, namePos.Col)
	selection := rng{
		Start: position{Line: selStart, Char: selChar},
		End:   position{Line: selStart, Char: selChar + len(name)},
	}
	full := selection
	if fullEnd.Line > 0 {
		endLine, endChar := zeroBased(fullEnd.Line, fullEnd.Col+1)
		full = rng{Start: selection.Start, End: position{Line: endLine, Char: endChar}}
	}
	return map[string]any{
		"name":           name,
		"kind":           kind,
		"range":          full,
		"selectionRange": selection,
	}, true
}

// completion returns the visible identifiers at (line, char). The set is
// intentionally a slight over-approximation — builtins, top-level decls of
// the entry module, imported names, plus parameters and locals of the
// enclosing function. The LSP client filters by the user's prefix so a few
// extra candidates don't degrade the experience.
func (s *server) completion(uri string, line, char int) any {
	st := s.docState(uri)
	items := []any{}
	addItem := func(label string, kind int, detail string) {
		it := map[string]any{"label": label, "kind": kind}
		if detail != "" {
			it["detail"] = detail
		}
		items = append(items, it)
	}

	for _, b := range checker.BuiltinSymbols() {
		detail := ""
		if b.Type != nil {
			detail = b.Type.String()
		}
		addItem(b.Name, completionKindFunction, detail)
	}

	if st != nil && st.entry != nil && st.entry.File != nil {
		for _, d := range st.entry.File.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				detail := ""
				if sym := st.info.Decls[checker.MangledName(st.entry, d.Name)]; sym != nil && sym.Type != nil {
					detail = sym.Type.String()
				}
				addItem(d.Name, completionKindFunction, detail)
			case *ast.VarDecl:
				kind := completionKindVariable
				detail := ""
				if sym := st.info.Decls[checker.MangledName(st.entry, d.Name)]; sym != nil && sym.Type != nil {
					detail = sym.Type.String()
				}
				addItem(d.Name, kind, detail)
			case *ast.TypeDecl:
				kind := completionKindStruct
				if _, isSum := d.Spec.(*ast.SumType); isSum {
					kind = completionKindEnum
				}
				addItem(d.Name, kind, "")
			}
		}
		// Imported names — show them with no detail; full type info would
		// require resolving the importing-side Symbol which lives keyed by
		// MangledName on the dep, not the entry. v1 nice-to-have.
		for _, imp := range st.entry.Imports {
			for _, n := range imp.Decl.Names {
				addItem(n.Name, completionKindVariable, "imported from "+imp.Decl.Path)
			}
		}
		// Params + locals from the enclosing function.
		if fd := enclosingFunc(st.entry.File, line+1, char+1); fd != nil {
			for _, p := range fd.Params {
				detail := ""
				if p.TypeAnn != nil {
					detail = formatTypeExpr(p.TypeAnn)
				}
				addItem(p.Name, completionKindVariable, detail)
			}
			for _, ld := range localsBefore(fd.Body, line+1, char+1) {
				detail := ""
				if ld.TypeAnn != nil {
					detail = formatTypeExpr(ld.TypeAnn)
				}
				kind := completionKindVariable
				if ld.IsConst {
					kind = completionKindKeyword
				}
				addItem(ld.Name, kind, detail)
			}
		}
	}

	return map[string]any{
		"isIncomplete": false,
		"items":        items,
	}
}

// enclosingFunc returns the FuncDecl whose body's brace range contains the
// (1-based) position, or nil if the cursor sits outside any function body.
// Used to scope local-variable and parameter completions.
func enclosingFunc(f *ast.File, line, col int) *ast.FuncDecl {
	for _, d := range f.Decls {
		fd, ok := d.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			continue
		}
		if posBetween(fd.Body.LBrace, fd.Body.RBrace, line, col) {
			return fd
		}
	}
	return nil
}

// posBetween reports whether (line, col) sits inside the byte range
// [start, end] (inclusive). Both inputs are 1-based.
func posBetween(start, end token.Pos, line, col int) bool {
	if line < start.Line || line > end.Line {
		return false
	}
	if line == start.Line && col < start.Col {
		return false
	}
	if line == end.Line && col > end.Col {
		return false
	}
	return true
}

// localsBefore walks a block in source order and returns every `let` /
// `const` declaration whose position precedes (line, col). Used to feed
// the completion list — only bindings already visible at the cursor.
func localsBefore(b *ast.Block, line, col int) []*ast.VarDecl {
	if b == nil {
		return nil
	}
	var out []*ast.VarDecl
	var walk func(stmts []ast.Stmt)
	walk = func(stmts []ast.Stmt) {
		for _, s := range stmts {
			if posBefore(s.Pos(), line, col) == false {
				return
			}
			switch s := s.(type) {
			case *ast.DeclStmt:
				if s.Decl != nil {
					out = append(out, s.Decl)
				}
			case *ast.IfStmt:
				if s.Then != nil {
					walk(s.Then.Stmts)
				}
				if s.Else != nil {
					walk(s.Else.Stmts)
				}
			case *ast.ForStmt:
				if s.Body != nil {
					walk(s.Body.Stmts)
				}
			case *ast.WhileStmt:
				if s.Body != nil {
					walk(s.Body.Stmts)
				}
			case *ast.Block:
				walk(s.Stmts)
			}
		}
	}
	walk(b.Stmts)
	return out
}

func posBefore(p token.Pos, line, col int) bool {
	if p.Line < line {
		return true
	}
	if p.Line == line && p.Col <= col {
		return true
	}
	return false
}

// formatTypeExpr is a small pretty-printer for AST TypeExpr nodes used in
// completion `detail` strings. The checker's resolved types are richer but
// we don't have a direct AST→resolved-type map for params; the textual form
// is good enough for tooltips.
func formatTypeExpr(t ast.TypeExpr) string {
	switch t := t.(type) {
	case *ast.TypeName:
		return t.Name
	case *ast.ArrayType:
		return formatTypeExpr(t.Elem) + "[]"
	case *ast.OptionalType:
		return formatTypeExpr(t.Elem) + "?"
	case *ast.MapType:
		return "map<" + formatTypeExpr(t.Key) + ", " + formatTypeExpr(t.Value) + ">"
	case *ast.ChanType:
		return "chan[" + formatTypeExpr(t.Elem) + "]"
	case *ast.FuncType:
		var b strings.Builder
		b.WriteString("func(")
		for i, p := range t.Params {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(formatTypeExpr(p))
		}
		b.WriteString("): ")
		b.WriteString(formatTypeExpr(t.Result))
		return b.String()
	}
	return ""
}

// symbolAt returns the (Symbol, source-range-of-identifier) at (line, col)
// in the entry module's file. It handles three click sites:
//
//   - on an Ident in expression position (uses info.Uses)
//   - on the name of a top-level FuncDecl / VarDecl (uses info.Decls)
//   - on the name of a TypeDecl (uses info.Decls keyed by type name)
//
// Returns (nil, 0, 0, false) if no symbol resolves; callers treat that as
// "no action."
func symbolAt(st *docState, line, col int) (*checker.Symbol, string, token.Pos, int, bool) {
	if st.entry == nil || st.entry.File == nil || st.info == nil {
		return nil, "", token.Pos{}, 0, false
	}
	if n := nodeAt(st.entry.File, line, col); n != nil {
		if id, ok := n.(*ast.Ident); ok {
			if sym := st.info.Uses[id]; sym != nil {
				return sym, id.Name, id.NamePos, len(id.Name), true
			}
		}
	}
	// Try top-level decl names. We match on (line, col) inside [namePos,
	// namePos+len(name)).
	for _, d := range st.entry.File.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if matchName(d.NamePos, d.Name, line, col) {
				if sym := st.info.Decls[checker.MangledName(st.entry, d.Name)]; sym != nil {
					return sym, d.Name, d.NamePos, len(d.Name), true
				}
			}
		case *ast.VarDecl:
			if matchName(d.NamePos, d.Name, line, col) {
				if sym := st.info.Decls[checker.MangledName(st.entry, d.Name)]; sym != nil {
					return sym, d.Name, d.NamePos, len(d.Name), true
				}
			}
		}
	}
	return nil, "", token.Pos{}, 0, false
}

func matchName(p token.Pos, name string, line, col int) bool {
	if p.Line != line {
		return false
	}
	return col >= p.Col && col < p.Col+len(name)
}

// uriForPos maps a position back to a document URI by basename-matching
// against the loaded module set. Falls back to the document's own URI when
// no match is found, which keeps single-file rename/refs sensible.
func uriForPos(st *docState, p token.Pos) string {
	for _, m := range st.modules {
		if filepath.Base(m.AbsPath) == p.File {
			return absPathToURI(m.AbsPath)
		}
	}
	return absPathToURI(st.absPath)
}

// references returns every location where the symbol at the cursor is
// referenced. With `includeDeclaration=true`, the declaration site is part
// of the result.
func (s *server) references(uri string, line, char int, includeDecl bool) []any {
	st := s.docState(uri)
	if st == nil {
		return []any{}
	}
	sym, _, _, _, ok := symbolAt(st, line+1, char+1)
	if !ok || sym == nil {
		return []any{}
	}
	out := []any{}
	seen := map[string]bool{}
	add := func(name string, pos token.Pos) {
		if pos.Line == 0 {
			return
		}
		key := pos.File + "|" + strconv.Itoa(pos.Line) + "|" + strconv.Itoa(pos.Col)
		if seen[key] {
			return
		}
		seen[key] = true
		startLine, startChar := zeroBased(pos.Line, pos.Col)
		out = append(out, map[string]any{
			"uri": uriForPos(st, pos),
			"range": rng{
				Start: position{Line: startLine, Char: startChar},
				End:   position{Line: startLine, Char: startChar + len(name)},
			},
		})
	}
	for id, used := range st.info.Uses {
		if used == sym {
			add(id.Name, id.NamePos)
		}
	}
	if includeDecl && !sym.IsBuiltin {
		add(sym.Name, sym.DeclPos)
	}
	return out
}

// rename produces a WorkspaceEdit that swaps every occurrence of the symbol
// at the cursor for newName. Cross-file edits are emitted when the symbol's
// uses span multiple modules; the LSP client applies them atomically.
//
// Returns nil for invalid renames (builtin, empty/illegal name, cursor not
// on a symbol). LSP treats nil as "no edit to apply."
func (s *server) rename(uri string, line, char int, newName string) any {
	if !isValidIdent(newName) {
		return nil
	}
	st := s.docState(uri)
	if st == nil {
		return nil
	}
	sym, _, _, _, ok := symbolAt(st, line+1, char+1)
	if !ok || sym == nil || sym.IsBuiltin {
		return nil
	}
	changes := map[string][]any{}
	seen := map[string]bool{}
	addEdit := func(name string, pos token.Pos) {
		if pos.Line == 0 {
			return
		}
		key := pos.File + "|" + strconv.Itoa(pos.Line) + "|" + strconv.Itoa(pos.Col)
		if seen[key] {
			return
		}
		seen[key] = true
		startLine, startChar := zeroBased(pos.Line, pos.Col)
		locURI := uriForPos(st, pos)
		changes[locURI] = append(changes[locURI], map[string]any{
			"range": rng{
				Start: position{Line: startLine, Char: startChar},
				End:   position{Line: startLine, Char: startChar + len(name)},
			},
			"newText": newName,
		})
	}
	for id, used := range st.info.Uses {
		if used == sym {
			addEdit(id.Name, id.NamePos)
		}
	}
	addEdit(sym.Name, sym.DeclPos)
	if len(changes) == 0 {
		return nil
	}
	return map[string]any{"changes": changes}
}

// isValidIdent accepts the subset of identifiers the lexer would accept:
// a letter or underscore followed by letters/digits/underscores. We don't
// check against keywords here — the user can still create a confusing rename,
// but downstream checking will flag it on the next pass.
func isValidIdent(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		ok := r == '_' ||
			(r >= 'a' && r <= 'z') ||
			(r >= 'A' && r <= 'Z') ||
			(i > 0 && r >= '0' && r <= '9')
		if !ok {
			return false
		}
	}
	return true
}
