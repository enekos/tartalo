/**
 * Tree-sitter grammar for the Tartalo language.
 *
 * Mirrors the hand-written lexer/parser in internal/lexer and internal/parser:
 * - POSIX-shell-targeted scripting language with .tt files
 * - Precedence ladder identical to parseBinary in internal/parser/parser.go
 * - String/command literals carry `${...}` interpolations
 * - Record literals `Name{ field: value }` need a small disambiguation
 *   against block bodies via two parallel expression rules (`_expression`
 *   vs `_expression_no_struct`), the same trick rust-tree-sitter uses.
 */

const PREC = {
  COALESCE: 1,
  OR: 2,
  AND: 3,
  EQ: 4,
  CMP: 5,
  RANGE: 6,
  ADD: 7,
  MUL: 8,
  UNARY: 9,
  POSTFIX: 10,
};

module.exports = grammar({
  name: 'tartalo',

  extras: $ => [
    /[\s]/,
    $.line_comment,
  ],

  word: $ => $.identifier,

  conflicts: $ => [],

  supertypes: $ => [
    $._declaration,
    $._statement,
    $._expression,
    $._type,
    $._pattern,
  ],

  rules: {
    source_file: $ => seq(
      repeat($.import_declaration),
      repeat($._declaration),
    ),

    line_comment: $ => token(seq('//', /[^\n]*/)),

    // ---------- Imports ----------
    import_declaration: $ => seq(
      'import',
      '{',
      commaSep1($.identifier),
      '}',
      'from',
      field('path', $.string),
      optional(';'),
    ),

    // ---------- Top-level / statement-level declarations ----------
    _declaration: $ => choice(
      $.function_declaration,
      $.variable_declaration,
      $.type_declaration,
      $.test_declaration,
    ),

    function_declaration: $ => seq(
      optional('export'),
      'func',
      field('name', $.identifier),
      field('parameters', $.parameter_list),
      ':',
      field('return_type', $._type),
      field('body', $.block),
    ),

    parameter_list: $ => seq(
      '(',
      optional(commaSep1($.parameter)),
      ')',
    ),

    parameter: $ => seq(
      field('name', $.identifier),
      ':',
      field('type', $._type),
    ),

    variable_declaration: $ => seq(
      optional('export'),
      field('kind', choice('let', 'const')),
      field('name', $.identifier),
      optional(seq(':', field('type', $._type))),
      '=',
      field('value', $._expression),
      optional(';'),
    ),

    type_declaration: $ => seq(
      optional('export'),
      'type',
      field('name', $.identifier),
      '=',
      field('value', $._type),
      optional(';'),
    ),

    test_declaration: $ => seq(
      'test',
      field('name', $.string),
      field('body', $.block),
    ),

    // ---------- Types ----------
    _type: $ => choice(
      $.primitive_type,
      $.type_identifier,
      $.array_type,
      $.optional_type,
      $.record_type,
      $.function_type,
    ),

    primitive_type: $ => choice('string', 'number', 'float', 'bool', 'void'),

    type_identifier: $ => $.identifier,

    array_type: $ => prec(2, seq(field('element', $._type), '[', ']')),
    optional_type: $ => prec(1, seq(field('element', $._type), '?')),

    record_type: $ => seq(
      '{',
      optional(seq(
        $.record_field,
        repeat(seq(choice(',', ';'), $.record_field)),
        optional(choice(',', ';')),
      )),
      '}',
    ),

    record_field: $ => seq(
      field('name', $.identifier),
      ':',
      field('type', $._type),
    ),

    function_type: $ => seq(
      'func',
      '(',
      optional(commaSep1($._type)),
      ')',
      ':',
      field('return_type', $._type),
    ),

    // ---------- Statements ----------
    block: $ => seq('{', repeat($._statement), '}'),

    _statement: $ => choice(
      $.variable_declaration,
      $.if_statement,
      $.for_statement,
      $.while_statement,
      $.break_statement,
      $.continue_statement,
      $.return_statement,
      $.match_statement,
      $.block,
      $.assignment_statement,
      $.expression_statement,
    ),

    if_statement: $ => seq(
      'if',
      field('condition', $._expression_no_struct),
      field('consequence', $.block),
      optional(seq(
        'else',
        field('alternative', choice($.block, $.if_statement)),
      )),
    ),

    for_statement: $ => seq(
      'for',
      field('var', $.identifier),
      'in',
      field('iter', $._expression_no_struct),
      field('body', $.block),
    ),

    while_statement: $ => seq(
      'while',
      field('condition', $._expression_no_struct),
      field('body', $.block),
    ),

    break_statement: $ => prec.right(seq(
      'break',
      optional(';'),
    )),

    continue_statement: $ => prec.right(seq(
      'continue',
      optional(';'),
    )),

    return_statement: $ => prec.right(seq(
      'return',
      optional($._expression),
      optional(';'),
    )),

    match_statement: $ => seq(
      'match',
      field('subject', $._expression_no_struct),
      '{',
      repeat($.match_case),
      '}',
    ),

    match_case: $ => seq(
      $._pattern,
      repeat(seq('|', $._pattern)),
      '=>',
      field('body', $._statement),
    ),

    _pattern: $ => choice(
      $.wildcard_pattern,
      $.int_literal,
      $.bool_literal,
      $.string,
    ),

    wildcard_pattern: _ => '_',

    // Tartalo only allows `name = expr` and `target.field = expr` as
    // assignments (matches the Go parser's FieldAssignStmt shape). We accept
    // any field_expression on the LHS — call/index chains under a final `.f`
    // are permitted because the outermost node is still a field_expression.
    assignment_statement: $ => prec(1, seq(
      field('left', choice($.identifier, $.field_expression)),
      '=',
      field('right', $._expression),
      optional(';'),
    )),

    expression_statement: $ => seq($._expression, optional(';')),

    // ---------- Expressions ----------
    _expression: $ => choice(
      $.binary_expression,
      $.unary_expression,
      $.coalesce_expression,
      $.range_expression,
      $._unary_expression_target,
    ),

    // The struct-free flavor exists so `if Foo { ... }` parses with `Foo` as
    // the condition (not as an empty record literal). Each rule is hidden
    // (leading `_`) and aliased back to its struct-allowing name so the
    // public tree exposes only `binary_expression`, `field_expression`, etc.
    _expression_no_struct: $ => choice(
      alias($._binary_expression_no_struct, $.binary_expression),
      alias($._unary_expression_no_struct, $.unary_expression),
      alias($._coalesce_expression_no_struct, $.coalesce_expression),
      alias($._range_expression_no_struct, $.range_expression),
      $._unary_expression_target_no_struct,
    ),

    binary_expression: $ => choice(
      ...binaryOpClauses($, '_expression'),
    ),

    _binary_expression_no_struct: $ => choice(
      ...binaryOpClauses($, '_expression_no_struct'),
    ),

    coalesce_expression: $ => prec.right(PREC.COALESCE, seq(
      field('left', $._expression),
      '??',
      field('right', $._expression),
    )),

    _coalesce_expression_no_struct: $ => prec.right(PREC.COALESCE, seq(
      field('left', $._expression_no_struct),
      '??',
      field('right', $._expression_no_struct),
    )),

    range_expression: $ => prec.left(PREC.RANGE, seq(
      field('start', $._expression),
      '..',
      field('end', $._expression),
    )),

    _range_expression_no_struct: $ => prec.left(PREC.RANGE, seq(
      field('start', $._expression_no_struct),
      '..',
      field('end', $._expression_no_struct),
    )),

    unary_expression: $ => prec(PREC.UNARY, seq(
      field('operator', choice('-', '!')),
      field('operand', $._expression),
    )),

    _unary_expression_no_struct: $ => prec(PREC.UNARY, seq(
      field('operator', choice('-', '!')),
      field('operand', $._expression_no_struct),
    )),

    _unary_expression_target: $ => choice(
      $.call_expression,
      $.index_expression,
      $.field_expression,
      $.unwrap_expression,
      $._primary_expression,
    ),

    _unary_expression_target_no_struct: $ => choice(
      alias($._call_expression_no_struct, $.call_expression),
      alias($._index_expression_no_struct, $.index_expression),
      alias($._field_expression_no_struct, $.field_expression),
      alias($._unwrap_expression_no_struct, $.unwrap_expression),
      $._primary_expression_no_struct,
    ),

    call_expression: $ => prec(PREC.POSTFIX, seq(
      field('callee', $._unary_expression_target),
      '(',
      optional(commaSep1($._expression)),
      ')',
    )),

    _call_expression_no_struct: $ => prec(PREC.POSTFIX, seq(
      field('callee', $._unary_expression_target_no_struct),
      '(',
      optional(commaSep1($._expression)),
      ')',
    )),

    index_expression: $ => prec(PREC.POSTFIX, seq(
      field('target', $._unary_expression_target),
      '[',
      field('index', $._expression),
      ']',
    )),

    _index_expression_no_struct: $ => prec(PREC.POSTFIX, seq(
      field('target', $._unary_expression_target_no_struct),
      '[',
      field('index', $._expression),
      ']',
    )),

    field_expression: $ => prec(PREC.POSTFIX, seq(
      field('target', $._unary_expression_target),
      '.',
      field('name', $.identifier),
    )),

    _field_expression_no_struct: $ => prec(PREC.POSTFIX, seq(
      field('target', $._unary_expression_target_no_struct),
      '.',
      field('name', $.identifier),
    )),

    unwrap_expression: $ => prec(PREC.POSTFIX, seq(
      field('operand', $._unary_expression_target),
      '!',
    )),

    _unwrap_expression_no_struct: $ => prec(PREC.POSTFIX, seq(
      field('operand', $._unary_expression_target_no_struct),
      '!',
    )),

    _primary_expression: $ => choice(
      $.parenthesized_expression,
      $.array_literal,
      $.record_literal,
      $.identifier,
      $.int_literal,
      $.float_literal,
      $.bool_literal,
      $.null_literal,
      $.string,
      $.command_literal,
    ),

    _primary_expression_no_struct: $ => choice(
      $.parenthesized_expression,
      $.array_literal,
      $.identifier,
      $.int_literal,
      $.float_literal,
      $.bool_literal,
      $.null_literal,
      $.string,
      $.command_literal,
    ),

    parenthesized_expression: $ => seq('(', $._expression, ')'),

    array_literal: $ => seq(
      '[',
      optional(seq(
        $._expression,
        repeat(seq(',', $._expression)),
        optional(','),
      )),
      ']',
    ),

    // Record literal: `Name { field: expr (, field: expr)* }`.
    // Fields use a leading `Ident :` which prevents collision with bare blocks.
    record_literal: $ => prec(1, seq(
      field('type_name', $.identifier),
      '{',
      optional(seq(
        $.field_initializer,
        repeat(seq(',', $.field_initializer)),
        optional(','),
      )),
      '}',
    )),

    field_initializer: $ => seq(
      field('name', $.identifier),
      ':',
      field('value', $._expression),
    ),

    // ---------- Literals ----------
    int_literal: _ => token(/[0-9]+/),
    float_literal: _ => token(choice(
      /[0-9]+\.[0-9]+([eE][+-]?[0-9]+)?/,
      /[0-9]+[eE][+-]?[0-9]+/,
    )),
    bool_literal: _ => choice('true', 'false'),
    null_literal: _ => 'null',

    // ---------- Strings with `${...}` interpolation ----------
    string: $ => seq(
      '"',
      repeat(choice(
        alias($._string_chunk, $.string_content),
        $.escape_sequence,
        $.interpolation,
      )),
      '"',
    ),

    // Anything inside a string except `"`, `\`, `${`, and a bare newline.
    // `$` not followed by `{` is allowed via the second alternative; the
    // lexer's longest-match rule keeps `${` aligned with `interpolation`
    // (no per-token `prec` here — that would override longest-match).
    _string_chunk: _ => choice(
      token.immediate(/[^"\\$\n]+/),
      token.immediate('$'),
    ),

    escape_sequence: _ => token.immediate(/\\[ntr\\"$`]/),

    interpolation: $ => seq(
      '${',
      $._expression,
      '}',
    ),

    // ---------- Command literals ----------
    command_literal: $ => seq(
      '`',
      repeat(choice(
        alias($._cmd_chunk, $.cmd_content),
        $.cmd_escape_sequence,
        $.interpolation,
      )),
      '`',
    ),

    _cmd_chunk: _ => choice(
      token.immediate(/[^`\\$]+/),
      token.immediate('$'),
    ),

    cmd_escape_sequence: _ => token.immediate(/\\[`\\$]/),

    // ---------- Identifiers ----------
    identifier: _ => /[A-Za-z_][A-Za-z0-9_]*/,
  },
});

function commaSep1(rule) {
  return seq(rule, repeat(seq(',', rule)), optional(','));
}

// Build the `binary_expression` ladder by reusing the same expression
// non-terminal name for both the struct-allowing and struct-free flavors.
function binaryOpClauses($, exprName) {
  const expr = $[exprName];
  return [
    [PREC.OR, '||', 'left'],
    [PREC.AND, '&&', 'left'],
    [PREC.EQ, '==', 'left'],
    [PREC.EQ, '!=', 'left'],
    [PREC.CMP, '<', 'left'],
    [PREC.CMP, '<=', 'left'],
    [PREC.CMP, '>', 'left'],
    [PREC.CMP, '>=', 'left'],
    [PREC.ADD, '+', 'left'],
    [PREC.ADD, '-', 'left'],
    [PREC.MUL, '*', 'left'],
    [PREC.MUL, '/', 'left'],
    [PREC.MUL, '%', 'left'],
  ].map(([precLevel, op, assoc]) =>
    (assoc === 'right' ? prec.right : prec.left)(precLevel, seq(
      field('left', expr),
      field('operator', op),
      field('right', expr),
    )),
  );
}
