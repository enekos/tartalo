# tree-sitter-tartalo

A [tree-sitter](https://tree-sitter.github.io) grammar for the
[Tartalo](../README.md) scripting language (`.tt`). It mirrors the
hand-written lexer and recursive-descent parser in `internal/lexer` and
`internal/parser`, including:

- Declarations: `import`, `let`, `const`, `func`, `type`, `test`, `export`
- Types: primitives, arrays (`T[]`), optionals (`T?`), records, function types
- Statements: `if`/`else if`/`else`, `for ... in`, `match`, `return`, blocks,
  variable + field assignments
- Expressions: full Pratt-style operator precedence (matches `parseBinary` in
  `internal/parser/parser.go`), postfix `()`, `[]`, `.field`, `!` (unwrap),
  prefix `-`/`!`, `??`, `..` ranges
- String literals with `${...}` interpolation and `\n \t \r \\ \" \$ \``
  escapes
- Command literals (backtick) with the same interpolation support
- Match patterns (`_`, ints, bools, strings, `|` alternatives)
- Record literals `Name{ field: expr, ... }`, disambiguated against block
  bodies via parallel "no-struct" expression rules (so `if Foo { ... }` parses
  with `Foo` as the condition).

## Layout

```
grammar.js              # the grammar
queries/highlights.scm  # syntax highlighting query (standard captures)
test/corpus/basic.txt   # tree-sitter test corpus
tree-sitter.json        # CLI metadata
```

## Building

```
npm install                    # node-addon-api / node-gyp-build / tree-sitter-cli
npx tree-sitter generate       # writes src/parser.c
npx tree-sitter test           # runs the corpus
npx tree-sitter parse FILE.tt  # parse a file from disk
```

`tree-sitter parse` needs to know where the grammar lives. The simplest setup
is to add the parent directory (i.e. the Tartalo repo root) to
`parser-directories` in `~/.config/tree-sitter/config.json`.

## Validation

The corpus tests (`tree-sitter test`) are the primary regression suite. As a
smoke test, every `.tt` file under `examples/`, `internal/codegen/`,
`internal/stdlib/lib/`, and the top-level benchmark script also parse without
errors.
