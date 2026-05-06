// Package parser turns a token stream into an AST.
//
// It is a hand-written recursive-descent parser with a small Pratt-style
// expression sub-parser for operator precedence. The parser tries to recover
// from common errors by skipping to the next statement boundary so that more
// than one diagnostic can be reported per run.
package parser

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/token"
)

type Parser struct {
	tokens []token.Token
	pos    int
	errs   []error
}

func New(toks []token.Token) *Parser {
	return &Parser{tokens: toks}
}

func (p *Parser) Parse(path string) (*ast.File, []error) {
	f := &ast.File{Path: path}
	// Import declarations may only appear at the top of the file.
	for !p.atEnd() && p.peek().Kind == token.Import {
		startPos := p.pos
		if imp := p.parseImportDecl(); imp != nil {
			f.Imports = append(f.Imports, imp)
		}
		// Defensive: any code path that returns without advancing would loop.
		if p.pos == startPos {
			p.advance()
		}
	}
	for !p.atEnd() {
		startPos := p.pos
		d := p.parseDecl()
		if d != nil {
			f.Decls = append(f.Decls, d)
		}
		if p.pos == startPos {
			// recoverToStmt may halt on a token that's both a recovery point
			// and the offending token (e.g. `if`/`for` at top level, where
			// only declarations are valid). Step past it to guarantee progress.
			p.advance()
		}
	}
	return f, p.errs
}

func (p *Parser) parseImportDecl() *ast.ImportDecl {
	kw := p.advance() // import
	imp := &ast.ImportDecl{KwPos: kw.Pos}
	p.expect(token.LBrace, "import declaration")
	for p.peek().Kind != token.RBrace && !p.atEnd() {
		name := p.expect(token.Ident, "import name list")
		imp.Names = append(imp.Names, ast.ImportName{NamePos: name.Pos, Name: name.Value})
		if _, ok := p.accept(token.Comma); ok {
			continue
		}
		break
	}
	p.expect(token.RBrace, "import declaration")
	// Contextual `from` keyword.
	fromTok := p.peek()
	if fromTok.Kind != token.Ident || fromTok.Value != "from" {
		p.errorf(fromTok.Pos, `expected "from" in import declaration, got %s`, fromTok.Kind)
		return imp
	}
	p.advance()
	// Path is a string literal; we accept simple StringStart+StringPart+StringEnd
	// without interpolations.
	if p.peek().Kind != token.StringStart {
		p.errorf(p.peek().Pos, "expected import path string, got %s", p.peek().Kind)
		return imp
	}
	start := p.advance()
	imp.PathPos = start.Pos
	if p.peek().Kind != token.StringPart {
		p.errorf(p.peek().Pos, "malformed import path string")
		return imp
	}
	imp.Path = p.advance().Value
	if p.peek().Kind != token.StringEnd {
		p.errorf(p.peek().Pos, "import path may not contain interpolations")
		// best-effort recovery: skip until StringEnd
		for !p.atEnd() && p.peek().Kind != token.StringEnd {
			p.advance()
		}
	}
	if p.peek().Kind == token.StringEnd {
		p.advance()
	}
	_, _ = p.accept(token.Semicolon)
	return imp
}

// --- low-level helpers ------------------------------------------------------

func (p *Parser) atEnd() bool { return p.peek().Kind == token.EOF }

func (p *Parser) peek() token.Token {
	return p.tokens[p.pos]
}

func (p *Parser) peekAhead(n int) token.Token {
	if p.pos+n >= len(p.tokens) {
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[p.pos+n]
}

func (p *Parser) advance() token.Token {
	t := p.tokens[p.pos]
	if p.pos < len(p.tokens)-1 {
		p.pos++
	}
	return t
}

func (p *Parser) accept(k token.Kind) (token.Token, bool) {
	if p.peek().Kind == k {
		return p.advance(), true
	}
	return token.Token{}, false
}

func (p *Parser) expect(k token.Kind, ctx string) token.Token {
	t := p.peek()
	if t.Kind != k {
		p.errorf(t.Pos, "expected %s in %s, got %s", k, ctx, t.Kind)
		// don't advance — the caller will try to recover
		return t
	}
	return p.advance()
}

func (p *Parser) errorf(pos token.Pos, format string, args ...any) {
	p.errs = append(p.errs, fmt.Errorf("%s: %s", pos, fmt.Sprintf(format, args...)))
}

// recoverToStmt advances tokens until we reach something that looks like the
// start of a new statement or declaration, so that one syntax error doesn't
// drown the rest of the file in cascading errors.
func (p *Parser) recoverToStmt() {
	for !p.atEnd() {
		switch p.peek().Kind {
		case token.Let, token.Const, token.Func, token.Tool, token.Agent, token.If, token.For, token.Return,
			token.Match, token.Type, token.Export, token.Test, token.RBrace, token.Semicolon:
			return
		}
		p.advance()
	}
}

// --- declarations -----------------------------------------------------------

func (p *Parser) parseDecl() ast.Decl {
	exported := false
	if p.peek().Kind == token.Export {
		p.advance()
		exported = true
	}
	switch p.peek().Kind {
	case token.Func:
		fd := p.parseFunc()
		if fd != nil {
			fd.IsExported = exported
		}
		return fd
	case token.Tool:
		fd := p.parseToolOrAgent(ast.FuncKindTool)
		if fd != nil {
			fd.IsExported = exported
		}
		return fd
	case token.Agent:
		fd := p.parseToolOrAgent(ast.FuncKindAgent)
		if fd != nil {
			fd.IsExported = exported
		}
		return fd
	case token.Let, token.Const:
		v := p.parseVarDecl()
		if v != nil {
			v.IsExported = exported
		}
		_, _ = p.accept(token.Semicolon)
		return v
	case token.Type:
		td := p.parseTypeDecl()
		if td != nil {
			td.IsExported = exported
		}
		return td
	case token.Test:
		if exported {
			p.errorf(p.peek().Pos, "test declarations cannot be exported")
		}
		return p.parseTest()
	}
	t := p.peek()
	if exported {
		p.errorf(t.Pos, "`export` must be followed by func/type/let/const (test cannot be exported), got %s", t.Kind)
	} else {
		p.errorf(t.Pos, "expected declaration (let/const/func/type/test/export/import), got %s", t.Kind)
	}
	p.recoverToStmt()
	return nil
}

// parseTest parses `test "name" { body }`. The name must be a plain string
// literal — interpolation is rejected so test names are stable identifiers
// that don't depend on runtime state.
func (p *Parser) parseTest() *ast.TestDecl {
	kw := p.advance() // test
	td := &ast.TestDecl{KwPos: kw.Pos}
	if p.peek().Kind != token.StringStart {
		p.errorf(p.peek().Pos, `expected string literal after "test", got %s`, p.peek().Kind)
		// Best-effort recovery: try to parse a block anyway so we don't lose
		// nested errors inside an unnamed test body.
		if p.peek().Kind == token.LBrace {
			td.Body = p.parseBlock()
		}
		return td
	}
	start := p.advance() // StringStart
	td.NamePos = start.Pos
	var nameParts []string
	hadInterp := false
	for {
		t := p.peek()
		switch t.Kind {
		case token.StringPart:
			p.advance()
			nameParts = append(nameParts, t.Value)
		case token.InterpStart:
			hadInterp = true
			// consume the interpolation so we can keep parsing
			p.advance()
			_ = p.parseExpr()
			p.expect(token.InterpEnd, "test name")
		case token.StringEnd:
			p.advance()
			td.Name = strings.Join(nameParts, "")
			if hadInterp {
				p.errorf(start.Pos, "test names cannot contain interpolations")
			}
			td.Body = p.parseBlock()
			return td
		case token.EOF:
			p.errorf(t.Pos, "unexpected end of file in test name")
			return td
		default:
			p.errorf(t.Pos, "unexpected %s in test name", t.Kind)
			p.advance()
		}
	}
}

func (p *Parser) parseTypeDecl() *ast.TypeDecl {
	p.advance() // type
	name := p.expect(token.Ident, "type declaration")
	p.expect(token.Assign, "type declaration")
	spec := p.parseTypeDeclRHS()
	_, _ = p.accept(token.Semicolon)
	return &ast.TypeDecl{NamePos: name.Pos, Name: name.Value, Spec: spec}
}

// parseTypeDeclRHS parses the right-hand side of a `type Name = ...` decl.
// In addition to plain types it accepts a sum/union form:
//
//	A{...} | B | C{...}    (or with an optional leading `|`)
//
// Detection rule: a leading `|`, or `Ident {` / `Ident |` at the top level
// signals a sum. Anything else is a regular type expression so we don't
// regress aliases or array/optional shapes.
func (p *Parser) parseTypeDeclRHS() ast.TypeExpr {
	if p.peek().Kind == token.Pipe {
		p.advance()
		return p.parseSumType()
	}
	if p.peek().Kind == token.Ident {
		next := p.peekAhead(1).Kind
		if next == token.LBrace || next == token.Pipe {
			return p.parseSumType()
		}
	}
	return p.parseTypeExpr()
}

func (p *Parser) parseSumType() *ast.SumType {
	s := &ast.SumType{KwPos: p.peek().Pos}
	s.Variants = append(s.Variants, p.parseSumVariant())
	for {
		if _, ok := p.accept(token.Pipe); !ok {
			break
		}
		s.Variants = append(s.Variants, p.parseSumVariant())
	}
	return s
}

func (p *Parser) parseSumVariant() ast.SumVariant {
	name := p.expect(token.Ident, "sum variant")
	v := ast.SumVariant{NamePos: name.Pos, Name: name.Value}
	if _, ok := p.accept(token.LBrace); ok {
		v.HasBraces = true
		for p.peek().Kind != token.RBrace && !p.atEnd() {
			fname := p.expect(token.Ident, "variant field")
			p.expect(token.Colon, "variant field")
			ft := p.parseTypeExpr()
			v.Fields = append(v.Fields, ast.FieldDecl{
				NamePos: fname.Pos,
				Name:    fname.Value,
				TypeAnn: ft,
			})
			if _, ok := p.accept(token.Comma); ok {
				continue
			}
			if _, ok := p.accept(token.Semicolon); ok {
				continue
			}
			break
		}
		p.expect(token.RBrace, "variant fields")
	}
	return v
}

func (p *Parser) parseVarDecl() *ast.VarDecl {
	kw := p.advance() // let | const
	isConst := kw.Kind == token.Const

	name := p.expect(token.Ident, "variable declaration")
	var ty ast.TypeExpr
	if _, ok := p.accept(token.Colon); ok {
		ty = p.parseTypeExpr()
	}
	p.expect(token.Assign, "variable declaration")
	val := p.parseExpr()

	return &ast.VarDecl{
		NamePos: name.Pos,
		Name:    name.Value,
		IsConst: isConst,
		TypeAnn: ty, // may be nil → type is inferred from Value
		Value:   val,
	}
}

func (p *Parser) parseFunc() *ast.FuncDecl {
	return p.parseFuncLike("function declaration", ast.FuncKindPlain)
}

// parseToolOrAgent shares the function-declaration shape with parseFunc but
// records the kind on the AST so the checker/codegen can branch on it.
// Tool/agent bodies may begin with metadata calls — desc("...") and
// budget(N) — which are pulled off and stored on the FuncDecl after parsing.
func (p *Parser) parseToolOrAgent(kind ast.FuncKind) *ast.FuncDecl {
	ctx := "tool declaration"
	if kind == ast.FuncKindAgent {
		ctx = "agent declaration"
	}
	fd := p.parseFuncLike(ctx, kind)
	if fd == nil {
		return nil
	}
	p.extractToolMetadata(fd)
	return fd
}

func (p *Parser) parseFuncLike(ctx string, kind ast.FuncKind) *ast.FuncDecl {
	p.advance() // func | tool | agent
	name := p.expect(token.Ident, ctx)
	p.expect(token.LParen, ctx)

	var params []ast.Param
	if p.peek().Kind != token.RParen {
		for {
			pname := p.expect(token.Ident, "parameter list")
			p.expect(token.Colon, "parameter list")
			pty := p.parseTypeExpr()
			params = append(params, ast.Param{
				NamePos: pname.Pos,
				Name:    pname.Value,
				TypeAnn: pty,
			})
			if _, ok := p.accept(token.Comma); !ok {
				break
			}
		}
	}
	p.expect(token.RParen, ctx)
	p.expect(token.Colon, ctx)
	ret := p.parseTypeExpr()

	// Postfix effect annotations: `: T !net !fs:read !ai`.
	var effects []string
	for p.peek().Kind == token.Bang {
		p.advance() // !
		eff := p.parseEffectName(ctx)
		if eff != "" {
			effects = append(effects, eff)
		}
	}

	body := p.parseBlock()

	return &ast.FuncDecl{
		NamePos: name.Pos,
		Name:    name.Value,
		Kind:    kind,
		Params:  params,
		Result:  ret,
		Effects: effects,
		Body:    body,
	}
}

// parseEffectName reads `name` or `name:tag` after a leading bang.
func (p *Parser) parseEffectName(ctx string) string {
	t := p.expect(token.Ident, ctx+" effect")
	name := t.Value
	if _, ok := p.accept(token.Colon); ok {
		tag := p.expect(token.Ident, ctx+" effect tag")
		return name + ":" + tag.Value
	}
	return name
}

// extractToolMetadata scans the leading statements of a tool/agent body for
// desc("...") and budget(N) calls and stores them on the decl.
func (p *Parser) extractToolMetadata(fd *ast.FuncDecl) {
	if fd.Body == nil {
		return
	}
	stmts := fd.Body.Stmts
	keep := stmts[:0]
	for _, s := range stmts {
		es, ok := s.(*ast.ExprStmt)
		if !ok {
			keep = append(keep, s)
			continue
		}
		ce, ok := es.X.(*ast.CallExpr)
		if !ok {
			keep = append(keep, s)
			continue
		}
		ident, ok := ce.Callee.(*ast.Ident)
		if !ok {
			keep = append(keep, s)
			continue
		}
		switch ident.Name {
		case "desc":
			if len(ce.Args) == 1 {
				if str, ok := plainStringLit(ce.Args[0]); ok {
					fd.Description = str
					continue
				}
			}
			p.errorf(ident.Pos(), `desc() takes a single string literal`)
			continue
		case "budget":
			if len(ce.Args) == 1 {
				if n, ok := plainIntLit(ce.Args[0]); ok {
					fd.Budget = n
					continue
				}
			}
			p.errorf(ident.Pos(), `budget() takes a single integer literal`)
			continue
		}
		keep = append(keep, s)
	}
	fd.Body.Stmts = keep
}

func plainStringLit(e ast.Expr) (string, bool) {
	sl, ok := e.(*ast.StringLit)
	if !ok {
		return "", false
	}
	if len(sl.Parts) != 1 {
		return "", false
	}
	chunk, ok := sl.Parts[0].(*ast.StringChunk)
	if !ok {
		return "", false
	}
	return chunk.Value, true
}

func plainIntLit(e ast.Expr) (int64, bool) {
	il, ok := e.(*ast.IntLit)
	if !ok {
		return 0, false
	}
	return il.Value, true
}

func (p *Parser) parseTypeExpr() ast.TypeExpr {
	var ty ast.TypeExpr
	t := p.peek()
	switch t.Kind {
	case token.TyString, token.TyNumber, token.TyFloat, token.TyBool, token.TyVoid:
		p.advance()
		ty = &ast.TypeName{NamePos: t.Pos, Name: t.Value}
	case token.Ident:
		// User-defined type reference; checker validates it resolves.
		p.advance()
		ty = &ast.TypeName{NamePos: t.Pos, Name: t.Value}
	case token.LBrace:
		ty = p.parseRecordType()
	case token.Func:
		ty = p.parseFuncType()
	default:
		p.errorf(t.Pos, "expected type, got %s", t.Kind)
		return &ast.TypeName{NamePos: t.Pos, Name: "<error>"}
	}
	// Trailing postfix modifiers: `[]` for arrays, `?` for optional. They can
	// stack and chain: `T[]?`, `T?[]`, `T?[]?` are all syntactically valid.
	for {
		switch p.peek().Kind {
		case token.LBracket:
			if p.peekAhead(1).Kind != token.RBracket {
				return ty
			}
			lb := p.advance()
			p.advance()
			ty = &ast.ArrayType{LBracket: lb.Pos, Elem: ty}
		case token.Question:
			q := p.advance()
			ty = &ast.OptionalType{QPos: q.Pos, Elem: ty}
		default:
			return ty
		}
	}
}

func (p *Parser) parseFuncType() *ast.FuncType {
	kw := p.advance() // func
	p.expect(token.LParen, "func type")
	ft := &ast.FuncType{KwPos: kw.Pos}
	if p.peek().Kind != token.RParen {
		for {
			ft.Params = append(ft.Params, p.parseTypeExpr())
			if _, ok := p.accept(token.Comma); !ok {
				break
			}
		}
	}
	p.expect(token.RParen, "func type")
	p.expect(token.Colon, "func type")
	ft.Result = p.parseTypeExpr()
	return ft
}

func (p *Parser) parseRecordType() *ast.RecordType {
	lb := p.expect(token.LBrace, "record type")
	rt := &ast.RecordType{LBrace: lb.Pos}
	for p.peek().Kind != token.RBrace && !p.atEnd() {
		name := p.expect(token.Ident, "record field")
		p.expect(token.Colon, "record field")
		ft := p.parseTypeExpr()
		rt.Fields = append(rt.Fields, ast.FieldDecl{
			NamePos: name.Pos,
			Name:    name.Value,
			TypeAnn: ft,
		})
		// Field separator: comma or semicolon, both optional after the last.
		if _, ok := p.accept(token.Comma); ok {
			continue
		}
		if _, ok := p.accept(token.Semicolon); ok {
			continue
		}
		break
	}
	rb := p.expect(token.RBrace, "record type")
	rt.RBrace = rb.Pos
	return rt
}

// --- statements -------------------------------------------------------------

func (p *Parser) parseBlock() *ast.Block {
	lb := p.expect(token.LBrace, "block")
	b := &ast.Block{LBrace: lb.Pos}
	for !p.atEnd() && p.peek().Kind != token.RBrace {
		s := p.parseStmt()
		if s != nil {
			b.Stmts = append(b.Stmts, s)
		}
	}
	rb := p.expect(token.RBrace, "block")
	b.RBrace = rb.Pos
	return b
}

func (p *Parser) parseStmt() ast.Stmt {
	switch p.peek().Kind {
	case token.Let, token.Const:
		v := p.parseVarDecl()
		_, _ = p.accept(token.Semicolon)
		return &ast.DeclStmt{Decl: v}
	case token.If:
		return p.parseIf()
	case token.For:
		return p.parseFor()
	case token.Return:
		return p.parseReturn()
	case token.Match:
		return p.parseMatch()
	case token.Defer:
		return p.parseDefer()
	case token.LBrace:
		return p.parseBlock()
	}

	// Either a simple assignment, a field assignment, or an expression statement.
	if p.peek().Kind == token.Ident && p.peekAhead(1).Kind == token.Assign {
		name := p.advance()
		p.advance() // =
		val := p.parseExpr()
		_, _ = p.accept(token.Semicolon)
		return &ast.AssignStmt{NamePos: name.Pos, Name: name.Value, Value: val}
	}

	x := p.parseExpr()
	if fe, ok := x.(*ast.FieldExpr); ok && p.peek().Kind == token.Assign {
		p.advance()
		val := p.parseExpr()
		_, _ = p.accept(token.Semicolon)
		return &ast.FieldAssignStmt{
			Target:  fe.Target,
			NamePos: fe.NamePos,
			Name:    fe.Name,
			Value:   val,
		}
	}
	_, _ = p.accept(token.Semicolon)
	if x == nil {
		p.recoverToStmt()
		return nil
	}
	return &ast.ExprStmt{X: x}
}

func (p *Parser) parseIf() *ast.IfStmt {
	kw := p.advance() // if
	cond := p.parseExpr()
	then := p.parseBlock()
	var elseBlock *ast.Block
	if _, ok := p.accept(token.Else); ok {
		if p.peek().Kind == token.If {
			// else-if: wrap the nested if in a block so the AST stays uniform.
			inner := p.parseIf()
			elseBlock = &ast.Block{
				LBrace: inner.Pos(),
				Stmts:  []ast.Stmt{inner},
			}
		} else {
			elseBlock = p.parseBlock()
		}
	}
	return &ast.IfStmt{KwPos: kw.Pos, Cond: cond, Then: then, Else: elseBlock}
}

func (p *Parser) parseDefer() *ast.DeferStmt {
	kw := p.advance() // defer
	body := p.parseBlock()
	return &ast.DeferStmt{KwPos: kw.Pos, Body: body}
}

func (p *Parser) parseFor() *ast.ForStmt {
	kw := p.advance() // for
	name := p.expect(token.Ident, "for statement")
	p.expect(token.In, "for statement")
	iter := p.parseExpr()
	body := p.parseBlock()
	return &ast.ForStmt{
		KwPos:  kw.Pos,
		Var:    name.Value,
		VarPos: name.Pos,
		Iter:   iter,
		Body:   body,
	}
}

func (p *Parser) parseMatch() *ast.MatchStmt {
	kw := p.advance() // match
	subject := p.parseExpr()
	p.expect(token.LBrace, "match statement")
	m := &ast.MatchStmt{KwPos: kw.Pos, Subject: subject}
	for !p.atEnd() && p.peek().Kind != token.RBrace {
		c := p.parseMatchCase()
		if c == nil {
			p.recoverToStmt()
			continue
		}
		m.Cases = append(m.Cases, c)
		// Track whether we've seen a wildcard-only arm so the codegen can omit
		// a synthesised default.
		for _, pat := range c.Patterns {
			if _, ok := pat.(*ast.WildcardPattern); ok {
				m.HasDflt = true
			}
		}
	}
	rb := p.expect(token.RBrace, "match statement")
	m.RBrace = rb.Pos
	return m
}

func (p *Parser) parseMatchCase() *ast.MatchCase {
	c := &ast.MatchCase{}
	for {
		pat := p.parseMatchPattern()
		if pat == nil {
			return nil
		}
		c.Patterns = append(c.Patterns, pat)
		if _, ok := p.accept(token.Pipe); !ok {
			break
		}
	}
	arrow := p.expect(token.Arrow, "match arm")
	c.ArrowPos = arrow.Pos
	// Body is either a block or a single statement.
	if p.peek().Kind == token.LBrace {
		c.Body = p.parseBlock()
	} else {
		s := p.parseStmt()
		if s == nil {
			return nil
		}
		c.Body = &ast.Block{LBrace: s.Pos(), Stmts: []ast.Stmt{s}}
	}
	return c
}

func (p *Parser) parseMatchPattern() ast.Pattern {
	t := p.peek()
	switch t.Kind {
	case token.Ident:
		if t.Value == "_" {
			p.advance()
			return &ast.WildcardPattern{NamePos: t.Pos}
		}
		// Variant pattern: `Name` (unit) or `Name{ a, b }` (binding form).
		name := p.advance()
		v := &ast.VariantPattern{NamePos: name.Pos, Name: name.Value}
		if _, ok := p.accept(token.LBrace); ok {
			v.HasBraces = true
			for p.peek().Kind != token.RBrace && !p.atEnd() {
				b := p.expect(token.Ident, "variant binding")
				if b.Value == "" {
					return nil
				}
				v.Bindings = append(v.Bindings, ast.VariantBinding{
					NamePos: b.Pos,
					Name:    b.Value,
				})
				if _, ok := p.accept(token.Comma); ok {
					continue
				}
				break
			}
			p.expect(token.RBrace, "variant pattern")
		}
		return v
	case token.Int, token.True, token.False, token.StringStart:
		expr := p.parsePrimary()
		switch expr.(type) {
		case *ast.IntLit, *ast.BoolLit, *ast.StringLit:
			return &ast.LiteralPattern{Lit: expr}
		}
		p.errorf(t.Pos, "unsupported pattern expression")
		return nil
	}
	p.errorf(t.Pos, "expected pattern, got %s", t.Kind)
	return nil
}

func (p *Parser) parseReturn() *ast.ReturnStmt {
	kw := p.advance()
	r := &ast.ReturnStmt{KwPos: kw.Pos}
	// A return without a value is allowed for void functions. We treat
	// `}` and `;` as terminators.
	switch p.peek().Kind {
	case token.RBrace, token.Semicolon, token.EOF:
		// no value
	default:
		r.Value = p.parseExpr()
	}
	_, _ = p.accept(token.Semicolon)
	return r
}

// --- expressions (Pratt) ----------------------------------------------------

// Precedence levels — higher binds tighter.
const (
	precLowest   = iota
	precPipe     // |> (pipeline)
	precCoalesce // ??
	precOr       // ||
	precAnd      // &&
	precEq       // == !=
	precCmp      // < <= > >=
	precRange    // ..
	precAdd      // + -
	precMul      // * / %
	precUnary
	precCall
)

func opPrec(k token.Kind) int {
	switch k {
	case token.Pipeline:
		return precPipe
	case token.Coalesce:
		return precCoalesce
	case token.OrOr:
		return precOr
	case token.AndAnd:
		return precAnd
	case token.Eq, token.Neq:
		return precEq
	case token.Lt, token.Lte, token.Gt, token.Gte:
		return precCmp
	case token.DotDot:
		return precRange
	case token.Plus, token.Minus:
		return precAdd
	case token.Star, token.Slash, token.Percent:
		return precMul
	}
	return precLowest
}

func (p *Parser) parseExpr() ast.Expr {
	return p.parseBinary(precLowest)
}

func (p *Parser) parseBinary(min int) ast.Expr {
	lhs := p.parseUnary()
	for {
		op := p.peek()
		prec := opPrec(op.Kind)
		if prec <= min {
			return lhs
		}
		p.advance()
		rhs := p.parseBinary(prec)
		switch op.Kind {
		case token.DotDot:
			lhs = &ast.RangeExpr{OpPos: op.Pos, Start: lhs, End: rhs}
		case token.Coalesce:
			lhs = &ast.CoalesceExpr{OpPos: op.Pos, Lhs: lhs, Rhs: rhs}
		case token.Pipeline:
			lhs = p.desugarPipeline(op.Pos, lhs, rhs)
		default:
			lhs = &ast.BinaryExpr{OpPos: op.Pos, Op: op.Kind, Lhs: lhs, Rhs: rhs}
		}
	}
}

// desugarPipeline rewrites `lhs |> rhs` at parse time. The right-hand side
// must be a function call or a bare identifier (treated as a zero-arg call);
// the operator prepends `lhs` to the call's argument list. The output is a
// CallExpr identical to what the user would have written by hand, so no
// downstream pass needs to know about pipelines.
func (p *Parser) desugarPipeline(opPos token.Pos, lhs, rhs ast.Expr) ast.Expr {
	switch r := rhs.(type) {
	case *ast.CallExpr:
		args := make([]ast.Expr, 0, len(r.Args)+1)
		args = append(args, lhs)
		args = append(args, r.Args...)
		return &ast.CallExpr{LParenPos: r.LParenPos, Callee: r.Callee, Args: args}
	case *ast.Ident:
		return &ast.CallExpr{LParenPos: r.NamePos, Callee: r, Args: []ast.Expr{lhs}}
	}
	p.errorf(opPos, "right-hand side of |> must be a function call or identifier")
	return rhs
}

func (p *Parser) parseUnary() ast.Expr {
	switch p.peek().Kind {
	case token.Minus, token.Bang:
		op := p.advance()
		return &ast.UnaryExpr{OpPos: op.Pos, Op: op.Kind, Operand: p.parseUnary()}
	}
	return p.parseCall()
}

func (p *Parser) parseCall() ast.Expr {
	expr := p.parsePrimary()
	for {
		switch p.peek().Kind {
		case token.LParen:
			lp := p.advance()
			var args []ast.Expr
			if p.peek().Kind != token.RParen {
				for {
					args = append(args, p.parseExpr())
					if _, ok := p.accept(token.Comma); !ok {
						break
					}
				}
			}
			p.expect(token.RParen, "call expression")
			expr = &ast.CallExpr{LParenPos: lp.Pos, Callee: expr, Args: args}
		case token.LBracket:
			lb := p.advance()
			idx := p.parseExpr()
			p.expect(token.RBracket, "index expression")
			expr = &ast.IndexExpr{LBracket: lb.Pos, Target: expr, Index: idx}
		case token.Dot:
			dot := p.advance()
			name := p.expect(token.Ident, "field access")
			expr = &ast.FieldExpr{
				DotPos:  dot.Pos,
				Target:  expr,
				NamePos: name.Pos,
				Name:    name.Value,
			}
		case token.Bang:
			// Postfix `!` is a forced unwrap. Prefix `!` (boolean negation) is
			// handled in parseUnary which runs *before* parseCall, so reaching
			// `!` here always means postfix.
			bang := p.advance()
			expr = &ast.UnwrapExpr{OpPos: bang.Pos, Operand: expr}
		case token.Question:
			// Postfix `?` is the Result-style propagation operator. The lexer
			// emits Question only when the next char is not also `?` (which
			// would be the Coalesce token), so reaching here is unambiguous.
			q := p.advance()
			expr = &ast.TryExpr{OpPos: q.Pos, Operand: expr}
		default:
			return expr
		}
	}
}

func (p *Parser) parsePrimary() ast.Expr {
	t := p.peek()
	switch t.Kind {
	case token.Int:
		p.advance()
		v, err := strconv.ParseInt(t.Value, 10, 64)
		if err != nil {
			p.errorf(t.Pos, "invalid integer literal %q", t.Value)
		}
		return &ast.IntLit{LitPos: t.Pos, Value: v}
	case token.Float:
		p.advance()
		// Validate format eagerly so a malformed lexer-generated token surfaces
		// a clean parse error rather than failing later in awk at runtime.
		if _, err := strconv.ParseFloat(t.Value, 64); err != nil {
			p.errorf(t.Pos, "invalid float literal %q", t.Value)
		}
		return &ast.FloatLit{LitPos: t.Pos, Text: t.Value}
	case token.True, token.False:
		p.advance()
		return &ast.BoolLit{LitPos: t.Pos, Value: t.Kind == token.True}
	case token.Null:
		p.advance()
		return &ast.NullLit{LitPos: t.Pos}
	case token.Ident:
		// Detect a typed record literal: `Name{` followed by `Ident :` or `}`.
		// Anything else is just an identifier reference.
		if p.peekAhead(1).Kind == token.LBrace && p.looksLikeRecordLit(2) {
			p.advance()       // ident
			lb := p.advance() // {
			return p.parseRecordLitBody(lb.Pos, t.Value, t.Pos)
		}
		p.advance()
		return &ast.Ident{NamePos: t.Pos, Name: t.Value}
	case token.LParen:
		p.advance()
		e := p.parseExpr()
		p.expect(token.RParen, "parenthesized expression")
		return e
	case token.StringStart:
		return p.parseString()
	case token.CmdStart:
		return p.parseCmd()
	case token.LBracket:
		return p.parseArrayLit()
	}
	p.errorf(t.Pos, "unexpected %s in expression", t.Kind)
	// advance to avoid infinite loops on bad input
	if !p.atEnd() {
		p.advance()
	}
	return nil
}

// looksLikeRecordLit peeks past an open brace to decide if the upcoming
// `{...}` is a record literal body. Returns true when the next non-`{` tokens
// are `}` (empty literal) or `Ident :` (first field).
func (p *Parser) looksLikeRecordLit(offsetAfterLBrace int) bool {
	t := p.peekAhead(offsetAfterLBrace)
	if t.Kind == token.RBrace {
		return true
	}
	if t.Kind == token.Ident && p.peekAhead(offsetAfterLBrace+1).Kind == token.Colon {
		return true
	}
	return false
}

func (p *Parser) parseRecordLitBody(lbracePos token.Pos, typeName string, namePos token.Pos) *ast.RecordLit {
	lit := &ast.RecordLit{
		NamePos:  namePos,
		TypeName: typeName,
		LBrace:   lbracePos,
	}
	for p.peek().Kind != token.RBrace && !p.atEnd() {
		nameTok := p.expect(token.Ident, "record literal field")
		p.expect(token.Colon, "record literal field")
		val := p.parseExpr()
		lit.Fields = append(lit.Fields, ast.FieldInit{
			NamePos: nameTok.Pos,
			Name:    nameTok.Value,
			Value:   val,
		})
		if _, ok := p.accept(token.Comma); ok {
			continue
		}
		break
	}
	rb := p.expect(token.RBrace, "record literal")
	lit.RBrace = rb.Pos
	return lit
}

func (p *Parser) parseArrayLit() ast.Expr {
	lb := p.advance() // [
	lit := &ast.ArrayLit{LBracket: lb.Pos}
	if p.peek().Kind == token.RBracket {
		p.advance()
		return lit
	}
	for {
		lit.Elems = append(lit.Elems, p.parseExpr())
		if _, ok := p.accept(token.Comma); !ok {
			break
		}
		// allow trailing comma: `[1, 2, ]`
		if p.peek().Kind == token.RBracket {
			break
		}
	}
	p.expect(token.RBracket, "array literal")
	return lit
}

func (p *Parser) parseString() ast.Expr {
	start := p.advance() // StringStart
	lit := &ast.StringLit{LitPos: start.Pos}
	for {
		t := p.peek()
		switch t.Kind {
		case token.StringPart:
			p.advance()
			if t.Value != "" {
				lit.Parts = append(lit.Parts, &ast.StringChunk{LitPos: t.Pos, Value: t.Value})
			}
		case token.InterpStart:
			p.advance()
			lit.Parts = append(lit.Parts, p.parseExpr())
			p.expect(token.InterpEnd, "string interpolation")
		case token.StringEnd:
			p.advance()
			return lit
		case token.EOF:
			p.errorf(t.Pos, "unexpected end of file in string literal")
			return lit
		default:
			p.errorf(t.Pos, "unexpected %s in string literal", t.Kind)
			p.advance()
		}
	}
}

func (p *Parser) parseCmd() ast.Expr {
	start := p.advance() // CmdStart
	lit := &ast.CmdLit{LitPos: start.Pos}
	for {
		t := p.peek()
		switch t.Kind {
		case token.CmdPart:
			p.advance()
			if t.Value != "" {
				lit.Parts = append(lit.Parts, &ast.StringChunk{LitPos: t.Pos, Value: t.Value})
			}
		case token.InterpStart:
			p.advance()
			lit.Parts = append(lit.Parts, p.parseExpr())
			p.expect(token.InterpEnd, "command interpolation")
		case token.CmdEnd:
			p.advance()
			return lit
		case token.EOF:
			p.errorf(t.Pos, "unexpected end of file in command literal")
			return lit
		default:
			p.errorf(t.Pos, "unexpected %s in command literal", t.Kind)
			p.advance()
		}
	}
}
