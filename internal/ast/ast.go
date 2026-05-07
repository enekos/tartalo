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
//
// Tools and agents are encoded as FuncDecl with Kind = FuncKindTool or
// FuncKindAgent. They share the same call/checking machinery as regular
// functions; the metadata (Description, Budget, Effects) and Kind flag let
// codegen branch on them when building the tool-schema table.
type FuncDecl struct {
	NamePos     token.Pos
	Name        string
	IsExported  bool
	Kind        FuncKind
	Params      []Param
	Result      TypeExpr // may be a TypeName "void"
	Effects     []string // declared effect tags ("net", "fs:read", "ai", ...)
	Description string   // pulled from leading desc("...") in tool/agent body
	Budget      int64    // pulled from leading budget(N); 0 = unset
	Tools       []string // names of tools this agent may invoke (uses clause)
	Body        *Block
}

// FuncKind distinguishes plain functions from tool/agent declarations.
type FuncKind int

const (
	FuncKindPlain FuncKind = iota
	FuncKindTool
	FuncKindAgent
)

func (k FuncKind) String() string {
	switch k {
	case FuncKindTool:
		return "tool"
	case FuncKindAgent:
		return "agent"
	}
	return "func"
}

func (d *FuncDecl) Pos() token.Pos { return d.NamePos }
func (d *FuncDecl) declNode()      {}

// TestDecl: `test "name" { body }`. A top-level test declaration. The body
// runs as a void function in module scope; the harness emitted by codegen
// invokes it from a subshell so failed assertions can exit early.
type TestDecl struct {
	KwPos   token.Pos
	NamePos token.Pos
	Name    string // the literal test name; no interpolation
	Body    *Block
}

func (d *TestDecl) Pos() token.Pos { return d.KwPos }
func (d *TestDecl) declNode()      {}

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

// FuncType: `func(T1, T2, ...): R`. Used as a type annotation when a value
// of function type is being passed around.
type FuncType struct {
	KwPos  token.Pos
	Params []TypeExpr
	Result TypeExpr
}

func (t *FuncType) Pos() token.Pos { return t.KwPos }
func (t *FuncType) typeExprNode()  {}

// RecordType: `{ f1: T1, f2: T2, ... }`. Used as the RHS of a `type` decl.
type RecordType struct {
	LBrace token.Pos
	RBrace token.Pos
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

// SumType: `A{...} | B | C{...}`. Used as the RHS of a `type` decl. Each
// variant either has a list of named fields (`Foo{x:T,y:U}`) or is a unit
// (`Foo` — Fields is nil and HasBraces is false).
type SumType struct {
	KwPos    token.Pos
	Variants []SumVariant
}

func (t *SumType) Pos() token.Pos { return t.KwPos }
func (t *SumType) typeExprNode()  {}

// SumVariant is one alternative in a sum type. Fields is nil when the
// variant is a bare tag with no payload (`Empty`).
type SumVariant struct {
	NamePos   token.Pos
	Name      string
	HasBraces bool
	Fields    []FieldDecl
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
	KwPos  token.Pos
	Var    string
	VarPos token.Pos
	Iter   Expr
	Body   *Block
}

func (s *ForStmt) Pos() token.Pos { return s.KwPos }
func (s *ForStmt) stmtNode()      {}

// DeferStmt: `defer { ... }`. Registers a block to run when the enclosing
// function exits, in last-registered-first-run order. The body cannot
// contain `return`; the checker enforces that.
type DeferStmt struct {
	KwPos token.Pos
	Body  *Block
}

func (s *DeferStmt) Pos() token.Pos { return s.KwPos }
func (s *DeferStmt) stmtNode()      {}

// ParallelStmt: `parallel { task { ... } task { ... } ... }`. Runs every
// inner task concurrently and joins before continuing past the closing
// brace. Tasks see the enclosing scope read-only; the checker rejects
// assignments to outer locals so the sh (subshell) and native (goroutine)
// backends behave identically. Only TaskStmt is allowed inside the body.
type ParallelStmt struct {
	KwPos token.Pos
	Body  *Block // contains *TaskStmt entries (checker enforces)
}

func (s *ParallelStmt) Pos() token.Pos { return s.KwPos }
func (s *ParallelStmt) stmtNode()      {}

// TaskStmt: `task { ... }`. Only valid as a direct child of a ParallelStmt
// body. Behaves like a void function body that runs concurrently with its
// siblings; cannot return, defer, or contain a nested parallel/task.
type TaskStmt struct {
	KwPos token.Pos
	Body  *Block
}

func (s *TaskStmt) Pos() token.Pos { return s.KwPos }
func (s *TaskStmt) stmtNode()      {}

// Block: `{ stmts... }`. RBrace is captured so source-position-based passes
// (formatter, IDE tools) know where the block ends without re-scanning.
type Block struct {
	LBrace token.Pos
	RBrace token.Pos
	Stmts  []Stmt
}

func (b *Block) Pos() token.Pos { return b.LBrace }
func (b *Block) stmtNode()      {}

// MatchStmt: `match expr { pat | pat => stmts ... }`. Codegens to a `case`.
type MatchStmt struct {
	KwPos   token.Pos
	Subject Expr
	Cases   []*MatchCase
	RBrace  token.Pos
	HasDflt bool // true if any case is purely the wildcard `_`
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

// VariantPattern: `Name{ field1, field2 }` or bare `Name`. Each binding
// names a field of the variant; the local introduced shadows the field
// name within the arm's body.
type VariantPattern struct {
	NamePos   token.Pos
	Name      string
	HasBraces bool
	Bindings  []VariantBinding
}

func (p *VariantPattern) Pos() token.Pos { return p.NamePos }
func (p *VariantPattern) patternNode()   {}

// VariantBinding identifies a field of a variant to extract, plus the local
// name to bind it to inside the match arm. In v0 these are always equal
// (`Foo{x}` binds the field `x` to the local `x`).
type VariantBinding struct {
	NamePos token.Pos
	Name    string
}

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

// FloatLit holds the source text of a floating-point literal verbatim. We
// keep the textual form so the codegen can pass it straight to awk without
// worrying about Go's float formatting differing from the user's input.
type FloatLit struct {
	LitPos token.Pos
	Text   string
}

func (e *FloatLit) Pos() token.Pos { return e.LitPos }
func (e *FloatLit) exprNode()      {}

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

// CmdLit is a command literal “ `cmd args ${x}` “ whose value is the trimmed
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

// TryExpr: `expr?`. Operand must be a Result-shaped sum (variants `Ok{value:
// T}` and `Err{error: E}`) inside a function whose return type is a Result-
// shaped sum sharing the same Err type. On Err the enclosing function
// returns immediately propagating the same Err; otherwise the value is the
// Ok variant's payload (T).
type TryExpr struct {
	OpPos   token.Pos
	Operand Expr
}

func (e *TryExpr) Pos() token.Pos { return e.Operand.Pos() }
func (e *TryExpr) exprNode()      {}

// RecordLit: `Name{ f1: e1, f2: e2 }`, optionally with a leading spread:
// `Name{ ...source, f2: override }`. The Spread expression, when non-nil,
// must evaluate to a value of the same record type; explicit Fields override
// the corresponding fields from the spread source.
//
// We use the Go-style typed-literal form rather than bare `{...}` because it
// avoids ambiguity with statement blocks that follow control-flow keywords
// (e.g. `for x in iter { ... }`).
type RecordLit struct {
	NamePos   token.Pos
	TypeName  string
	LBrace    token.Pos
	RBrace    token.Pos
	SpreadPos token.Pos // position of `...` if Spread != nil
	Spread    Expr      // optional source record to copy fields from; nil if absent
	Fields    []FieldInit
}

func (e *RecordLit) Pos() token.Pos { return e.NamePos }
func (e *RecordLit) exprNode()      {}

// FieldInit: `name: value` inside a record literal.
type FieldInit struct {
	NamePos token.Pos
	Name    string
	Value   Expr
}

// CastExpr: `expr as TypeName`. Performs an explicit type conversion. For v0
// the only meaningful cast is between record types whose shapes are
// compatible (target's field set is a subset of the source's, with each
// shared field's type assignable from source to target).
type CastExpr struct {
	KwPos   token.Pos
	Operand Expr
	TypeAnn TypeExpr
}

func (e *CastExpr) Pos() token.Pos { return e.Operand.Pos() }
func (e *CastExpr) exprNode()      {}

// FuncLit is an anonymous function literal — a lambda usable in any
// expression position. Same shape as FuncDecl but without a name; the
// codegen hoists each FuncLit to a uniquely-named top-level function. The
// FreeVars slice is populated by the checker with names referenced inside
// the body that resolve to a binding in the enclosing scope (excluding
// globals and other top-level function references). Sh codegen rejects a
// FuncLit with non-empty FreeVars; the native target captures naturally
// via Go's closure semantics.
type FuncLit struct {
	KwPos    token.Pos
	Params   []Param
	Result   TypeExpr
	Body     *Block
	FreeVars []string
}

func (e *FuncLit) Pos() token.Pos { return e.KwPos }
func (e *FuncLit) exprNode()      {}
