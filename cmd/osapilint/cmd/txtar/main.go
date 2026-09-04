// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Command txtar round-trips the linter's fixture archives so they can be
// edited with ordinary Go tooling instead of inside one big text file:
//
//	go run ./cmd/txtar unpack linter/testdata/corpus/v2.txtar /tmp/v2
//	(edit /tmp/v2 with gofmt, go build, an editor, ...)
//	go run ./cmd/txtar pack /tmp/v2 linter/testdata/corpus/v2.txtar
//
// pack writes back into an existing archive rather than regenerating it:
// the archive comment (license header and prose) is kept, surviving
// sections keep their order, deleted files drop out, and new files are
// appended in sorted order. Unpacking and repacking without edits is a
// no-op diff.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"golang.org/x/tools/txtar"
)

// Subcommand names, shared with the usage text and the dispatch tests.
const (
	cmdUnpack = "unpack"
	cmdPack   = "pack"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run dispatches one subcommand and returns the process exit status: 0 on
// success, 1 when the subcommand fails, 2 on a usage error. Taking the
// arguments and the error stream as parameters keeps the dispatch reachable
// from a test without spawning a subprocess.
func run(args []string, stderr io.Writer) int {
	if len(args) != 3 {
		usage(stderr)
		return 2
	}
	var err error
	switch args[0] {
	case cmdUnpack:
		err = unpack(args[1], args[2])
	case cmdPack:
		err = pack(args[1], args[2])
	default:
		usage(stderr)
		return 2
	}
	if err != nil {
		fmt.Fprintln(stderr, "txtar:", err)
		return 1
	}
	return 0
}

func usage(w io.Writer) {
	fmt.Fprint(w, `usage:
  txtar unpack <archive> <dir>   materialize the archive's sections under dir
  txtar pack   <dir> <archive>   write dir's files back into the archive
`)
}

// unpack materializes archive's sections as files under dir. dir is created
// through an os.Root anchored at its parent (which must exist), so the write
// stays confined there - the same shape as gensurface's writeThroughRoot. It
// refuses to overwrite: os.CopyFS fails if a destination file already exists,
// so point it at a fresh directory.
func unpack(archive, dir string) error {
	ar, err := txtar.ParseFile(archive)
	if err != nil {
		return err
	}
	fsys, err := txtar.FS(ar)
	if err != nil {
		return err
	}
	parent, base := filepath.Split(filepath.Clean(dir))
	if parent == "" {
		parent = "."
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(base, 0o755); err != nil {
		return err
	}
	return os.CopyFS(dir, fsys)
}

// pack writes every file under dir into archive. When the archive already
// exists its comment is preserved and its section order is kept for files that
// survive; deleted files drop out and new files are appended sorted, so a
// pack right after an unpack reproduces the archive byte for byte.
func pack(dir, archive string) error {
	files := map[string][]byte{}
	root := os.DirFS(dir)
	err := fs.WalkDir(root, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		files[path] = data
		return nil
	})
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("no files under %s", dir)
	}

	var ar txtar.Archive
	if old, err := txtar.ParseFile(archive); err == nil {
		ar.Comment = old.Comment
		for _, f := range old.Files {
			if data, ok := files[f.Name]; ok {
				ar.Files = append(ar.Files, txtar.File{Name: f.Name, Data: data})
				delete(files, f.Name)
			}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		ar.Files = append(ar.Files, txtar.File{Name: name, Data: files[name]})
	}

	out := txtar.Format(&ar)
	// txtar has no escaping: a file body containing a "-- name --" line would
	// silently absorb everything after it into a phantom section. Parse the
	// output back and require it to round-trip before touching the archive.
	if err := verifyRoundTrip(&ar, out); err != nil {
		return err
	}
	return writeThroughRoot(archive, out)
}

// writeThroughRoot writes data to out via an os.Root anchored at out's
// directory, so the write is confined there and cannot escape via symlinks or
// ".." - the same shape as gensurface's write path.
func writeThroughRoot(out string, data []byte) error {
	outDir, outName := filepath.Split(out)
	if outDir == "" {
		outDir = "."
	}
	root, err := os.OpenRoot(outDir)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.WriteFile(outName, data, 0o644)
}

// verifyRoundTrip re-parses formatted output and checks it yields exactly the
// sections that went in.
func verifyRoundTrip(want *txtar.Archive, out []byte) error {
	got := txtar.Parse(out)
	if len(got.Files) != len(want.Files) {
		return fmt.Errorf("archive does not round-trip: packed %d sections, re-parsed %d (a file body probably contains a txtar section marker)",
			len(want.Files), len(got.Files))
	}
	for i := range want.Files {
		w, g := want.Files[i], got.Files[i]
		// txtar.Format appends a newline to bodies missing one; compare
		// against that normalized form.
		if g.Name != w.Name || !bytes.Equal(g.Data, fixNL(w.Data)) {
			return fmt.Errorf("archive does not round-trip at section %q (a file body probably contains a txtar section marker)", w.Name)
		}
	}
	return nil
}

// fixNL mirrors txtar.Format's normalization: a non-empty body gains a
// trailing newline if it lacks one.
func fixNL(data []byte) []byte {
	if len(data) == 0 || data[len(data)-1] == '\n' {
		return data
	}
	return append(slices.Clone(data), '\n')
}
