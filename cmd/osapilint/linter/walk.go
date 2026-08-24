// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// patternAll is the go/packages load pattern for a whole module.
const patternAll = "./..."

// WalkConfig parameterizes a Walk over a consumer module.
type WalkConfig struct {
	Dir      string   // consumer module directory
	Patterns []string // go/packages patterns; default {"./..."}
	Write    bool     // false = dry run, report only
}

// File is one loaded source file handed to a Visitor.
type File struct {
	Pkg    *packages.Package
	Syntax *ast.File
	Info   *types.Info
	Fset   *token.FileSet
}

// Visitor mutates one file's AST in place and reports the edits it made plus any
// unclassified references it could not resolve. A non-empty unclassified slice
// anywhere in the walk aborts the whole run before ANY file is written, so an
// incomplete external rewrite table can never leave a module half-migrated.
type Visitor func(File) (edits []string, unclassified []string)

// Result reports per-file edits after a Walk.
type Result struct {
	Path  string
	Edits []string
}

// Walk loads the consumer packages against their current deps, runs visit on
// each file, and (when cfg.Write) flushes mutated files through an os.Root write
// sandbox anchored at cfg.Dir. It retains the internal driver's load-error gate,
// cross-variant dedupe, and abort-before-write-on-unclassified invariant, so the
// same safety net governs external visitors.
func Walk(cfg WalkConfig, visit Visitor) ([]Result, error) {
	patterns := cfg.Patterns
	if len(patterns) == 0 {
		patterns = []string{patternAll}
	}
	loadCfg := &packages.Config{
		Dir: cfg.Dir,
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
		Tests: true,
	}
	pkgs, err := packages.Load(loadCfg, patterns...)
	if err != nil {
		return nil, err
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, fmt.Errorf("consumer must compile against the source version before rewriting; load reported errors")
	}

	seen := map[string]bool{} // dedupe files shared across test/non-test package variants
	var results []Result

	// pending holds files whose AST was mutated, to be flushed only after the
	// whole module is checked: an unclassified reference anywhere aborts the run
	// before any file is written, so a bug in the rewrite table can never leave
	// the module half-rewritten.
	type pendingWrite struct {
		path string
		fset *token.FileSet
		file *ast.File
	}
	var pending []pendingWrite
	var unclassified []string

	for _, pkg := range pkgs {
		for _, file := range pkg.Syntax {
			// Resolve the file's path from the token position, not by indexing a
			// parallel slice - Syntax and CompiledGoFiles are not guaranteed to be
			// the same length across test/non-test package variants.
			path := pkg.Fset.Position(file.Pos()).Filename
			if path == "" || seen[path] {
				continue
			}
			seen[path] = true

			edits, uncl := visit(File{Pkg: pkg, Syntax: file, Info: pkg.TypesInfo, Fset: pkg.Fset})
			unclassified = append(unclassified, uncl...)
			if len(edits) == 0 {
				continue
			}
			results = append(results, Result{Path: path, Edits: edits})
			pending = append(pending, pendingWrite{path: path, fset: pkg.Fset, file: file})
		}
	}

	// A referenced unclassified field is a bug in the rewrite table - it cannot
	// know rename vs remove. Fail loudly and write nothing rather than guess or
	// silently drop a caller's value.
	if len(unclassified) > 0 {
		return results, fmt.Errorf("osapilint bug: %d field(s) vanished on the target with no disposition "+
			"(cannot know rename vs remove); classify each in the hop's FieldDispositions table:\n  %s",
			len(unclassified), strings.Join(unclassified, "\n  "))
	}

	if !cfg.Write {
		return results, nil
	}

	// Sandbox all writes to the consumer module directory: os.Root refuses any
	// path that escapes root (via .., absolute paths, or symlinks), so a
	// misresolved file position can never overwrite something outside the module
	// being migrated.
	abs, err := filepath.Abs(cfg.Dir)
	if err != nil {
		return results, fmt.Errorf("resolve module dir: %w", err)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return results, fmt.Errorf("open module dir as sandbox root: %w", err)
	}
	defer root.Close()

	for _, w := range pending {
		if err := writeFormatted(root, cfg.Dir, w.path, w.fset, w.file); err != nil {
			return results, err
		}
	}
	return results, nil
}
