# Tartalo for Zed

Syntax highlighting, LSP diagnostics, and language support for [Tartalo](https://github.com/yourusername/tartalo) — a small, statically-typed scripting language that compiles to POSIX sh.

## Features

- **Syntax highlighting** for `.tt` files
- **Shell syntax highlighting** inside command literals (`` `...` ``)
- **LSP diagnostics** — real-time syntax error reporting via `tartalo lsp`
- **Auto-bracket pairing** for `{}`, `[]`, `()`, strings, and command literals
- **Outline view** support for functions, tests, and type declarations
- **Line comment** toggling with `//`
- **Smart indentation** for blocks, functions, conditionals, and loops

## Installation

### Prerequisites

1. **Build the Tartalo compiler** (the LSP needs the `tartalo` binary on PATH):
   ```bash
   cd /path/to/tartalo
   go build -o tartalo ./cmd/tartalo
   # Ensure it's on your PATH, e.g.:
   # sudo mv tartalo /usr/local/bin/
   ```

2. **Install Rust** (for compiling the Zed extension WASM adapter):
   ```bash
   curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
   ```

### Install as a dev extension

1. In Zed, open the command palette (`Cmd+Shift+P` / `Ctrl+Shift+P`)
2. Run **"Extensions: Install Dev Extension"**
3. Select the `zed-tartalo` directory
4. Zed will compile the Rust/WASM adapter and download the Tree-sitter grammar automatically

## File Structure

```
zed-tartalo/
├── Cargo.toml                  # Rust package for WASM LSP adapter
├── extension.toml              # Extension manifest
├── src/
│   └── lib.rs                  # LSP adapter (finds tartalo binary, runs `tartalo lsp`)
├── grammars/
│   └── tartalo.toml            # Tree-sitter grammar source
└── languages/
    └── tartalo/
        ├── config.toml         # Language configuration
        ├── highlights.scm      # Syntax highlighting queries
        ├── indents.scm         # Smart indentation rules
        ├── injections.scm      # Shell highlighting in command literals
        └── outline.scm         # Symbol outline queries
```

## LSP Configuration

The extension auto-discovers the `tartalo` binary on your PATH and runs `tartalo lsp` for diagnostics. No manual configuration needed.

If you need to override the binary path, add this to your Zed `settings.json`:

```json
{
  "lsp": {
    "tartalo": {
      "binary": {
        "path": "/path/to/tartalo",
        "args": ["lsp"]
      }
    }
  }
}
```

## Shell Highlighting in Command Literals

Tartalo's command literals (backtick strings) contain shell commands. The extension injects bash syntax highlighting inside them:

```tartalo
let r = `git log --oneline -n 5`   // <-- shell code is highlighted
let out = `printf "%s\n" ${name}`  // <-- interpolations + shell highlighting
```

## Grammar

This extension uses the Tree-sitter grammar defined in [`../tree-sitter-tartalo`](../tree-sitter-tartalo).
