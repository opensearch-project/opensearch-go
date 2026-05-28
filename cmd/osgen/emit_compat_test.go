// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/cmd/osgen/emit"
)

func TestCompatFileTarget_MatchesLegacy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		pkg         string
		hasDuration bool
	}{
		{name: "without duration", pkg: opensearchAPIPkgName, hasDuration: false},
		{name: "with duration", pkg: opensearchAPIPkgName, hasDuration: true},
		{name: "plugin without duration", pkg: "knn", hasDuration: false},
		{name: "plugin with duration", pkg: "ism", hasDuration: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			target := emit.NewCompatFile("/tmp/test", tt.pkg, tt.hasDuration)

			newOutput, err := target.Render()
			require.NoError(t, err)

			// Verify structural correctness.
			src := string(newOutput)
			require.Contains(t, src, "package "+tt.pkg)
			require.Contains(t, src, "type Inspect = apiutil.Inspect")
			require.Contains(t, src, `"github.com/opensearch-project/opensearch-go/v4/internal/apiutil"`)

			if tt.hasDuration {
				require.Contains(t, src, `"time"`)
				require.Contains(t, src, "func formatDuration")
				// goimports style: stdlib before local module
				timeIdx := strings.Index(src, `"time"`)
				apiutilIdx := strings.Index(src, `"github.com/opensearch-project/opensearch-go/v4/internal/apiutil"`)
				require.Less(t, timeIdx, apiutilIdx, "stdlib imports should precede local module imports")
			} else {
				require.NotContains(t, src, `"time"`)
				require.NotContains(t, src, "func formatDuration")
			}
		})
	}
}
