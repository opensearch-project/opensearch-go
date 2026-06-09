// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

// CompatInspectFragment renders the Inspect type alias.
type CompatInspectFragment struct{}

// Imports returns the imports the Inspect type-alias fragment needs.
func (f *CompatInspectFragment) Imports() []Import {
	return []Import{{Path: "github.com/opensearch-project/opensearch-go/v5/internal/apiutil"}}
}

// Body renders the Inspect type-alias declaration.
func (f *CompatInspectFragment) Body() (string, error) {
	return "// Inspect represents the struct returned by Inspect(), " +
		"its main use is to return the opensearch.Response to the user.\n" +
		"type Inspect = apiutil.Inspect\n", nil
}

// CompatDurationFragment renders the formatDuration helper.
type CompatDurationFragment struct{}

// Imports returns the imports the formatDuration fragment needs.
func (f *CompatDurationFragment) Imports() []Import {
	return []Import{
		{Path: "time"},
		{Path: "github.com/opensearch-project/opensearch-go/v5/internal/apiutil"},
	}
}

// Body renders the formatDuration helper function.
func (f *CompatDurationFragment) Body() (string, error) {
	return "func formatDuration(d time.Duration) string { return apiutil.FormatDuration(d) }\n", nil
}

// NewCompatFile builds a Target for compat_gen.go.
func NewCompatFile(outDir, pkg string, hasDuration bool) Target {
	frags := []Fragment{&CompatInspectFragment{}}
	if hasDuration {
		frags = append(frags, &CompatDurationFragment{})
	}
	return &File{
		FilePath:  outDir + "/compat_gen.go",
		Package:   pkg,
		Fragments: frags,
	}
}
