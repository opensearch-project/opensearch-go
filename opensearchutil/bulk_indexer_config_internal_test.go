// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchutil

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewBulkIndexerRefusesNegativeByteLimits: a negative byte limit has no meaning,
// and one that got through would break the limit it names, so the indexer is not built.
func TestNewBulkIndexerRefusesNegativeByteLimits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		field string
		set   func(*BulkIndexerConfig)
	}{
		{name: "FlushBytes", field: "FlushBytes", set: func(c *BulkIndexerConfig) { c.FlushBytes = -1 }},
		{name: "MaxMemoryBytes", field: "MaxMemoryBytes", set: func(c *BulkIndexerConfig) { c.MaxMemoryBytes = -1 }},
		{name: "a very negative MaxMemoryBytes", field: "MaxMemoryBytes", set: func(c *BulkIndexerConfig) { c.MaxMemoryBytes = -1 << 40 }},
		{name: "NodeBytes", field: "NodeBytes", set: func(c *BulkIndexerConfig) { c.NodeBytes = -1 }},
		{name: "Sizing.MinBytes", field: "Sizing.MinBytes", set: func(c *BulkIndexerConfig) { c.Sizing.MinBytes = -1 }},
		{name: "Sizing.InitialBytes", field: "Sizing.InitialBytes", set: func(c *BulkIndexerConfig) { c.Sizing.InitialBytes = -1 }},
		{name: "Sizing.StepBytes", field: "Sizing.StepBytes", set: func(c *BulkIndexerConfig) { c.Sizing.StepBytes = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := BulkIndexerConfig{Client: newBulkTestClient(t, defaultRoundTripFunc)}
			tt.set(&cfg)

			bi, err := NewBulkIndexer(cfg)
			require.ErrorIs(t, err, ErrInvalidConfig)
			require.ErrorContains(t, err, tt.field)
			require.Nil(t, bi)
		})
	}
}

// TestNewBulkIndexerAcceptsZeroByteLimits: zero selects the default for each byte
// limit, and is not refused with the negatives.
func TestNewBulkIndexerAcceptsZeroByteLimits(t *testing.T) {
	t.Parallel()

	bi, err := NewBulkIndexer(BulkIndexerConfig{Client: newBulkTestClient(t, defaultRoundTripFunc)})
	require.NoError(t, err)
	require.NoError(t, bi.Close(t.Context()))
}
