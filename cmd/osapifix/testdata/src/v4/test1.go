// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package v4

import (
	"github.com/stretchr/testify/require"
)

type T struct{}

func (T) Errorf(string, ...interface{}) {}

// resp models a v5 generated struct: precise, mostly-pointer numeric fields.
type resp struct {
	Version *int64 // was int in v4 - the canonical hazard
	Count   int64
	Batches int
}

func good(t T, r resp) {
	// Matching types: must NOT be flagged.
	require.Equal(t, int64(5), r.Count)
	require.Greater(t, r.Count, int64(0))
	require.Equal(t, 1, r.Batches)
	if r.Version != nil {
		require.Greater(t, *r.Version, int64(0))
	}
	require.Equal(t, "x", "x")
	require.True(t, r.Count > 0)
	require.NoError(t, nil)
}

func bad(t T, r resp) {
	// Pointer vs untyped-int literal: the exact bug that started this.
	require.Greater(t, r.Version, 0) // want `testify Greater: operands have mismatched types .*one side is a pointer`

	// int64 field vs untyped-int literal that defaults to int.
	require.Equal(t, 5, r.Count) // want `testify Equal: operands have mismatched types .*numeric kinds differ`

	// int field vs explicit int64 literal.
	require.Equal(t, int64(0), r.Batches) // want `testify Equal: operands have mismatched types .*numeric kinds differ`

	// Pointer vs value of the same base type.
	require.Equal(t, r.Version, r.Count) // want `testify Equal: operands have mismatched types .*one side is a pointer`
}

// interfaceOperands must NOT be flagged: the dynamic type is unknowable.
func interfaceOperands(t T, a, b interface{}) {
	require.Equal(t, a, b)
	require.Equal(t, a, 5)
}

// notTestify must NOT be flagged: only testify calls are in scope.
func notTestify(r resp) {
	myGreater(r.Version, 0)
}

func myGreater(a interface{}, b interface{}) {}
