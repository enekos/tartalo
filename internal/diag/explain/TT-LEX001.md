# TT-LEX001: Lexer error

The lexer rejected a token before parsing could begin. Common causes:

- an unterminated `"..."` string literal (missing closing quote)
- an unterminated `` `...` `` command literal
- an invalid escape inside a string (only `\n \t \\ \" \$` are recognized)
- a stray byte that is not part of any token (e.g. `&` is rejected because Tartalo has no bitwise `&`; use `&&` for logical AND)

## Repair

- Close any open `"` or `` ` `` on the same line. Multi-line strings are not supported; use `\n` for embedded newlines.
- Replace unsupported escapes with the literal character or one of the four supported escapes.
- For backticks, prefer the `exec("...")` builtin when you need control characters in the command itself — the backtick form parses raw shell text.

```tartalo
let line: string = "first\nsecond"        // ok
let cmd:  string = `ls -1`                // ok
```
