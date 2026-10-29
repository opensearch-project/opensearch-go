// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/cmd/osapilint/v5/internal/apirev"
)

func planV5toV5_2(t *testing.T) hopPlan {
	t.Helper()
	plans, err := planChain(majorLine(5), version{major: 5, minor: 2})
	require.NoError(t, err)
	require.Len(t, plans, 1, "v5->v5.2 is a single hop")
	return plans[0]
}

// TestHopV5toV5_2_RulesEveryVanishedField holds the hop to a stronger bar than
// the fail-loud default: every field that vanishes between the v5.1.3 and v5.2
// surfaces has a ruling, so no consumer hits an unclassified field.
func TestHopV5toV5_2_RulesEveryVanishedField(t *testing.T) {
	t.Parallel()

	for qualified, sd := range planV5toV5_2(t).delta.Structs {
		for _, ch := range sd.Changes {
			require.NotEqualf(t, apirev.KindUnclassified, ch.Kind, "%s#%s is unclassified", qualified, ch.From)
		}
	}
}

// TestHopV5toV5_2_KeepsImportPath checks the hop within v5 maps the module path
// to itself, which RewriteImports reports as no edit.
func TestHopV5toV5_2_KeepsImportPath(t *testing.T) {
	t.Parallel()

	p := planV5toV5_2(t)
	require.Equal(t, [][2]string{{modulePath(5), modulePath(5)}}, p.importPrefixes)
}

// TestHopV5toV5_2_RemovedTypes checks the types v5.2 drops with no rename are
// recorded, so a reference to one is reported MANUAL.
func TestHopV5toV5_2_RemovedTypes(t *testing.T) {
	t.Parallel()

	removed := planV5toV5_2(t).delta.RemovedTypes
	for _, typ := range []string{"MLHitsTotal", "RollupsIntervalCron"} {
		require.Truef(t, removed[v5api+"."+typ], "%q has no v5.2 counterpart", typ)
	}
}

// TestDetectSourceReadsMinor checks the source line comes from the version of
// the major's module that go.mod requires.
func TestDetectSourceReadsMinor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		require string
		want    version
	}{
		{name: "v5.1 is on the v5 line", require: "v5.1.3", want: majorLine(5)},
		{name: "v5.2 starts its own line", require: "v5.2.0", want: version{major: 5, minor: 2}},
		{name: "a pseudo-version uses its base minor", require: "v5.2.1-0.20261029000000-abcdefabcdef", want: version{major: 5, minor: 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			extractTxtar(t, filepath.Join("testdata", "corpus", "stubs.txtar"), root)
			consumer := filepath.Join(root, "consumer")
			writeModule(t, consumer, map[string]string{
				"go.mod": "module example.com/minor\n\ngo 1.24\n\n" +
					"require github.com/opensearch-project/opensearch-go/v5 " + tt.require + "\n\n" +
					"replace github.com/opensearch-project/opensearch-go/v5 => ../stub-v5\n",
				"consumer.go": "package consumer\n\nimport _ \"github.com/opensearch-project/opensearch-go/v5/opensearchapi\"\n",
			})

			got, warnings, err := detectSource(t.Context(), consumer)
			require.NoError(t, err)
			require.Empty(t, warnings)
			require.Equal(t, tt.want, got)
		})
	}
}
