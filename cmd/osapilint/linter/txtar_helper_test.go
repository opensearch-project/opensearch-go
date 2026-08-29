// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/txtar"
)

// extractTxtar parses the named txtar archive and materializes its files under
// dir, creating dir if needed. Fixtures live as txtar archives (one file per
// corpus/stub, rather than a directory of loose .go/.golden/go.mod files) so a
// scenario is one file to read, diff, and pin to LF in .gitattributes.
func extractTxtar(t *testing.T, archivePath, dir string) {
	t.Helper()
	a, err := txtar.ParseFile(archivePath)
	require.NoError(t, err, "parse %s", archivePath)
	fsys, err := txtar.FS(a)
	require.NoError(t, err, "build fs.FS from %s", archivePath)
	require.NoError(t, os.CopyFS(dir, fsys))
}

// txtarFile returns the data of the named file in a, and whether it was found.
func txtarFile(a *txtar.Archive, name string) ([]byte, bool) {
	for _, f := range a.Files {
		if f.Name == name {
			return f.Data, true
		}
	}
	return nil, false
}

// setTxtarFile sets (or appends) the named file's data in a.
func setTxtarFile(a *txtar.Archive, name string, data []byte) {
	for i, f := range a.Files {
		if f.Name == name {
			a.Files[i].Data = data
			return
		}
	}
	a.Files = append(a.Files, txtar.File{Name: name, Data: data})
}
