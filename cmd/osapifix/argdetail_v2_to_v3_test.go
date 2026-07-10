// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestArgDetailSeedOpsPresent(t *testing.T) {
	ping, ok := argDetailV2toV3["Ping"]
	require.True(t, ok, "Ping arg-detail missing")
	require.Empty(t, ping.Positionals, "Ping has no positional args")
	require.Equal(t, destContext, ping.Options["WithContext"].Kind)
	require.Equal(t, destDropped, ping.Options["WithFilterPath"].Kind)

	exists, ok := argDetailV2toV3["Indices.Exists"]
	require.True(t, ok, "Indices.Exists arg-detail missing")
	require.Equal(t, "Indices", exists.Positionals[0].ReqField)
	require.Equal(t, destParams, exists.Options["WithAllowNoIndices"].Kind)
	require.Equal(t, "AllowNoIndices", exists.Options["WithAllowNoIndices"].Field)
}
