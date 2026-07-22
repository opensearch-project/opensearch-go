// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package engine

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestTypedAssertAnalyzer runs the analyzer against testdata/src/v4, whose
// //want comments mark the lines that must be flagged. Lines without a //want
// comment must NOT be flagged (false positives fail the test just as missed
// diagnostics do).
func TestTypedAssertAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), TypedAssertAnalyzer, "v4")
}
