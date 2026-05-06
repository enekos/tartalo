; Indentation rules for Tartalo

; Indent after opening braces
[
  (block)
  (function_declaration)
  (if_statement)
  (for_statement)
  (match_statement)
  (record_literal)
  (array_literal)
] @indent

; Dedent on closing brace
("}") @outdent

; Indent after match case arrow
(match_case
  "=>" @indent)
