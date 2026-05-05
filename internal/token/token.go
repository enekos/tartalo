package token

import "fmt"

type Kind int

const (
	Illegal Kind = iota
	EOF

	Ident
	Int

	// strings are always emitted as: StringStart StringPart [InterpStart ...expr... InterpEnd StringPart]* StringEnd
	StringStart
	StringPart
	StringEnd

	// command literals: CmdStart CmdPart [InterpStart ...expr... InterpEnd CmdPart]* CmdEnd
	CmdStart
	CmdPart
	CmdEnd

	// keywords
	Let
	Const
	Func
	Return
	If
	Else
	For
	In
	Match
	Type
	Import
	Export
	Null
	True
	False
	TyString
	TyNumber
	TyBool
	TyVoid

	// operators / punctuation
	Assign  // =
	Plus    // +
	Minus   // -
	Star    // *
	Slash   // /
	Percent // %
	Eq      // ==
	Neq     // !=
	Lt      // <
	Lte     // <=
	Gt      // >
	Gte     // >=
	AndAnd   // &&
	OrOr     // ||
	Coalesce // ?? (null-coalesce)
	Question // ? (postfix T?)
	Pipe     // | (single, used by match patterns)
	Arrow    // => (match arm)
	Bang     // !
	LParen
	RParen
	LBrace
	RBrace
	LBracket
	RBracket
	Comma
	Colon
	Semicolon
	Dot    // .
	DotDot // ..

	// string interpolation framing
	InterpStart // ${
	InterpEnd   // } closing an interpolation
)

var kindNames = map[Kind]string{
	Illegal: "ILLEGAL", EOF: "EOF",
	Ident: "IDENT", Int: "INT",
	StringStart: "STR_START", StringPart: "STR_PART", StringEnd: "STR_END",
	CmdStart: "CMD_START", CmdPart: "CMD_PART", CmdEnd: "CMD_END",
	Let: "let", Const: "const", Func: "func", Return: "return",
	If: "if", Else: "else", For: "for", In: "in", Match: "match", Type: "type",
	Import: "import", Export: "export",
	Null: "null", True: "true", False: "false",
	TyString: "string", TyNumber: "number", TyBool: "bool", TyVoid: "void",
	Assign: "=", Plus: "+", Minus: "-", Star: "*", Slash: "/", Percent: "%",
	Eq: "==", Neq: "!=", Lt: "<", Lte: "<=", Gt: ">", Gte: ">=",
	AndAnd: "&&", OrOr: "||", Coalesce: "??", Question: "?",
	Pipe: "|", Arrow: "=>", Bang: "!",
	LParen: "(", RParen: ")", LBrace: "{", RBrace: "}",
	LBracket: "[", RBracket: "]",
	Comma: ",", Colon: ":", Semicolon: ";", Dot: ".", DotDot: "..",
	InterpStart: "${", InterpEnd: "}",
}

func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return fmt.Sprintf("Kind(%d)", int(k))
}

// Pos is a 1-based line/column pair plus the file the token came from.
type Pos struct {
	File string
	Line int
	Col  int
}

func (p Pos) String() string {
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

type Token struct {
	Kind  Kind
	Value string // identifier name, raw int text, decoded string chunk, etc.
	Pos   Pos
}

func (t Token) String() string {
	if t.Value != "" {
		return fmt.Sprintf("%s(%q)@%s", t.Kind, t.Value, t.Pos)
	}
	return fmt.Sprintf("%s@%s", t.Kind, t.Pos)
}

var Keywords = map[string]Kind{
	"let":    Let,
	"const":  Const,
	"func":   Func,
	"return": Return,
	"if":     If,
	"else":   Else,
	"for":    For,
	"in":     In,
	"match":  Match,
	"type":   Type,
	"import": Import,
	"export": Export,
	"null":   Null,
	"true":   True,
	"false":  False,
	"string": TyString,
	"number": TyNumber,
	"bool":   TyBool,
	"void":   TyVoid,
}
