// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCompareVersion_PreReleaseOrdering(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		a, b   string
		expect int
	}{
		{"identical releases", "v2.20.0", "v2.20.0", 0},
		{"snapshot < release of same base", "v2.20.0-SNAPSHOT", "v2.20.0", -1},
		{"release > snapshot of same base", "v2.20.0", "v2.20.0-SNAPSHOT", +1},
		{"snapshot < alpha", "v2.20.0-snapshot", "v2.20.0-alpha", -1},
		{"alpha > snapshot", "v2.20.0-alpha", "v2.20.0-snapshot", +1},
		{"alpha < beta", "v2.20.0-alpha", "v2.20.0-beta", -1},
		{"beta > alpha", "v2.20.0-beta", "v2.20.0-alpha", +1},
		{"beta < rc", "v2.20.0-beta", "v2.20.0-rc", -1},
		{"rc > beta", "v2.20.0-rc", "v2.20.0-beta", +1},
		{"rc < release", "v2.20.0-rc", "v2.20.0", -1},
		{"release > rc", "v2.20.0", "v2.20.0-rc", +1},
		{"rc.1 < rc.2", "v2.20.0-rc.1", "v2.20.0-rc.2", -1},
		{"rc.2 > rc.1", "v2.20.0-rc.2", "v2.20.0-rc.1", +1},
		{"rc1 < rc2 (no separator)", "v2.20.0-rc1", "v2.20.0-rc2", -1},
		{"rc3 > rc2 (no separator)", "v2.20.0-rc3", "v2.20.0-rc2", +1},
		{"alpha1 < beta1 (rank wins over suffix)", "v2.20.0-alpha1", "v2.20.0-beta1", -1},
		{"rc1 > beta9 (rank wins over higher suffix)", "v2.20.0-rc1", "v2.20.0-beta9", +1},
		{"alpha9 < beta1 (rank wins over higher suffix)", "v2.20.0-alpha9", "v2.20.0-beta1", -1},
		{"beta1 > alpha9 (rank wins over higher suffix)", "v2.20.0-beta1", "v2.20.0-alpha9", +1},
		{"snapshot9 < alpha1 (rank wins)", "v2.20.0-snapshot9", "v2.20.0-alpha1", -1},
		{"alpha1 > snapshot9 (rank wins)", "v2.20.0-alpha1", "v2.20.0-snapshot9", +1},
		{"beta3 < rc1 (rank wins)", "v2.20.0-beta3", "v2.20.0-rc1", -1},
		{"alpha.1 < beta.1 (dotted form)", "v2.20.0-alpha.1", "v2.20.0-beta.1", -1},
		{"beta.1 > alpha.1 (dotted form)", "v2.20.0-beta.1", "v2.20.0-alpha.1", +1},
		{"snapshot of higher base > release of lower base", "v2.21.0-SNAPSHOT", "v2.20.0", +1},
		{"release of higher base > snapshot of lower base", "v2.21.0", "v2.20.0-SNAPSHOT", +1},
		{"release of lower base < snapshot of higher base", "v2.20.0", "v2.21.0-SNAPSHOT", -1},
		{"case-insensitive snapshot", "v2.20.0-Snapshot", "v2.20.0-SNAPSHOT", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := compareVersion(tt.a, tt.b)
			require.Equal(t, tt.expect, got, "compareVersion(%q, %q)", tt.a, tt.b)
		})
	}
}
