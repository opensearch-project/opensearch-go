// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"fmt"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/ir"
)

// BreadcrumbsFragment renders comment-only output naming items dropped by
// the version-range filter (--min-version/--max-version). Categories present
// in the rendered output are gated upstream by --version-breadcrumb-{operations,
// fields,params}; this fragment renders whatever is handed to it.
//
// The fragment emits no Go declarations, only a doc-comment block immediately
// after the package clause. An empty exclusion set produces an empty body so
// the file is suppressed.
type BreadcrumbsFragment struct {
	Operations []ir.Exclusion
	Fields     []ir.Exclusion
	Params     []ir.Exclusion
}

// Imports reports that this fragment needs no imports.
func (f *BreadcrumbsFragment) Imports() []Import { return nil }

// Body renders the breadcrumb comment block. Returns an empty string when no
// categories have any entries so the surrounding [File.Render] suppresses
// the output and emit.Build skips writing the file.
func (f *BreadcrumbsFragment) Body() (string, error) {
	if len(f.Operations) == 0 && len(f.Fields) == 0 && len(f.Params) == 0 {
		return "", nil
	}

	var sb strings.Builder
	sb.WriteString("// Items excluded by the version-range filter at code-generation time.\n")
	sb.WriteString("// These are not available in the generated client; use a wider --min-version /\n")
	sb.WriteString("// --max-version (or --remove-deprecated) bound to bring them back.\n")

	writeSection(&sb, "Operations", f.Operations)
	writeSection(&sb, "Query parameters", f.Params)
	writeSection(&sb, "Fields", f.Fields)

	// Bind the comment to a declaration so go/format keeps it adjacent to the
	// package clause (free-floating comments without an anchor get reflowed
	// or detached). The blank-import-style sentinel is a no-op at runtime.
	sb.WriteString("\nvar _ = struct{}{} // breadcrumbs anchor; see comment above\n")
	return sb.String(), nil
}

func writeSection(sb *strings.Builder, title string, items []ir.Exclusion) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintf(sb, "//\n// %s (%d):\n", title, len(items))
	for _, e := range items {
		fmt.Fprintf(sb, "//   - %s: %s\n", e.Name, e.Reason)
	}
}

// newBreadcrumbsFragment returns a BreadcrumbsFragment when at least one
// category has entries, or nil when every category is empty so the caller
// can skip emitting the file entirely.
func newBreadcrumbsFragment(exc ir.Exclusions) *BreadcrumbsFragment {
	if len(exc.Operations) == 0 && len(exc.Fields) == 0 && len(exc.Params) == 0 {
		return nil
	}
	return &BreadcrumbsFragment{
		Operations: exc.Operations,
		Fields:     exc.Fields,
		Params:     exc.Params,
	}
}
