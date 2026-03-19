// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestMetrics_String(t *testing.T) {
	t.Parallel()

	t.Run("empty metrics", func(t *testing.T) {
		t.Parallel()
		m := Metrics{}
		s := m.String()
		require.Contains(t, s, "Requests:0")
		require.Contains(t, s, "Failures:0")
		require.Contains(t, s, "Connections: []")
	})

	t.Run("with responses", func(t *testing.T) {
		t.Parallel()
		m := Metrics{
			Requests:  10,
			Failures:  2,
			Responses: map[int]int{200: 8, 500: 2},
		}
		s := m.String()
		require.Contains(t, s, "Requests:10")
		require.Contains(t, s, "Failures:2")
		require.Contains(t, s, "Responses:")
	})

	t.Run("with connections", func(t *testing.T) {
		t.Parallel()
		cm := ConnectionMetric{URL: "http://localhost:9200"}
		m := Metrics{
			Connections: []fmt.Stringer{cm},
		}
		s := m.String()
		require.Contains(t, s, "http://localhost:9200")
	})

	t.Run("with pools", func(t *testing.T) {
		t.Parallel()
		m := Metrics{
			Pools: []PoolSnapshot{
				{Name: "search", Enabled: true, ActiveCount: 3},
			},
		}
		s := m.String()
		require.Contains(t, s, "Pools:")
		require.Contains(t, s, "search")
	})
}

func TestConnectionMetric_String(t *testing.T) {
	t.Parallel()

	t.Run("basic", func(t *testing.T) {
		t.Parallel()
		cm := ConnectionMetric{URL: "http://localhost:9200"}
		s := cm.String()
		require.Contains(t, s, "http://localhost:9200")
		require.Contains(t, s, "state=")
	})

	t.Run("with roles and name", func(t *testing.T) {
		t.Parallel()
		cm := ConnectionMetric{URL: "http://node1:9200"}
		cm.Meta.Roles = []string{"data", "ingest"}
		cm.Meta.Name = "node-1"
		s := cm.String()
		require.Contains(t, s, "roles=")
		require.Contains(t, s, "name=node-1")
	})

	t.Run("with failures", func(t *testing.T) {
		t.Parallel()
		cm := ConnectionMetric{URL: "http://node1:9200", Failures: 5}
		s := cm.String()
		require.Contains(t, s, "failures=5")
	})

	t.Run("with dead_since", func(t *testing.T) {
		t.Parallel()
		now := time.Now()
		cm := ConnectionMetric{URL: "http://node1:9200", DeadSince: &now}
		s := cm.String()
		require.Contains(t, s, "dead_since=")
	})

	t.Run("with overloaded_since", func(t *testing.T) {
		t.Parallel()
		now := time.Now()
		cm := ConnectionMetric{
			URL:             "http://node1:9200",
			IsOverloaded:    true,
			OverloadedSince: &now,
		}
		s := cm.String()
		require.Contains(t, s, "overloaded_since=")
	})
}

func TestPoolSnapshot_String(t *testing.T) {
	t.Parallel()

	t.Run("enabled pool", func(t *testing.T) {
		t.Parallel()
		ps := PoolSnapshot{
			Name:          "search",
			Enabled:       true,
			ActiveListCap: 5,
			ActiveCount:   3,
			StandbyCount:  2,
			DeadCount:     0,
			Requests:      100,
			Successes:     95,
			Failures:      5,
		}
		s := ps.String()
		require.Contains(t, s, `"search"`)
		require.Contains(t, s, "(on")
		require.Contains(t, s, "cap=5")
		require.Contains(t, s, "active=3")
	})

	t.Run("disabled pool", func(t *testing.T) {
		t.Parallel()
		ps := PoolSnapshot{Name: "write", Enabled: false}
		s := ps.String()
		require.Contains(t, s, "(off")
	})
}

func TestOpensearchShardHash_Supplementary(t *testing.T) {
	t.Parallel()

	t.Run("BMP characters", func(t *testing.T) {
		t.Parallel()
		// ASCII string -- must produce a deterministic hash
		h := opensearchShardHash("doc123")
		require.NotZero(t, h)
	})

	t.Run("supplementary character triggers surrogate pair", func(t *testing.T) {
		t.Parallel()
		// U+1F600 (😀) is above U+FFFF, exercising the surrogate pair branch
		h := opensearchShardHash("doc\U0001F600")
		require.NotZero(t, h)

		// Must differ from the BMP-only version
		hBMP := opensearchShardHash("doc?")
		require.NotEqual(t, h, hBMP)
	})

	t.Run("long string uses heap buffer", func(t *testing.T) {
		t.Parallel()
		// > 64 code units -> heap allocation path (buf = make([]byte, n))
		var longStr strings.Builder
		for range 70 {
			longStr.WriteString("ab")
		}
		h := opensearchShardHash(longStr.String())
		require.NotZero(t, h)
	})
}

func TestRttBucket_Micros(t *testing.T) {
	t.Parallel()

	t.Run("zero bucket returns 0", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, durationMicros(0), rttBucket(0).Micros())
	})

	t.Run("negative bucket returns 0", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, durationMicros(0), rttBucket(-1).Micros())
	})

	t.Run("bucket 8 returns 256us", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, durationMicros(256), rttBucket(8).Micros())
	})

	t.Run("bucket 10 returns 1024us", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, durationMicros(1024), rttBucket(10).Micros())
	})
}

func TestRttRingSizeFor(t *testing.T) {
	t.Parallel()

	t.Run("zero intervals returns default", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, defaultRTTRingSize, rttRingSizeFor(0, 0))
	})

	t.Run("negative discover returns default", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, defaultRTTRingSize, rttRingSizeFor(-1, 5*time.Second))
	})

	t.Run("negative resurrect returns default", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, defaultRTTRingSize, rttRingSizeFor(30*time.Second, -1))
	})

	t.Run("30s/5s gives 12", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, 12, rttRingSizeFor(30*time.Second, 5*time.Second))
	})

	t.Run("10s/5s gives 4", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, 4, rttRingSizeFor(10*time.Second, 5*time.Second))
	})

	t.Run("5s/5s gives 2", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, 2, rttRingSizeFor(5*time.Second, 5*time.Second))
	})
}
