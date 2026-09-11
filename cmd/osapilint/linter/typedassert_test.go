// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"path/filepath"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestTypedAssertAnalyzer runs the analyzer against the v4 package packed in
// testdata/typedassert.txtar, whose //want comments mark the lines that must be
// flagged. Lines without a //want comment must NOT be flagged (false positives
// fail the test just as missed diagnostics do). analysistest expects a GOPATH-
// style "src/..." tree on disk, so the archive is extracted into a temp dir
// before the analyzer runs.
func TestTypedAssertAnalyzer(t *testing.T) {
	dir := t.TempDir()
	extractTxtar(t, filepath.Join("testdata", "typedassert.txtar"), filepath.Join(dir, "src"))
	analysistest.Run(t, dir, typedAssertAnalyzer, "v4")
}
