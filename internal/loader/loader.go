// Package loader resolves a tartalo program's import graph.
//
// Given an entry file, it recursively reads, parses, and links every imported
// module. The result is a flat slice of *Module values in topological order
// (dependencies before dependents) with stable per-module IDs that the
// codegen uses to mangle global names.
package loader

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/enekosarasola/tartalo/internal/ast"
	"github.com/enekosarasola/tartalo/internal/lexer"
	"github.com/enekosarasola/tartalo/internal/parser"
)

// Module is one parsed source file plus its resolved import edges.
type Module struct {
	// AbsPath is the canonical path of the file. Used as the cache key so the
	// same module imported via two different relative paths is loaded once.
	AbsPath string

	// ID is a unique 0-based index. The entry file is ID 0, dependencies follow.
	ID int

	// File is the parsed AST for this module.
	File *ast.File

	// Imports maps the AST ImportDecl to the Module it resolved to. Iteration
	// order matches the order imports appear in the source.
	Imports []ResolvedImport

	// IsEntry is true iff this is the file the program was started with.
	IsEntry bool
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
	abs, err := filepath.Abs(entryPath)
	if err != nil {
		return nil, []error{err}
	}
	l := &loader{
		cache:    map[string]*Module{},
		visiting: map[string]bool{},
	}
	root, errs := l.load(abs, true)
	if root == nil {
		return nil, errs
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
	// Re-assign IDs in topological order so codegen's mangling is stable.
	for i, m := range ordered {
		m.ID = i
	}
	return ordered, errs
}

type loader struct {
	cache    map[string]*Module
	visiting map[string]bool
	errs     []error
}

func (l *loader) load(absPath string, isEntry bool) (*Module, []error) {
	// Cycle check must come BEFORE the cache check: we eagerly cache modules
	// the moment they're parsed (so two siblings that import a shared dep get
	// the same Module pointer), which means a cycle would be silently
	// resolvable without this guard.
	if l.visiting[absPath] {
		l.errs = append(l.errs, fmt.Errorf("import cycle detected involving %s", absPath))
		return nil, l.errs
	}
	if m, ok := l.cache[absPath]; ok {
		return m, nil
	}
	l.visiting[absPath] = true
	defer delete(l.visiting, absPath)

	src, err := os.ReadFile(absPath)
	if err != nil {
		l.errs = append(l.errs, fmt.Errorf("read %s: %w", absPath, err))
		return nil, l.errs
	}
	toks, lerrs := lexer.New(filepath.Base(absPath), string(src)).Tokenize()
	l.errs = append(l.errs, lerrs...)
	file, perrs := parser.New(toks).Parse(filepath.Base(absPath))
	l.errs = append(l.errs, perrs...)
	if file == nil {
		return nil, l.errs
	}
	m := &Module{
		AbsPath: absPath,
		File:    file,
		IsEntry: isEntry,
	}
	l.cache[absPath] = m

	dir := filepath.Dir(absPath)
	for _, imp := range file.Imports {
		resolvedPath := imp.Path
		if !filepath.IsAbs(resolvedPath) {
			resolvedPath = filepath.Join(dir, resolvedPath)
		}
		canonical, err := filepath.Abs(resolvedPath)
		if err != nil {
			l.errs = append(l.errs, fmt.Errorf("%s: %w", imp.PathPos, err))
			m.Imports = append(m.Imports, ResolvedImport{Decl: imp})
			continue
		}
		if _, err := os.Stat(canonical); err != nil {
			l.errs = append(l.errs, fmt.Errorf("%s: cannot find module %q (resolved to %s)", imp.PathPos, imp.Path, canonical))
			m.Imports = append(m.Imports, ResolvedImport{Decl: imp})
			continue
		}
		dep, _ := l.load(canonical, false)
		m.Imports = append(m.Imports, ResolvedImport{Decl: imp, Module: dep})
	}
	return m, l.errs
}
