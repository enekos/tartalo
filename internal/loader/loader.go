// Package loader resolves a tartalo program's import graph.
//
// Given an entry file, it recursively reads, parses, and links every imported
// module. The result is a flat slice of *Module values in topological order
// (dependencies before dependents) with stable per-module IDs that the
// codegen uses to mangle global names.
//
// Lex+parse runs in parallel across modules: when a module is parsed and its
// imports are known, each unseen import is dispatched to its own goroutine.
// Cycle detection happens after the parse phase via a colored DFS, since the
// previous "visiting set" approach didn't survive concurrent recursion.
package loader

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/enekos/tartalo/internal/ast"
	"github.com/enekos/tartalo/internal/lexer"
	"github.com/enekos/tartalo/internal/parser"
	"github.com/enekos/tartalo/internal/stdlib"
)

// Module is one parsed source file plus its resolved import edges.
type Module struct {
	// AbsPath is the canonical path of the file. Used as the cache key so the
	// same module imported via two different relative paths is loaded once.
	AbsPath string

	// ID is a unique 0-based index. Assigned in topological order so deps
	// always have lower IDs than dependents.
	ID int

	// File is the parsed AST for this module.
	File *ast.File

	// Imports maps the AST ImportDecl to the Module it resolved to. Iteration
	// order matches the order imports appear in the source.
	Imports []ResolvedImport

	// IsEntry is true iff this is the file the program was started with.
	IsEntry bool

	// Source is the raw text the lexer was given. Held so the diagnostic
	// renderer can show code frames without re-reading the file. SourceName
	// matches the `File` field stamped onto every token.Pos in this module.
	Source     string
	SourceName string
}

// ResolvedImport is one import statement after path resolution.
type ResolvedImport struct {
	Decl   *ast.ImportDecl
	Module *Module
}

// Load reads, parses, and links the entry file and its transitive imports.
// Returns the modules in topological order (so each module's dependencies
// always appear earlier in the slice). Errors from any file's lex/parse pass
// are returned together; loading continues so multiple errors can be reported.
func Load(entryPath string) ([]*Module, []error) {
	mods, _, errs := load(entryPath, nil)
	return mods, errs
}

// LoadWithSources is Load that additionally returns a snapshot of every
// source the loader read, keyed by the same name the lexer stamped into
// token positions. The map is populated even when parsing fails — useful
// for the diagnostic renderer, which needs the original text to show code
// frames regardless of whether the AST is valid.
func LoadWithSources(entryPath string) ([]*Module, map[string]string, []error) {
	return load(entryPath, nil)
}

// LoadOverlay is LoadWithSources with an in-memory overlay: any module whose
// absolute path appears in `overlay` is parsed from the supplied text instead
// of being read from disk. The LSP uses this to feed unsaved editor buffers
// into the same loader+checker pipeline the CLI uses. Keys must be canonical
// absolute paths (matching what filepath.Abs returns).
func LoadOverlay(entryPath string, overlay map[string]string) ([]*Module, map[string]string, []error) {
	return load(entryPath, overlay)
}

func load(entryPath string, overlay map[string]string) ([]*Module, map[string]string, []error) {
	abs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, nil, []error{err}
	}
	l := &loader{cache: map[string]*Module{}, overlay: overlay}
	root := l.startLoad(abs, true)
	l.wg.Wait()

	sources := make(map[string]string, len(l.cache))
	for _, m := range l.cache {
		if m.SourceName != "" {
			sources[m.SourceName] = m.Source
		}
	}

	if root == nil || root.File == nil {
		return nil, sources, l.errs
	}

	if cycErr := detectCycles(root); cycErr != nil {
		l.errs = append(l.errs, cycErr)
		// Drop back-edges so downstream walks don't loop. Front-end bails on
		// errs anyway, but a defensive break keeps callers safe.
		breakBackEdges(root)
	}

	// Topological sort: post-order DFS gives us deps-before-dependents.
	var ordered []*Module
	seen := map[*Module]bool{}
	var visit func(m *Module)
	visit = func(m *Module) {
		if seen[m] {
			return
		}
		seen[m] = true
		for _, imp := range m.Imports {
			if imp.Module != nil {
				visit(imp.Module)
			}
		}
		ordered = append(ordered, m)
	}
	visit(root)
	for i, m := range ordered {
		m.ID = i
	}
	return ordered, sources, l.errs
}

type loader struct {
	mu    sync.Mutex
	cache map[string]*Module
	errs  []error
	wg    sync.WaitGroup
	// overlay is a snapshot of in-memory file contents keyed by canonical
	// absolute path. When non-nil, parseModule consults it before reading
	// from disk so the LSP can serve diagnostics against unsaved buffers.
	overlay map[string]string
}

// startLoad guarantees there is exactly one *Module per absolute path. If we
// haven't seen this path before, it allocates the Module, caches it, and
// kicks off a goroutine to parse it. Returns the (possibly still-being-parsed)
// pointer; callers must wait on wg before reading File or Imports.
func (l *loader) startLoad(absPath string, isEntry bool) *Module {
	l.mu.Lock()
	if m, ok := l.cache[absPath]; ok {
		l.mu.Unlock()
		return m
	}
	m := &Module{AbsPath: absPath, IsEntry: isEntry}
	l.cache[absPath] = m
	l.mu.Unlock()

	l.wg.Add(1)
	go l.parseModule(m)
	return m
}

func (l *loader) parseModule(m *Module) {
	defer l.wg.Done()

	var (
		src      []byte
		fileName string
	)
	if stdlib.IsStdlibPath(m.AbsPath) {
		_, data, ok := stdlib.Resolve(m.AbsPath)
		if !ok {
			l.addErr(fmt.Errorf("read stdlib %s: not found", m.AbsPath))
			return
		}
		src = data
		fileName = m.AbsPath
	} else if text, ok := l.overlay[m.AbsPath]; ok {
		src = []byte(text)
		fileName = filepath.Base(m.AbsPath)
	} else {
		data, err := os.ReadFile(m.AbsPath)
		if err != nil {
			l.addErr(fmt.Errorf("read %s: %w", m.AbsPath, err))
			return
		}
		src = data
		fileName = filepath.Base(m.AbsPath)
	}
	srcStr := string(src)
	m.Source = srcStr
	m.SourceName = fileName
	toks, lerrs := lexer.New(fileName, srcStr).Tokenize()
	l.addErrs(lerrs)
	file, perrs := parser.New(toks).Parse(fileName)
	l.addErrs(perrs)
	if file == nil {
		return
	}
	m.File = file

	for _, imp := range file.Imports {
		dep, err := l.resolveImport(m, imp)
		if err != nil {
			l.addErr(err)
			m.Imports = append(m.Imports, ResolvedImport{Decl: imp})
			continue
		}
		m.Imports = append(m.Imports, ResolvedImport{Decl: imp, Module: dep})
	}
}

// resolveImport turns an ImportDecl's path string into a *Module pointer,
// loading it if necessary. Two flavours:
//
//   - "tartalo:foo/bar" — a stdlib import. Resolved against the embedded
//     filesystem in the stdlib package; the canonical path is the import
//     string itself, kept stable so two `import` statements for the same
//     stdlib path collapse to a single Module.
//   - any other path — a relative file path resolved against the importing
//     module's directory. Stdlib modules cannot use relative imports.
func (l *loader) resolveImport(m *Module, imp *ast.ImportDecl) (*Module, error) {
	if strings.HasPrefix(imp.Path, stdlib.Prefix) {
		canonical, _, ok := stdlib.Resolve(imp.Path)
		if !ok {
			return nil, fmt.Errorf("%s: cannot find stdlib module %q", imp.PathPos, imp.Path)
		}
		return l.startLoad(canonical, false), nil
	}
	if stdlib.IsStdlibPath(m.AbsPath) {
		return nil, fmt.Errorf("%s: stdlib modules cannot use relative imports (got %q)", imp.PathPos, imp.Path)
	}
	dir := filepath.Dir(m.AbsPath)
	resolvedPath := imp.Path
	if !filepath.IsAbs(resolvedPath) {
		resolvedPath = filepath.Join(dir, resolvedPath)
	}
	canonical, err := filepath.Abs(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", imp.PathPos, err)
	}
	if _, err := os.Stat(canonical); err != nil {
		return nil, fmt.Errorf("%s: cannot find module %q (resolved to %s)", imp.PathPos, imp.Path, canonical)
	}
	return l.startLoad(canonical, false), nil
}

func (l *loader) addErr(e error) {
	l.mu.Lock()
	l.errs = append(l.errs, e)
	l.mu.Unlock()
}

func (l *loader) addErrs(es []error) {
	if len(es) == 0 {
		return
	}
	l.mu.Lock()
	l.errs = append(l.errs, es...)
	l.mu.Unlock()
}

// detectCycles walks the import graph from root with the classic
// white/gray/black DFS coloring and returns the first cycle found.
func detectCycles(root *Module) error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[*Module]int{}
	var visit func(m *Module) error
	visit = func(m *Module) error {
		switch color[m] {
		case gray:
			return fmt.Errorf("import cycle detected involving %s", m.AbsPath)
		case black:
			return nil
		}
		color[m] = gray
		for _, imp := range m.Imports {
			if imp.Module != nil {
				if err := visit(imp.Module); err != nil {
					return err
				}
			}
		}
		color[m] = black
		return nil
	}
	return visit(root)
}

// breakBackEdges nils out any import edge that closes a cycle, so post-error
// walks of the graph can't loop.
func breakBackEdges(root *Module) {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[*Module]int{}
	var visit func(m *Module)
	visit = func(m *Module) {
		if color[m] == black {
			return
		}
		color[m] = gray
		for i, imp := range m.Imports {
			if imp.Module == nil {
				continue
			}
			if color[imp.Module] == gray {
				m.Imports[i].Module = nil
				continue
			}
			visit(imp.Module)
		}
		color[m] = black
	}
	visit(root)
}
