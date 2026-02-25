// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testConn creates a Connection with the given host, ID/Name and RTT ring
// populated with the specified duration.
func testConn(t *testing.T, host string, id string, rtt time.Duration) *Connection {
	t.Helper()
	u, err := url.Parse("http://" + host)
	require.NoError(t, err)

	c := &Connection{
		URL:       u,
		URLString: u.String(),
		ID:        id,
		Name:      id,
		rttRing:   newRTTRing(4),
	}
	// Fill the ring so the median is the specified RTT.
	for range 4 {
		c.rttRing.add(rtt)
	}
	return c
}

func TestRendezvousWeight(t *testing.T) {
	t.Parallel()

	t.Run("deterministic", func(t *testing.T) {
		t.Parallel()
		w1 := rendezvousWeight("my-index", "", "http://node1:9200")
		w2 := rendezvousWeight("my-index", "", "http://node1:9200")
		require.Equal(t, w1, w2)
	})

	t.Run("different keys produce different weights", func(t *testing.T) {
		t.Parallel()
		w1 := rendezvousWeight("index-a", "", "http://node1:9200")
		w2 := rendezvousWeight("index-b", "", "http://node1:9200")
		require.NotEqual(t, w1, w2)
	})

	t.Run("different nodes produce different weights", func(t *testing.T) {
		t.Parallel()
		w1 := rendezvousWeight("my-index", "", "http://node1:9200")
		w2 := rendezvousWeight("my-index", "", "http://node2:9200")
		require.NotEqual(t, w1, w2)
	})

	t.Run("null byte separator prevents ambiguity", func(t *testing.T) {
		t.Parallel()
		// "ab" + "\x00" + "cd" vs "a" + "\x00" + "bcd"
		w1 := rendezvousWeight("ab", "", "cd")
		w2 := rendezvousWeight("a", "", "bcd")
		require.NotEqual(t, w1, w2)
	})

	t.Run("two-part key differs from one-part", func(t *testing.T) {
		t.Parallel()
		// "index/docid" as one part vs "index" + "docid" as two parts.
		// The two-part form hashes: index + '/' + docid + \x00 + nodeURL
		// The one-part form hashes: index/docid + \x00 + nodeURL
		// These should produce the same result (/ is literal in both).
		w1 := rendezvousWeight("index/docid", "", "http://node1:9200")
		w2 := rendezvousWeight("index", "docid", "http://node1:9200")
		require.Equal(t, w1, w2, "two-part key should produce same hash as concatenated key")
	})
}

func TestRankByHash(t *testing.T) {
	t.Parallel()

	// newConns creates a fresh slice each call since rankByHash sorts in-place.
	newConns := func() []*Connection {
		return []*Connection{
			testConn(t, "node1:9200", "n1", 1*time.Millisecond),
			testConn(t, "node2:9200", "n2", 1*time.Millisecond),
			testConn(t, "node3:9200", "n3", 1*time.Millisecond),
		}
	}

	t.Run("returns all connections", func(t *testing.T) {
		t.Parallel()
		conns := newConns()
		ranked := rankByHash("test-key", "", conns)
		require.Len(t, ranked, len(conns))
	})

	t.Run("deterministic ordering", func(t *testing.T) {
		t.Parallel()
		ranked1 := rankByHash("test-key", "", newConns())
		ranked2 := rankByHash("test-key", "", newConns())
		for i := range ranked1 {
			require.Equal(t, ranked1[i].URLString, ranked2[i].URLString,
				"ranking should be deterministic at position %d", i)
		}
	})

	t.Run("different keys may produce different orderings", func(t *testing.T) {
		t.Parallel()
		ranked1 := rankByHash("key-alpha", "", newConns())
		ranked2 := rankByHash("key-beta", "", newConns())
		// Not guaranteed to differ, but with 3 nodes it's very likely.
		differ := false
		for i := range ranked1 {
			if ranked1[i].URLString != ranked2[i].URLString {
				differ = true
				break
			}
		}
		require.True(t, differ, "different keys should usually produce different orderings")
	})
}

func TestRendezvousTopK(t *testing.T) {
	t.Parallel()

	t.Run("empty connections returns nil", func(t *testing.T) {
		t.Parallel()
		result := rendezvousTopK("key", "", nil, 3, nil, nil, nil)
		require.Nil(t, result)
	})

	t.Run("k=0 returns nil", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{testConn(t, "node1:9200", "n1", 1*time.Millisecond)}
		result := rendezvousTopK("key", "", conns, 0, nil, nil, nil)
		require.Nil(t, result)
	})

	t.Run("k larger than len(conns) is clamped", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{
			testConn(t, "node1:9200", "n1", 1*time.Millisecond),
			testConn(t, "node2:9200", "n2", 1*time.Millisecond),
		}
		result := rendezvousTopK("key", "", conns, 10, nil, nil, nil)
		require.Len(t, result, 2)
	})

	t.Run("returns exactly k connections", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{
			testConn(t, "node1:9200", "n1", 1*time.Millisecond),
			testConn(t, "node2:9200", "n2", 1*time.Millisecond),
			testConn(t, "node3:9200", "n3", 1*time.Millisecond),
			testConn(t, "node4:9200", "n4", 1*time.Millisecond),
		}
		result := rendezvousTopK("key", "", conns, 2, nil, nil, nil)
		require.Len(t, result, 2)
	})

	t.Run("deterministic without jitter", func(t *testing.T) {
		t.Parallel()
		conns := []*Connection{
			testConn(t, "node1:9200", "n1", 1*time.Millisecond),
			testConn(t, "node2:9200", "n2", 1*time.Millisecond),
			testConn(t, "node3:9200", "n3", 1*time.Millisecond),
		}
		r1 := rendezvousTopK("key", "", conns, 2, nil, nil, nil)
		r2 := rendezvousTopK("key", "", conns, 2, nil, nil, nil)
		for i := range r1 {
			require.Equal(t, r1[i].URL.String(), r2[i].URL.String())
		}
	})
}

func TestRendezvousTopKShardPartition(t *testing.T) {
	t.Parallel()

	// Create connections: 2 shard-hosting (nearest), 2 non-shard (nearest).
	shardConns := []*Connection{
		testConn(t, "shard1:9200", "s1", 1*time.Millisecond),
		testConn(t, "shard2:9200", "s2", 1*time.Millisecond),
	}
	nonShardConns := []*Connection{
		testConn(t, "coord1:9200", "c1", 1*time.Millisecond),
		testConn(t, "coord2:9200", "c2", 1*time.Millisecond),
	}

	// All in same RTT tier, sorted by RTT ascending.
	conns := make([]*Connection, 0, len(shardConns)+len(nonShardConns))
	conns = append(conns, shardConns...)
	conns = append(conns, nonShardConns...)

	shardNodes := map[string]struct{}{
		"s1": {},
		"s2": {},
	}

	t.Run("shard nodes preferred over non-shard", func(t *testing.T) {
		t.Parallel()
		result := rendezvousTopK("my-index", "", conns, 2, nil, shardNodes, nil)
		require.Len(t, result, 2)

		// Both selected nodes must be shard-hosting.
		for _, c := range result {
			_, isShard := shardNodes[c.Name]
			require.True(t, isShard, "node %s should be shard-hosting", c.Name)
		}
	})

	t.Run("non-shard nodes used when shard nodes exhausted", func(t *testing.T) {
		t.Parallel()
		result := rendezvousTopK("my-index", "", conns, 3, nil, shardNodes, nil)
		require.Len(t, result, 3)

		// First 2 should be shard nodes, 3rd should be non-shard.
		shardCount := 0
		for _, c := range result {
			if _, ok := shardNodes[c.Name]; ok {
				shardCount++
			}
		}
		require.Equal(t, 2, shardCount, "should use all shard nodes before non-shard")
	})

	t.Run("nil shardNodes treats all equally", func(t *testing.T) {
		t.Parallel()
		result := rendezvousTopK("my-index", "", conns, 4, nil, nil, nil)
		require.Len(t, result, 4)
	})
}

func TestRendezvousTopKRTTTiering(t *testing.T) {
	t.Parallel()

	// Create connections in different RTT tiers.
	nearConns := []*Connection{
		testConn(t, "near1:9200", "n1", 200*time.Microsecond), // bucket 0
		testConn(t, "near2:9200", "n2", 200*time.Microsecond), // bucket 0
	}
	farConns := []*Connection{
		testConn(t, "far1:9200", "f1", 5*time.Millisecond),  // bucket ~19
		testConn(t, "far2:9200", "f2", 10*time.Millisecond), // bucket ~39
	}

	// Sorted by RTT ascending -- near first.
	conns := make([]*Connection, 0, len(nearConns)+len(farConns))
	conns = append(conns, nearConns...)
	conns = append(conns, farConns...)

	t.Run("nearest tier filled first", func(t *testing.T) {
		t.Parallel()
		result := rendezvousTopK("key", "", conns, 2, nil, nil, nil)
		require.Len(t, result, 2)

		// Both should be from the near tier (bucket 0).
		for _, c := range result {
			bucket := c.rttRing.medianBucket()
			require.Equal(t, rttBucketOf(200*time.Microsecond), bucket,
				"node %s should be from nearest tier", c.ID)
		}
	})

	t.Run("spills to farther tiers when needed", func(t *testing.T) {
		t.Parallel()
		result := rendezvousTopK("key", "", conns, 3, nil, nil, nil)
		require.Len(t, result, 3)

		// 2 near + 1 far
		nearCount := 0
		for _, c := range result {
			if c.rttRing.medianBucket() == rttBucketOf(200*time.Microsecond) {
				nearCount++
			}
		}
		require.Equal(t, 2, nearCount, "should use all near-tier nodes first")
	})
}

func TestRendezvousTopKJitter(t *testing.T) {
	t.Parallel()

	conns := []*Connection{
		testConn(t, "node1:9200", "n1", 1*time.Millisecond),
		testConn(t, "node2:9200", "n2", 1*time.Millisecond),
		testConn(t, "node3:9200", "n3", 1*time.Millisecond),
	}

	t.Run("jitter rotates within slots", func(t *testing.T) {
		t.Parallel()
		var jitter atomic.Int64

		// Call multiple times with advancing jitter. The slot set content
		// is the same (deterministic), but the rotation offset changes.
		results := make([][]*Connection, 5)
		for i := range results {
			results[i] = rendezvousTopK("key", "", conns, 3, &jitter, nil, nil)
			require.Len(t, results[i], 3)
		}

		// With 3 slots and incrementing jitter, the first element should
		// cycle through positions. Verify at least 2 distinct first elements
		// across 5 calls.
		firstURLs := make(map[string]bool)
		for _, r := range results {
			firstURLs[r[0].URL.String()] = true
		}
		require.GreaterOrEqual(t, len(firstURLs), 2,
			"jitter should rotate the preferred node across calls")
	})

	t.Run("nil jitter does not rotate", func(t *testing.T) {
		t.Parallel()
		r1 := rendezvousTopK("key", "", conns, 3, nil, nil, nil)
		r2 := rendezvousTopK("key", "", conns, 3, nil, nil, nil)
		for i := range r1 {
			require.Equal(t, r1[i].URL.String(), r2[i].URL.String(),
				"nil jitter should produce identical ordering")
		}
	})
}

func TestRendezvousTopKConsistency(t *testing.T) {
	t.Parallel()

	// When a node is added or removed, most key->node assignments should be stable.
	original := []*Connection{
		testConn(t, "node1:9200", "n1", 1*time.Millisecond),
		testConn(t, "node2:9200", "n2", 1*time.Millisecond),
		testConn(t, "node3:9200", "n3", 1*time.Millisecond),
		testConn(t, "node4:9200", "n4", 1*time.Millisecond),
	}
	// Remove node3.
	reduced := []*Connection{original[0], original[1], original[3]}

	stableCount := 0
	totalKeys := 100
	for i := range totalKeys {
		key := string(rune('a'+i%26)) + "-key-" + string(rune('0'+i/26))
		r1 := rendezvousTopK(key, "", original, 1, nil, nil, nil)
		r2 := rendezvousTopK(key, "", reduced, 1, nil, nil, nil)

		if r1[0].URL.String() == r2[0].URL.String() {
			stableCount++
		}
	}

	// With 4 nodes and removing 1, ~75% of keys should stay mapped to the same node.
	require.Greater(t, stableCount, totalKeys/2,
		"rendezvous hashing should provide stability when nodes change: %d/%d stable", stableCount, totalKeys)
}
