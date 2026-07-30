// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/emit"
)

func TestLowerFirst(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "standard", input: "Returns cluster health.", want: "returns cluster health."},
		{name: "already lower", input: "returns cluster health.", want: "returns cluster health."},
		{name: "acronym", input: "JSON body of the request.", want: "JSON body of the request."},
		{name: "empty", input: "", want: ""},
		{name: "single char", input: "A", want: "a"},
		{name: "single lower", input: "a", want: "a"},
		{name: "two upper (acronym)", input: "HTTP method.", want: "HTTP method."},
		{name: "unicode upper", input: "Étude", want: "étude"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, emit.LowerFirst(tt.input))
		})
	}
}

func TestSplitFirstLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantFirst string
		wantRest  string
	}{
		{name: "single line", input: "Hello world.", wantFirst: "Hello world.", wantRest: ""},
		{
			name:      "blank line separator",
			input:     "First paragraph.\n\nSecond paragraph.",
			wantFirst: "First paragraph.",
			wantRest:  "Second paragraph.",
		},
		{name: "newline no blank", input: "Line one.\nLine two.", wantFirst: "Line one.", wantRest: "Line two."},
		{name: "multiple paragraphs", input: "First.\n\nSecond.\n\nThird.", wantFirst: "First.", wantRest: "Second.\n\nThird."},
		{name: "trailing whitespace", input: "  First.  \n\n  Second.  ", wantFirst: "First.", wantRest: "Second."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotFirst, gotRest := emit.SplitFirstLine(tt.input)
			require.Equal(t, tt.wantFirst, gotFirst)
			require.Equal(t, tt.wantRest, gotRest)
		})
	}
}

func TestMethodComment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		data   emit.MethodDocData
		checks []string
	}{
		{
			name: "full metadata",
			data: emit.MethodDocData{
				MethodName:      "GetRole",
				Group:           "security.get_role",
				Description:     "Retrieves one role.",
				HTTPMethods:     []string{http.MethodGet},
				PrimaryPath:     "/_plugins/_security/api/roles/{role}",
				VersionAdded:    "1.0.0",
				ExcludedDistros: []string{"amazon-managed", "amazon-serverless"},
				DocsURL:         "https://opensearch.org/docs/latest/security/access-control/api/#get-role",
			},
			checks: []string{
				"// GetRole retrieves one role.",
				"// GET /_plugins/_security/api/roles/{role}",
				"// Available: >= 1.0.0.",
				"// Not available on: amazon-managed, amazon-serverless.",
				"// See: https://opensearch.org/docs/latest/security/access-control/api/#get-role",
			},
		},
		{
			name: "no description fallback",
			data: emit.MethodDocData{
				MethodName:  "Health",
				Group:       "security.health",
				HTTPMethods: []string{http.MethodGet},
				PrimaryPath: "/_plugins/_security/health",
			},
			checks: []string{
				"// Health executes the security.health operation.",
				"// GET /_plugins/_security/health",
			},
		},
		{
			name: "multiple HTTP methods",
			data: emit.MethodDocData{
				MethodName:  "Search",
				Group:       "search",
				Description: "Returns results matching a query.",
				HTTPMethods: []string{http.MethodGet, http.MethodPost},
				PrimaryPath: "/{index}/_search",
			},
			checks: []string{
				"// Search returns results matching a query.",
				"// Path: /{index}/_search",
				"// Methods: GET, POST",
			},
		},
		{
			name: "deprecated operation",
			data: emit.MethodDocData{
				MethodName:        "OldGet",
				Group:             "old.get",
				Description:       "Fetches a resource.",
				HTTPMethods:       []string{http.MethodGet},
				PrimaryPath:       "/old/{id}",
				VersionAdded:      "1.0",
				VersionDeprecated: "2.0",
				DeprecationMsg:    "Use NewGet instead.",
			},
			checks: []string{
				"// OldGet fetches a resource.",
				"// GET /old/{id}",
				"// Deprecated: since 2.0.0. Available >= 1.0.0. Use NewGet instead.",
			},
		},
		{
			name: "minimal",
			data: emit.MethodDocData{
				MethodName: "Ping",
				Group:      "ping",
			},
			checks: []string{
				"// Ping executes the ping operation.",
			},
		},
		{
			name: "multi-line description",
			data: emit.MethodDocData{
				MethodName: "Create",
				Group:      "create",
				Description: "Creates a new document in the index.\n\n" +
					"Returns a 409 response when a document with a same ID already exists in the index.",
				HTTPMethods:  []string{http.MethodPut},
				PrimaryPath:  "/{index}/_create/{id}",
				VersionAdded: "1.0",
			},
			checks: []string{
				"// Create creates a new document in the index.",
				"// Returns a 409 response when a document with a same ID already exists in the index.",
				"// PUT /{index}/_create/{id}",
				"// Available: >= 1.0.0.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := emit.MethodComment(tt.data)
			for _, want := range tt.checks {
				require.Contains(t, got, want)
			}
		})
	}
}

func TestFieldComment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field string
		text  string
		want  string
	}{
		{
			name:  "noun phrase takes a copula",
			field: "PhaseTook",
			text:  "The time taken by different phases of the search.",
			want:  "// PhaseTook is the time taken by different phases of the search.",
		},
		{
			name:  "indefinite article is lowercased",
			field: "Reason",
			text:  "A human-readable explanation of the error.",
			want:  "// Reason is a human-readable explanation of the error.",
		},
		{
			name:  "an is lowercased",
			field: "Entry",
			text:  "An entry in the index.",
			want:  "// Entry is an entry in the index.",
		},
		{
			name:  "verb-initial description stays its own sentence",
			field: "Found",
			text:  "Whether the document was found.",
			want:  "// Found. Whether the document was found.",
		},
		{
			name:  "conditional description stays its own sentence",
			field: "Ordered",
			text:  "If `true`, matching terms must appear in order.",
			want:  "// Ordered. If `true`, matching terms must appear in order.",
		},
		{
			name:  "description already leading with the field name is untouched",
			field: "Timeout",
			text:  "Timeout for the request.",
			want:  "// Timeout for the request.",
		},
		{
			name:  "deprecation marker is never prefixed",
			field: "GetTime",
			text:  "Deprecated: use time instead.",
			want:  "// Deprecated: use time instead.",
		},
		{
			name:  "empty description yields no comment",
			field: "Nodes",
			text:  "",
			want:  "",
		},
		{
			name:  "missing field name falls back to the bare description",
			field: "",
			text:  "The number of shards.",
			want:  "// The number of shards.",
		},
		{
			name:  "article-only description is not treated as a noun phrase",
			field: "Value",
			text:  "The",
			want:  "// Value. The",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, emit.FieldComment(tt.field, tt.text))
		})
	}
}

// TestFieldCommentWrapsAtTheSameWidth pins that prefixing does not bypass the
// wrap: the field name counts toward the line budget like any other word.
func TestFieldCommentWrapsAtTheSameWidth(t *testing.T) {
	t.Parallel()

	got := emit.FieldComment(
		"GlobalNativeMemoryUsage",
		"The percentage of native memory currently consumed across every node in the cluster.",
	)

	require.Equal(t,
		"// GlobalNativeMemoryUsage is the percentage of native memory currently\n"+
			"\t// consumed across every node in the cluster.",
		got,
	)
}

// TestFieldCommentCapitalizesSentenceForm covers the spec descriptions that are
// lowercase fragments: promoted to their own sentence, they have to start like
// one.
func TestFieldCommentCapitalizesSentenceForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field string
		text  string
		want  string
	}{
		{
			name:  "lowercase fragment is capitalized",
			field: "Alias",
			text:  "alias name",
			want:  "// Alias. Alias name",
		},
		{
			name:  "backtick opening is left alone",
			field: "Mode",
			text:  "`true` enables the mode.",
			want:  "// Mode. `true` enables the mode.",
		},
		{
			name:  "already capitalized is unchanged",
			field: "Found",
			text:  "Whether the document was found.",
			want:  "// Found. Whether the document was found.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, emit.FieldComment(tt.field, tt.text))
		})
	}
}

// TestConstComment covers enum members, where the description says what the
// value means rather than naming an attribute, so a copula never applies.
func TestConstComment(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		const_ string
		text   string
		want   string
	}{
		{
			name:   "noun phrase stays a sentence, unlike a struct field",
			const_: "NodeRoleDataHot",
			text:   "The node can store hot data.",
			want:   "// NodeRoleDataHot. The node can store hot data.",
		},
		{
			name:   "imperative description stays a sentence",
			const_: "SortModeAvg",
			text:   "Use the average of all values.",
			want:   "// SortModeAvg. Use the average of all values.",
		},
		{
			name:   "lowercase fragment is capitalized",
			const_: "SearchTotalHitsRelationEq",
			text:   "accurate",
			want:   "// SearchTotalHitsRelationEq. Accurate",
		},
		{
			name:   "deprecation marker is never prefixed",
			const_: "NodeRoleLegacy",
			text:   "Deprecated: use data instead.",
			want:   "// Deprecated: use data instead.",
		},
		{
			name:   "empty description yields no comment",
			const_: "NodeRoleML",
			text:   "",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, emit.ConstComment(tt.const_, tt.text))
		})
	}
}
