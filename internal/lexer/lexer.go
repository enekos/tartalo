// Package lexer converts source text into a stream of tokens for the parser.
//
// Strings and command literals embed expressions (`${...}`), so the lexer keeps a
// small mode stack: while inside a string or command, it scans literal chunks
// and switches back to "code" mode for the duration of an interpolation.
package lexer

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/enekos/tartalo/internal/token"
)

type mode int

const (
	modeCode mode = iota
	modeString
	modeCmd
)

type frame struct {
	mode    mode
	// For modeCode frames started by `${`, braceDepth tracks nested `{` so that
	// the matching `}` ends the interpolation rather than a block.
	braceDepth int
}

type Lexer struct {
	file    string
	src     string
	pos     int // byte offset of next rune to consume
	line    int
	col     int
	stack   []frame
	tokens  []token.Token
	errs    []error
}

func New(file, src string) *Lexer {
	return &Lexer{
		file:  file,
		src:   src,
		line:  1,
		col:   1,
		stack: []frame{{mode: modeCode}},
	}
}

// Tokenize returns the full token stream for the source. Errors are collected
// and returned alongside whatever tokens were produced; callers may choose to
// proceed with parsing for IDE-style use cases.
func (l *Lexer) Tokenize() ([]token.Token, []error) {
	for !l.atEOF() {
		switch l.top().mode {
		case modeCode:
			l.lexCode()
		case modeString:
			l.lexStringChunk()
		case modeCmd:
			l.lexCmdChunk()
		}
	}
	if len(l.stack) > 1 {
		l.errorf(l.currentPos(), "unexpected end of file inside string or command literal")
	}
	l.emit(token.EOF, "")
	return l.tokens, l.errs
}

func (l *Lexer) top() *frame { return &l.stack[len(l.stack)-1] }

func (l *Lexer) push(m mode)  { l.stack = append(l.stack, frame{mode: m}) }
func (l *Lexer) pop()         { l.stack = l.stack[:len(l.stack)-1] }

func (l *Lexer) atEOF() bool { return l.pos >= len(l.src) }

func (l *Lexer) peek() byte {
	if l.atEOF() {
		return 0
	}
	return l.src[l.pos]
}

func (l *Lexer) peekAt(n int) byte {
	if l.pos+n >= len(l.src) {
		return 0
	}
	return l.src[l.pos+n]
}

func (l *Lexer) advance() byte {
	c := l.src[l.pos]
	l.pos++
	if c == '\n' {
		l.line++
		l.col = 1
	} else {
		l.col++
	}
	return c
}

func (l *Lexer) currentPos() token.Pos {
	return token.Pos{File: l.file, Line: l.line, Col: l.col}
}

func (l *Lexer) emitAt(k token.Kind, val string, p token.Pos) {
	l.tokens = append(l.tokens, token.Token{Kind: k, Value: val, Pos: p})
}

func (l *Lexer) emit(k token.Kind, val string) {
	l.emitAt(k, val, l.currentPos())
}

func (l *Lexer) errorf(p token.Pos, format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf("%s: %s", p, fmt.Sprintf(format, args...)))
}

func (l *Lexer) lexCode() {
	for !l.atEOF() {
		// If we're inside an interpolation `${...}`, the matching `}` ends it.
		f := l.top()
		c := l.peek()

		// whitespace and comments
		if c == ' ' || c == '\t' || c == '\r' || c == '\n' {
			l.advance()
			continue
		}
		if c == '/' && l.peekAt(1) == '/' {
			for !l.atEOF() && l.peek() != '\n' {
				l.advance()
			}
			continue
		}

		// closing an interpolation?
		if c == '}' && f.braceDepth == 0 && len(l.stack) > 1 {
			p := l.currentPos()
			l.advance()
			l.emitAt(token.InterpEnd, "", p)
			l.pop() // back to enclosing string/cmd
			return
		}

		// string opener
		if c == '"' {
			p := l.currentPos()
			l.advance()
			l.emitAt(token.StringStart, "", p)
			l.push(modeString)
			return
		}

		// command literal opener
		if c == '`' {
			p := l.currentPos()
			l.advance()
			l.emitAt(token.CmdStart, "", p)
			l.push(modeCmd)
			return
		}

		// identifiers / keywords
		if isIdentStart(c) {
			l.lexIdent()
			continue
		}

		// numbers
		if c >= '0' && c <= '9' {
			l.lexNumber()
			continue
		}

		// punctuation / operators
		l.lexPunct()
		// after lexPunct, brace depth tracking for interpolations:
		// (handled inside lexPunct to keep dispatch simple)
		// loop continues
	}
}

func (l *Lexer) lexIdent() {
	p := l.currentPos()
	start := l.pos
	for !l.atEOF() && isIdentCont(l.peek()) {
		l.advance()
	}
	name := l.src[start:l.pos]
	if k, ok := token.Keywords[name]; ok {
		l.emitAt(k, name, p)
		return
	}
	l.emitAt(token.Ident, name, p)
}

func (l *Lexer) lexNumber() {
	p := l.currentPos()
	start := l.pos
	for !l.atEOF() && l.peek() >= '0' && l.peek() <= '9' {
		l.advance()
	}
	// Float? `123.45` (we require a digit after the dot so `0..10` still
	// parses as Int + DotDot + Int, the range syntax).
	isFloat := false
	if l.peek() == '.' && l.peekAt(1) >= '0' && l.peekAt(1) <= '9' {
		isFloat = true
		l.advance() // consume '.'
		for !l.atEOF() && l.peek() >= '0' && l.peek() <= '9' {
			l.advance()
		}
	}
	// Optional exponent: `e+10`, `E-3`, `e2`.
	if l.peek() == 'e' || l.peek() == 'E' {
		// Lookahead to confirm the exponent — otherwise we'd swallow the `e`
		// of an identifier that happens to start at this position (unlikely
		// because we already lexed digits, but defensive).
		j := 1
		if l.peekAt(j) == '+' || l.peekAt(j) == '-' {
			j++
		}
		if l.peekAt(j) >= '0' && l.peekAt(j) <= '9' {
			isFloat = true
			l.advance() // e
			if l.peek() == '+' || l.peek() == '-' {
				l.advance()
			}
			for !l.atEOF() && l.peek() >= '0' && l.peek() <= '9' {
				l.advance()
			}
		}
	}
	if isFloat {
		l.emitAt(token.Float, l.src[start:l.pos], p)
	} else {
		l.emitAt(token.Int, l.src[start:l.pos], p)
	}
}

func (l *Lexer) lexPunct() {
	p := l.currentPos()
	c := l.advance()
	switch c {
	case '+':
		l.emitAt(token.Plus, "+", p)
	case '-':
		l.emitAt(token.Minus, "-", p)
	case '*':
		l.emitAt(token.Star, "*", p)
	case '/':
		l.emitAt(token.Slash, "/", p)
	case '%':
		l.emitAt(token.Percent, "%", p)
	case '(':
		l.emitAt(token.LParen, "(", p)
	case ')':
		l.emitAt(token.RParen, ")", p)
	case '{':
		l.top().braceDepth++
		l.emitAt(token.LBrace, "{", p)
	case '}':
		l.top().braceDepth--
		l.emitAt(token.RBrace, "}", p)
	case '[':
		l.emitAt(token.LBracket, "[", p)
	case ']':
		l.emitAt(token.RBracket, "]", p)
	case ',':
		l.emitAt(token.Comma, ",", p)
	case ':':
		l.emitAt(token.Colon, ":", p)
	case ';':
		l.emitAt(token.Semicolon, ";", p)
	case '=':
		switch l.peek() {
		case '=':
			l.advance()
			l.emitAt(token.Eq, "==", p)
		case '>':
			l.advance()
			l.emitAt(token.Arrow, "=>", p)
		default:
			l.emitAt(token.Assign, "=", p)
		}
	case '!':
		if l.peek() == '=' {
			l.advance()
			l.emitAt(token.Neq, "!=", p)
		} else {
			l.emitAt(token.Bang, "!", p)
		}
	case '<':
		if l.peek() == '=' {
			l.advance()
			l.emitAt(token.Lte, "<=", p)
		} else {
			l.emitAt(token.Lt, "<", p)
		}
	case '>':
		if l.peek() == '=' {
			l.advance()
			l.emitAt(token.Gte, ">=", p)
		} else {
			l.emitAt(token.Gt, ">", p)
		}
	case '&':
		if l.peek() == '&' {
			l.advance()
			l.emitAt(token.AndAnd, "&&", p)
		} else {
			l.errorf(p, "unexpected character '&' (did you mean '&&'?)")
			l.emitAt(token.Illegal, "&", p)
		}
	case '|':
		if l.peek() == '|' {
			l.advance()
			l.emitAt(token.OrOr, "||", p)
		} else {
			l.emitAt(token.Pipe, "|", p)
		}
	case '.':
		if l.peek() == '.' {
			l.advance()
			l.emitAt(token.DotDot, "..", p)
		} else {
			l.emitAt(token.Dot, ".", p)
		}
	case '?':
		if l.peek() == '?' {
			l.advance()
			l.emitAt(token.Coalesce, "??", p)
		} else {
			l.emitAt(token.Question, "?", p)
		}
	default:
		if c >= 0x80 {
			// future: real UTF-8 handling; for now, only ASCII identifiers are valid.
			l.errorf(p, "unexpected non-ASCII byte 0x%02x", c)
		} else if unicode.IsPrint(rune(c)) {
			l.errorf(p, "unexpected character %q", c)
		} else {
			l.errorf(p, "unexpected byte 0x%02x", c)
		}
		l.emitAt(token.Illegal, string(c), p)
	}
}

func (l *Lexer) lexStringChunk() {
	startPos := l.currentPos()
	var b strings.Builder
	for !l.atEOF() {
		c := l.peek()
		switch c {
		case '"':
			l.emitAt(token.StringPart, b.String(), startPos)
			endPos := l.currentPos()
			l.advance()
			l.emitAt(token.StringEnd, "", endPos)
			l.pop()
			return
		case '\\':
			l.advance()
			if l.atEOF() {
				l.errorf(l.currentPos(), "unterminated escape sequence")
				return
			}
			esc := l.advance()
			switch esc {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '\\':
				b.WriteByte('\\')
			case '"':
				b.WriteByte('"')
			case '$':
				b.WriteByte('$')
			case '`':
				b.WriteByte('`')
			default:
				l.errorf(l.currentPos(), "unknown escape \\%c", esc)
			}
		case '$':
			if l.peekAt(1) == '{' {
				// flush current chunk, enter interpolation
				l.emitAt(token.StringPart, b.String(), startPos)
				p := l.currentPos()
				l.advance() // $
				l.advance() // {
				l.emitAt(token.InterpStart, "${", p)
				l.push(modeCode)
				return
			}
			b.WriteByte(l.advance())
		case '\n':
			l.errorf(l.currentPos(), "newline in string literal (use \\n)")
			b.WriteByte(l.advance())
		default:
			b.WriteByte(l.advance())
		}
	}
	l.errorf(l.currentPos(), "unterminated string literal")
}

func (l *Lexer) lexCmdChunk() {
	startPos := l.currentPos()
	var b strings.Builder
	for !l.atEOF() {
		c := l.peek()
		switch c {
		case '`':
			l.emitAt(token.CmdPart, b.String(), startPos)
			endPos := l.currentPos()
			l.advance()
			l.emitAt(token.CmdEnd, "", endPos)
			l.pop()
			return
		case '\\':
			// escape sequences inside command literals are passed through verbatim,
			// except \` and \\ which let you embed a backtick or backslash.
			l.advance()
			if l.atEOF() {
				l.errorf(l.currentPos(), "unterminated escape in command literal")
				return
			}
			esc := l.advance()
			switch esc {
			case '`':
				b.WriteByte('`')
			case '\\':
				b.WriteByte('\\')
			case '$':
				b.WriteByte('$')
			default:
				b.WriteByte('\\')
				b.WriteByte(esc)
			}
		case '$':
			if l.peekAt(1) == '{' {
				l.emitAt(token.CmdPart, b.String(), startPos)
				p := l.currentPos()
				l.advance()
				l.advance()
				l.emitAt(token.InterpStart, "${", p)
				l.push(modeCode)
				return
			}
			b.WriteByte(l.advance())
		default:
			b.WriteByte(l.advance())
		}
	}
	l.errorf(l.currentPos(), "unterminated command literal")
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentCont(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}
