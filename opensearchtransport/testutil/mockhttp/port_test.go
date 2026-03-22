// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mockhttp_test

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil/mockhttp"
)

func TestConstants(t *testing.T) {
	t.Run("Scheme constants", func(t *testing.T) {
		require.Equal(t, "http", mockhttp.DefaultOpenSearchSchemeInsecure)
		require.Equal(t, "https", mockhttp.DefaultOpenSearchSchemeSecure)
	})

	t.Run("Host and port defaults", func(t *testing.T) {
		require.Equal(t, "localhost", mockhttp.DefaultOpenSearchHost)
		require.Equal(t, 9200, mockhttp.DefaultOpenSearchPort)
	})

	t.Run("Mock server constants", func(t *testing.T) {
		require.Equal(t, "localhost", mockhttp.MockServerHost)
		require.Equal(t, 9700, mockhttp.MockPortStart)
		require.Equal(t, 9799, mockhttp.MockPortEnd)
		require.Less(t, mockhttp.MockPortStart, mockhttp.MockPortEnd)
	})
}

func TestGetOpenSearchURL(t *testing.T) {
	u := mockhttp.GetOpenSearchURL(t)
	require.NotNil(t, u)
	require.NotEmpty(t, u.Scheme)
	require.NotEmpty(t, u.Host)
}

func TestAllocateAndReleasePort(t *testing.T) {
	t.Run("Allocate from mock pool", func(t *testing.T) {
		port, err := mockhttp.AllocateMockPort("test-alloc")
		require.NoError(t, err)
		require.GreaterOrEqual(t, port, mockhttp.MockPortStart)
		require.LessOrEqual(t, port, mockhttp.MockPortEnd)

		tp := mockhttp.GetPort(port)
		require.Equal(t, port, tp.Port)
		require.Equal(t, "test-alloc", tp.Owner)
		require.Equal(t, "mock", tp.Pool)

		mockhttp.ReleasePort(port)
		tp = mockhttp.GetPort(port)
		require.Zero(t, tp.Port)
	})

	t.Run("AllocatePort with named pool", func(t *testing.T) {
		tp, err := mockhttp.AllocatePort("mock", "named-owner")
		require.NoError(t, err)
		require.GreaterOrEqual(t, tp.Port, mockhttp.MockPortStart)
		require.Equal(t, "named-owner", tp.Owner)
		require.Equal(t, "mock", tp.Pool)
		mockhttp.ReleasePort(tp.Port)
	})

	t.Run("AllocatePort rejects unknown pool", func(t *testing.T) {
		_, err := mockhttp.AllocatePort("nonexistent", "owner")
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported pool")
	})

	t.Run("GetPort returns zero for unallocated port", func(t *testing.T) {
		tp := mockhttp.GetPort(99999)
		require.Zero(t, tp.Port)
		require.Empty(t, tp.Owner)
	})
}

func TestGetMockServerURL(t *testing.T) {
	t.Run("Returns HTTP URL at given port", func(t *testing.T) {
		u := mockhttp.GetMockServerURL(9750)
		require.NotNil(t, u)
		require.Equal(t, "http", u.Scheme)
		require.Equal(t, "localhost", u.Hostname())
		require.Equal(t, "9750", u.Port())
	})

	t.Run("URL is parseable and round-trips", func(t *testing.T) {
		u := mockhttp.GetMockServerURL(9701)
		parsed, err := url.Parse(u.String())
		require.NoError(t, err)
		require.Equal(t, u.Scheme, parsed.Scheme)
		require.Equal(t, u.Host, parsed.Host)
	})
}
