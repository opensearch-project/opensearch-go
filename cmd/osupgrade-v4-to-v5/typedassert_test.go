package main

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestTypedAssertAnalyzer runs the analyzer against testdata/src/a, whose
// //want comments mark the lines that must be flagged. Lines without a //want
// comment must NOT be flagged (false positives fail the test just as missed
// diagnostics do).
func TestTypedAssertAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), TypedAssertAnalyzer, "a")
}
