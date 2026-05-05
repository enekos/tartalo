// Package ast defines the syntax tree produced by the parser.
//
// The tree is intentionally pure structure: type information is computed by the
// checker and stored separately in a TypeInfo side table keyed by node pointer.
package ast

import "github.com/enekos/tartalo/internal/token"

// Node is the root interface for every AST node.
type Node interface {
	Pos() token.Pos
}

// File is the top-level container: one source file's worth of declarations.
type File struct {
	Path    string
	Imports []*ImportDecl
	Decls   []Decl
}

func (f *File) Pos() token.Pos {
	if len(f.Imports) > 0 {
		return f.Imports[0].Pos()
	}
	if len(f.Decls) > 0 {
		return f.Decls[0].Pos()
	}
	return token.Pos{File: f.Path, Line: 1, Col: 1}
}

// ImportDecl: `import { a, b } from "./util.tt"`. v0 requires the named-import
// form so every imported symbol is explicit.
type ImportDecl struct {
	KwPos   token.Pos
	Names   []ImportName
	PathPos token.Pos
	Path    string // the unquoted path string
}

func (d *ImportDecl) Pos() token.Pos { return d.KwPos }

// ImportName: one entry inside the import braces.
type ImportName struct {
	NamePos token.Pos
	Name    string
}

// --- Declarations -----------------------------------------------------------

type Decl interface {
	Node
	declNode()
}

// VarDecl: `let x: T = expr` or `const x: T = expr`.
type VarDecl struct {
	NamePos    token.Pos
	Name       string
	IsConst    bool
	IsExported bool
	TypeAnn    TypeExpr // optional; inferred when nil
	Value      Expr     // required in v0
}

func (d *VarDecl) Pos() token.Pos { return d.NamePos }
func (d *VarDecl) declNode()      {}

// FuncDecl: `func name(params): ret { body }`.
type FuncDecl struct {
	NamePos    token.Pos
	Name       string
	IsExported bool
	Params     []Param
	Result     TypeExpr // may be a TypeName "void"
	Body       *Block
}

func (d *FuncDecl) Pos() token.Pos { return d.NamePos }
func (d *FuncDecl) declNode()      {}

// TypeDecl: `type Name = TypeExpr`. v0 only allows record types on the RHS.
type TypeDecl struct {
	NamePos    token.Pos
	Name       string
	IsExported bool
	Spec       TypeExpr
}

func (d *TypeDecl) Pos() token.Pos { return d.NamePos }
func (d *TypeDecl) declNode()      {}

type Param struct {
	NamePos token.Pos
	Name    string
	TypeAnn TypeExpr
}

// --- Type expressions -------------------------------------------------------

type TypeExpr interface {
	Node
	typeExprNode()
}

// TypeName: a primitive type reference like `string`, `number`, `bool`, `void`.
type TypeName struct {
	NamePos token.Pos
	Name    string
}

func (t *TypeName) Pos() token.Pos { return t.NamePos }
func (t *TypeName) typeExprNode()  {}

// ArrayType: `T[]`.
type ArrayType struct {
	LBracket token.Pos
	Elem     TypeExpr
}

func (t *ArrayType) Pos() token.Pos { return t.Elem.Pos() }
func (t *ArrayType) typeExprNode()  {}

// OptionalType: `T?`. The wrapped element type cannot itself be optional in
// v0; the checker enforces that.
type OptionalType struct {
	QPos token.Pos
	Elem TypeExpr
}

func (t *OptionalType) Pos() token.Pos { return t.Elem.Pos() }
func (t *OptionalType) typeExprNode()  {}

// RecordType: `{ f1: T1, f2: T2, ... }`. Used as the RHS of a `type` decl.
type RecordType struct {
	LBrace token.Pos
	Fields []FieldDecl
}

func (t *RecordType) Pos() token.Pos { return t.LBrace }
func (t *RecordType) typeExprNode()  {}

// FieldDecl describes one field in a record type definition.
type FieldDecl struct {
	NamePos token.Pos
	Name    string
	TypeAnn TypeExpr
}

// --- Statements -------------------------------------------------------------

type Stmt interface {
	Node
	stmtNode()
}

// DeclStmt wraps a var declaration so it can appear inside a block.
type DeclStmt struct {
	Decl *VarDecl
}

func (s *DeclStmt) Pos() token.Pos { return s.Decl.Pos() }
func (s *DeclStmt) stmtNode()      {}

// ExprStmt is a top-level expression executed for its side effects (a call,
// or a command literal).
type ExprStmt struct {
	X Expr
}

func (s *ExprStmt) Pos() token.Pos { return s.X.Pos() }
func (s *ExprStmt) stmtNode()      {}

// AssignStmt: `name = expr`.
type AssignStmt struct {
	NamePos token.Pos
	Name    string
	Value   Expr
}

func (s *AssignStmt) Pos() token.Pos { return s.NamePos }
func (s *AssignStmt) stmtNode()      {}

// FieldAssignStmt: `target.field = expr`.
type FieldAssignStmt struct {
	Target  Expr
	NamePos token.Pos
	Name    string
	Value   Expr
}

func (s *FieldAssignStmt) Pos() token.Pos { return s.Target.Pos() }
func (s *FieldAssignStmt) stmtNode()      {}

// ReturnStmt: `return [expr]`.
type ReturnStmt struct {
	KwPos token.Pos
	Value Expr // may be nil
}

func (s *ReturnStmt) Pos() token.Pos { return s.KwPos }
func (s *ReturnStmt) stmtNode()      {}

// IfStmt: `if cond { ... } else { ... }`.
// `else if` is represented by Else being a Block containing a single IfStmt.
type IfStmt struct {
	KwPos token.Pos
	Cond  Expr
	Then  *Block
	Else  *Block // may be nil
}

func (s *IfStmt) Pos() token.Pos { return s.KwPos }
func (s *IfStmt) stmtNode()      {}

// ForStmt: `for x in iter { ... }`. Iter is either a RangeExpr (numeric) or a
// CmdLit (line-by-line) or any other Expr that resolves to a string at v0
// (lines split on newline).
type ForStmt struct {
	KwPos token.Pos
	Var   string
	VarPos token.Pos
	Iter  Expr
	Body  *Block
}

func (s *ForStmt) Pos() token.Pos { return s.KwPos }
func (s *ForStmt) stmtNode()      {}

// Block: `{ stmts... }`.
type Block struct {
	LBrace token.Pos
	Stmts  []Stmt
}

func (b *Block) Pos() token.Pos { return b.LBrace }
func (b *Block) stmtNode()      {}

// MatchStmt: `match expr { pat | pat => stmts ... }`. Codegens to a `case`.
type MatchStmt struct {
	KwPos    token.Pos
	Subject  Expr
	Cases    []*MatchCase
	HasDflt  bool // true if any case is purely the wildcard `_`
}

func (s *MatchStmt) Pos() token.Pos { return s.KwPos }
func (s *MatchStmt) stmtNode()      {}

// MatchCase: a list of patterns (alternatives) plus a body.
type MatchCase struct {
	Patterns []Pattern
	ArrowPos token.Pos
	Body     *Block
}

// Pattern is what appears on the LHS of `=>` in a match arm.
type Pattern interface {
	Node
	patternNode()
}

// LiteralPattern: an int, bool, or string literal pattern.
type LiteralPattern struct {
	Lit Expr // *IntLit | *BoolLit | *StringLit
}

func (p *LiteralPattern) Pos() token.Pos { return p.Lit.Pos() }
func (p *LiteralPattern) patternNode()   {}

// WildcardPattern: `_`. Matches anything, contributes a default case.
type WildcardPattern struct {
	NamePos token.Pos
}

func (p *WildcardPattern) Pos() token.Pos { return p.NamePos }
func (p *WildcardPattern) patternNode()   {}

// --- Expressions ------------------------------------------------------------

type Expr interface {
	Node
	exprNode()
}

// Ident: a variable or function reference.
type Ident struct {
	NamePos token.Pos
	Name    string
}

func (e *Ident) Pos() token.Pos { return e.NamePos }
func (e *Ident) exprNode()      {}

type IntLit struct {
	LitPos token.Pos
	Value  int64
}

func (e *IntLit) Pos() token.Pos { return e.LitPos }
func (e *IntLit) exprNode()      {}

type BoolLit struct {
	LitPos token.Pos
	Value  bool
}

func (e *BoolLit) Pos() token.Pos { return e.LitPos }
func (e *BoolLit) exprNode()      {}

// NullLit: `null`. Has the (synthetic) "untyped null" type; assignable to any
// optional. Comparison against null is the only op directly allowed on it.
type NullLit struct {
	LitPos token.Pos
}

func (e *NullLit) Pos() token.Pos { return e.LitPos }
func (e *NullLit) exprNode()      {}

// StringLit is composed of alternating literal chunks and embedded expressions.
// For "hello ${who}!" the parts are ["hello ", who, "!"]; chunks are *StringChunk.
type StringLit struct {
	LitPos token.Pos
	Parts  []Expr // each is either *StringChunk or any other Expr
}

func (e *StringLit) Pos() token.Pos { return e.LitPos }
func (e *StringLit) exprNode()      {}

// StringChunk is a literal piece of text inside a StringLit. We keep it as an
// Expr so a StringLit can be a heterogeneous slice.
type StringChunk struct {
	LitPos token.Pos
	Value  string
}

func (e *StringChunk) Pos() token.Pos { return e.LitPos }
func (e *StringChunk) exprNode()      {}

// CmdLit is a command literal `` `cmd args ${x}` `` whose value is the trimmed
// stdout of the command. Composed the same way as StringLit.
type CmdLit struct {
	LitPos token.Pos
	Parts  []Expr // *StringChunk or any expression to interpolate
}

func (e *CmdLit) Pos() token.Pos { return e.LitPos }
func (e *CmdLit) exprNode()      {}

// BinaryExpr: `lhs op rhs`.
type BinaryExpr struct {
	OpPos token.Pos
	Op    token.Kind
	Lhs   Expr
	Rhs   Expr
}

func (e *BinaryExpr) Pos() token.Pos { return e.OpPos }
func (e *BinaryExpr) exprNode()      {}

// UnaryExpr: `op operand`. Only `-` and `!` in v0.
type UnaryExpr struct {
	OpPos   token.Pos
	Op      token.Kind
	Operand Expr
}

func (e *UnaryExpr) Pos() token.Pos { return e.OpPos }
func (e *UnaryExpr) exprNode()      {}

// CallExpr: `callee(args)`. Callee is currently always an Ident in v0.
type CallExpr struct {
	LParenPos token.Pos
	Callee    Expr
	Args      []Expr
}

func (e *CallExpr) Pos() token.Pos { return e.Callee.Pos() }
func (e *CallExpr) exprNode()      {}

// RangeExpr: `start..end`. Only valid as a for-in iterator in v0.
type RangeExpr struct {
	OpPos token.Pos
	Start Expr
	End   Expr
}

func (e *RangeExpr) Pos() token.Pos { return e.Start.Pos() }
func (e *RangeExpr) exprNode()      {}

// ArrayLit: `[a, b, c]`.
type ArrayLit struct {
	LBracket token.Pos
	Elems    []Expr
}

func (e *ArrayLit) Pos() token.Pos { return e.LBracket }
func (e *ArrayLit) exprNode()      {}

// IndexExpr: `target[index]`.
type IndexExpr struct {
	LBracket token.Pos
	Target   Expr
	Index    Expr
}

func (e *IndexExpr) Pos() token.Pos { return e.Target.Pos() }
func (e *IndexExpr) exprNode()      {}

// FieldExpr: `target.field`.
type FieldExpr struct {
	DotPos  token.Pos
	Target  Expr
	NamePos token.Pos
	Name    string
}

func (e *FieldExpr) Pos() token.Pos { return e.Target.Pos() }
func (e *FieldExpr) exprNode()      {}

// CoalesceExpr: `lhs ?? rhs`. Lhs must be `T?`, rhs must be `T`; result is `T`.
type CoalesceExpr struct {
	OpPos token.Pos
	Lhs   Expr
	Rhs   Expr
}

func (e *CoalesceExpr) Pos() token.Pos { return e.Lhs.Pos() }
func (e *CoalesceExpr) exprNode()      {}

// UnwrapExpr: `expr!`. Operand must be `T?`; result is `T`. Aborts the script
// at runtime if the operand was null.
type UnwrapExpr struct {
	OpPos   token.Pos
	Operand Expr
}

func (e *UnwrapExpr) Pos() token.Pos { return e.Operand.Pos() }
func (e *UnwrapExpr) exprNode()      {}

// RecordLit: `Name{ f1: e1, f2: e2 }`. We use the Go-style typed-literal form
// rather than bare `{...}` because it avoids ambiguity with statement blocks
// that follow control-flow keywords (e.g. `for x in iter { ... }`).
type RecordLit struct {
	NamePos  token.Pos
	TypeName string
	LBrace   token.Pos
	Fields   []FieldInit
}

func (e *RecordLit) Pos() token.Pos { return e.NamePos }
func (e *RecordLit) exprNode()      {}

// FieldInit: `name: value` inside a record literal.
type FieldInit struct {
	NamePos token.Pos
	Name    string
	Value   Expr
}
