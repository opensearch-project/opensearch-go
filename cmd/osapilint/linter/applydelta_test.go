// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/tools/go/packages"
)

const (
	testV4Root    = "github.com/opensearch-project/opensearch-go/v4"
	testV4API     = testV4Root + "/opensearchapi"
	testAPIName   = "opensearchapi"
	testUnrelated = "net/http" // a path outside every opensearch-go module
)

func TestUnderModule(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{name: "module itself", path: testV4Root, want: true},
		{name: "sub-package", path: testV4API, want: true},
		{name: "nested sub-package", path: testV4Root + "/plugins/security", want: true},
		{name: "sibling sharing the prefix", path: testV4Root + "x/opensearchapi", want: false},
		{name: "other major", path: "github.com/opensearch-project/opensearch-go/v5", want: false},
		{name: "unrelated", path: testUnrelated, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, underModule(tc.path, testV4Root))
		})
	}
}

func TestIsHelperPkgPath(t *testing.T) {
	t.Parallel()
	prefixes := [][2]string{{testV4Root, "github.com/opensearch-project/opensearch-go/v5"}}
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{name: "source root module", path: testV4Root, want: true},
		{name: "source opensearchapi", path: testV4API, want: true},
		{name: "any opensearchapi", path: "github.com/opensearch-project/opensearch-go/v5/opensearchapi", want: true},
		{name: "other source sub-package", path: testV4Root + "/opensearchtransport", want: false},
		{name: "unrelated", path: testUnrelated, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, isHelperPkgPath(tc.path, prefixes))
		})
	}
}

func TestSourceImportsOf(t *testing.T) {
	t.Parallel()
	prefixes := [][2]string{{testV4Root, "github.com/opensearch-project/opensearch-go/v5"}}
	resolved := map[string]*packages.Package{
		testV4Root: {Name: "opensearch"},
		testV4API:  {Name: testAPIName},
	}
	for _, tc := range []struct {
		name    string
		imports string                       // import specs, one per line
		pkgs    map[string]*packages.Package // pkg.Imports
		want    map[string]string            // import path -> identifier its references use
	}{
		{
			name:    "unnamed imports take the package's real name",
			imports: `"` + testV4Root + `"` + "\n" + `"` + testV4API + `"`,
			pkgs:    resolved,
			want:    map[string]string{testV4Root: "opensearch", testV4API: testAPIName},
		},
		{
			name:    "aliased import takes its alias",
			imports: `osv4 "` + testV4Root + `"`,
			pkgs:    resolved,
			want:    map[string]string{testV4Root: "osv4"},
		},
		{
			name:    "unnamed import that does not resolve is skipped",
			imports: `"` + testV4Root + `"`,
			pkgs:    map[string]*packages.Package{},
			want:    map[string]string{},
		},
		{
			name:    "blank and dot imports are skipped",
			imports: `_ "` + testV4Root + `"` + "\n" + `. "` + testV4API + `"`,
			pkgs:    resolved,
			want:    map[string]string{},
		},
		{
			name:    "imports outside the source module are skipped",
			imports: `"` + testUnrelated + `"` + "\n" + `"` + testV4Root + `x"`,
			pkgs:    map[string]*packages.Package{testUnrelated: {Name: "http"}, testV4Root + "x": {Name: "x"}},
			want:    map[string]string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := "package p\n\nimport (\n" + tc.imports + "\n)\n"
			file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.ImportsOnly)
			require.NoError(t, err)

			got := map[string]string{}
			for _, si := range sourceImportsOf(file, &packages.Package{Imports: tc.pkgs}, prefixes) {
				got[strings.Trim(si.spec.Path.Value, `"`)] = si.name
			}
			require.Equal(t, tc.want, got)
		})
	}
}
