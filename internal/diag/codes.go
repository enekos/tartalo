package diag

import "strings"

// Stable diagnostic codes. These are the agent/editor-facing identifiers; the
// human-readable message can change between releases, but a code does not.
// Add codes by appending — never reuse a retired number.
//
// Categories (TT-XYZNNN):
//
//	LEX  lexer (unterminated literal, bad escape, stray byte)
//	PAR  parser (syntax)
//	IMP  import resolution / cycles / no-export
//	NAM  names: undeclared, duplicate, redeclaration
//	TYP  types: mismatch, unsupported form
//	OPT  optionals / null
//	FLD  record fields
//	VAR  sum / variants
//	MAP  map operations
//	CALL call-site arity / argument
//	CTL  control flow (break / continue / return / defer placement)
//	MUT  assignment / mutability
//	RNG  for-range / iter
//	GEN  generic functions / type parameters
//	RES  Result `?` operator
//	CON  concurrency (parallel / task / spawn / chan)
//	CST  `as` cast
//	INF  type inference
//	SPRD record spread
//	MCK  test-only / mocks
//	UNS  "v0 does not support X" intentional limitations
const (
	CodeLex          = "TT-LEX001"
	CodeParse        = "TT-PAR001"
	CodeImport       = "TT-IMP001"
	CodeImportCycle  = "TT-IMP002"
	CodeNoExport     = "TT-IMP003"
	CodeUndeclared   = "TT-NAM001"
	CodeDuplicate    = "TT-NAM002"
	CodeRedecl       = "TT-NAM003"
	CodeTypeMismatch = "TT-TYP001"
	CodeBadTypeExpr  = "TT-TYP002"
	CodeOptional     = "TT-OPT001"
	CodeField        = "TT-FLD001"
	CodeVariant      = "TT-VAR001"
	CodeMap          = "TT-MAP001"
	CodeCall         = "TT-CALL001"
	CodeControlFlow  = "TT-CTL001"
	CodeMut          = "TT-MUT001"
	CodeRange        = "TT-RNG001"
	CodeGeneric      = "TT-GEN001"
	CodeResult       = "TT-RES001"
	CodeConcurrency  = "TT-CON001"
	CodeCast         = "TT-CST001"
	CodeInfer        = "TT-INF001"
	CodeSpread       = "TT-SPRD001"
	CodeMockOnly     = "TT-MCK001"
	CodeUnsupported  = "TT-UNS001"
)

// AllCodes returns every stable code declared above. Used by `tartalo explain
// --list` and by the explain table's coverage test.
func AllCodes() []string {
	return []string{
		CodeLex, CodeParse,
		CodeImport, CodeImportCycle, CodeNoExport,
		CodeUndeclared, CodeDuplicate, CodeRedecl,
		CodeTypeMismatch, CodeBadTypeExpr,
		CodeOptional, CodeField, CodeVariant, CodeMap,
		CodeCall, CodeControlFlow, CodeMut, CodeRange,
		CodeGeneric, CodeResult, CodeConcurrency,
		CodeCast, CodeInfer, CodeSpread, CodeMockOnly,
		CodeUnsupported,
	}
}

// InferCode classifies a diagnostic message into one of the stable codes when
// the producer didn't set Diag.Code explicitly. Substring pattern based and
// intentionally ordered — first match wins, more specific rules go above more
// general ones. Producers can opt out by setting Code explicitly with
// WithCode.
//
// Patterns are matched against the lowercased message. Keep them in sync with
// the messages produced by internal/checker, internal/parser, internal/lexer,
// and internal/loader.
func InferCode(msg string) string {
	if msg == "" {
		return ""
	}
	m := strings.ToLower(msg)

	// --- concurrency (these mention task/parallel/spawn/chan/send/recv) ---
	if has(m,
		"task block", "task can only appear", "task {",
		"parallel block", "parallel cannot be nested", "parallel is only valid",
		"spawn does not", "spawn target", "spawn is only valid", "spawn requires",
		"chan expects", "chan: ", "send expects", "send: ",
		"recv expects", "recv: ",
		"closechan", "waitall",
		"from inside a task", "outer-scope record",
		"is not allowed inside a task",
		"return is not allowed inside a task",
		"defer is not allowed inside a task",
	) {
		return CodeConcurrency
	}

	// --- test-only / mocks ---
	if has(m,
		"may only be called inside a `test",
		"may only be called inside an `eval",
		"mockexec", "mockfetch", "mockreadfile", "mocklistdir",
		"mockwritefile", "mockmkdir", "mockstat", "mockenv", "mocknow",
		"mockargs", "mockreadstdin", "mockremovefile", "mockappendfile",
		"mocksleep", "mockisfile", "mockisdir", "mockexists",
	) {
		return CodeMockOnly
	}

	// --- import / module surface ---
	if has(m, "import cycle", "cyclic import") {
		return CodeImportCycle
	}
	if has(m, "no exported name", "is not exported", "has no exported") {
		return CodeNoExport
	}
	if has(m, "imports are not", "cannot import", "module path", "unresolved import") {
		return CodeImport
	}

	// --- Result `?` operator ---
	if has(m, "result-shaped", "? requires", "? is only valid",
		"ok/err") {
		return CodeResult
	}

	// --- generics ---
	if has(m, "type parameter", "generic function", "generic call",
		"type argument", "monomorph") {
		return CodeGeneric
	}

	// --- spread ---
	if has(m, "spread", "...source") {
		return CodeSpread
	}

	// --- cast (`as`) ---
	if has(m, "`as`", "cast to", "cast from",
		"as type", "cast is not allowed") {
		return CodeCast
	}

	// --- range / iter ---
	if has(m, "range start", "range end", "range step",
		"range expression is only", "for-in", "iterable") {
		return CodeRange
	}

	// --- control flow ---
	if has(m,
		"break is only valid", "continue is only valid",
		"return is not allowed", "void function cannot return",
		"function returns ", "return statement has no value",
		"defer is only valid", "defer is not allowed",
		"? is only valid inside a function body",
	) {
		return CodeControlFlow
	}

	// --- mutability / assignment ---
	if has(m, "cannot assign to const", "cannot assign to function",
		"cannot assign to outer-scope", "cannot mutate field of outer-scope",
		"assignment to ") {
		return CodeMut
	}

	// --- optionals ---
	if has(m,
		"bare null", "`null`", "compare %s to null", "compare to null",
		"only optional types are nullable",
		"forced unwrap", "optional operand", "optional values directly",
		"?? requires", "?? right-hand side", "?? type mismatch",
	) {
		return CodeOptional
	}

	// --- record fields ---
	if has(m, "duplicate field", "unknown field", "missing field",
		"has no field", "record literal is missing field",
		"field access requires", "field assignment requires") {
		return CodeField
	}

	// --- sum / variants ---
	if has(m, "variant", "sum type", "must be a sum",
		"not part of sum", "unit variant") {
		return CodeVariant
	}

	// --- maps ---
	if has(m, "map<", "mapnew", "mapget", "mapset", "mapdelete",
		"maphas", "mapkeys", "mapvalues", "maplen",
		"must be a map", "map type from context") {
		return CodeMap
	}

	// --- names (order matters: redecl → duplicate → undeclared) ---
	if has(m, "redeclaration", "cannot redeclare", "redeclare predeclared") {
		return CodeRedecl
	}
	if has(m, "duplicate ") {
		return CodeDuplicate
	}
	if has(m, "undefined name", "undefined function",
		"unknown identifier", "undeclared",
		"not declared", "is not defined", "unknown type") {
		return CodeUndeclared
	}

	// --- calls / arity ---
	if has(m, "expects 0 arguments", "expects 1 argument", "expects 2 argument",
		"expects 3 argument", "expects 4 argument",
		"wrong number of arguments", "too many arguments", "too few arguments",
		"takes no arguments",
		"is not a function", "is not a record type",
		"only direct function calls",
		"argument 1 must", "argument must be",
	) {
		return CodeCall
	}

	// --- "v0 does not support …" / intentional limitations ---
	if has(m, "v0 does not support", "not yet supported",
		"is not supported", "not part of v0") {
		return CodeUnsupported
	}

	// --- bad type expressions ---
	if has(m,
		"unsupported type expression", "must be a record",
		"anonymous record", "void cannot be made optional",
		"type is already optional", "array element type cannot be void",
		"unsupported literal pattern", "cyclic record type",
	) {
		return CodeBadTypeExpr
	}

	// --- inference failures ---
	if has(m, "cannot infer", "could not infer", "cannot determine") {
		return CodeInfer
	}

	// --- type mismatch catch-all ---
	if has(m, "expected ", " got ", "type mismatch",
		"must be bool", "must be number", "must be string",
		"must be a string", "must be a number", "must be a bool",
		"requires number", "requires bool", "requires numeric",
		"requires both operands", "operands of the same type",
		"requires a string or array", "requires an optional",
		"index must be number",
	) {
		return CodeTypeMismatch
	}

	// Fallback: a real diagnostic with no match. Bucket as type mismatch and
	// add a new pattern above when this fires for something else.
	return CodeTypeMismatch
}

func has(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
