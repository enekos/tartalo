; Tartalo syntax highlights
; Targets the captures in tree-sitter-cli's default theme so the same query
; works for `tree-sitter highlight` and editors that consume the standard
; highlight names.

; ---------- Comments ----------
(line_comment) @comment

; ---------- Keywords ----------
[
  "let"
  "const"
  "func"
  "type"
  "test"
  "import"
  "export"
  "return"
] @keyword

[
  "if"
  "else"
  "for"
  "in"
  "while"
  "break"
  "continue"
  "match"
] @keyword.control

"from" @keyword

; ---------- Built-in types ----------
(primitive_type) @type.builtin

; ---------- Type names ----------
(type_declaration name: (identifier) @type)
(type_identifier (identifier) @type)
(record_literal type_name: (identifier) @type)

; ---------- Functions ----------
(function_declaration name: (identifier) @function)
(call_expression callee: (identifier) @function.call)
(parameter name: (identifier) @variable.parameter)

; ---------- Fields ----------
(record_field name: (identifier) @property)
(field_initializer name: (identifier) @property)
(field_expression name: (identifier) @property)

; ---------- Variables ----------
(variable_declaration name: (identifier) @variable)
(assignment_statement left: (identifier) @variable)
(for_statement var: (identifier) @variable)
(import_declaration (identifier) @variable)

; ---------- Literals ----------
(int_literal) @number
(float_literal) @number.float
(bool_literal) @constant.builtin.boolean
(null_literal) @constant.builtin
(wildcard_pattern) @constant.builtin

; Strings (and command literals) and their inner pieces
(string) @string
(string_content) @string
(escape_sequence) @string.escape
(command_literal) @string.special
(cmd_content) @string.special
(cmd_escape_sequence) @string.escape

; The `${` / `}` framing of an interpolation should stand out from
; surrounding string text.
(interpolation
  "${" @punctuation.special
  "}" @punctuation.special)

; ---------- Operators ----------
[
  "+" "-" "*" "/" "%"
  "==" "!=" "<" "<=" ">" ">="
  "&&" "||" "!"
  "??"
  ".."
  "="
  "?"
  "=>"
] @operator

; ---------- Punctuation ----------
[
  "(" ")" "[" "]" "{" "}"
] @punctuation.bracket

[
  "," ":" ";" "."
] @punctuation.delimiter

"|" @punctuation.delimiter
