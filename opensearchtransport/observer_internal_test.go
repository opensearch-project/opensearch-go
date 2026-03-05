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

	"github.com/stretchr/testify/require"
)

func TestBaseConnectionObserver_NoOps(_ *testing.T) {
	var obs BaseConnectionObserver
	event := ConnectionEvent{URL: "http://localhost:9200"}

	// All 14 methods should be callable without panic
	obs.OnPromote(event)
	obs.OnDemote(event)
	obs.OnOverloadDetected(event)
	obs.OnOverloadCleared(event)
	obs.OnDiscoveryAdd(event)
	obs.OnDiscoveryRemove(event)
	obs.OnDiscoveryUnchanged(event)
	obs.OnHealthCheckPass(event)
	obs.OnHealthCheckFail(event)
	obs.OnStandbyPromote(event)
	obs.OnStandbyDemote(event)
	obs.OnWarmupRequest(event)
	obs.OnRoute(RouteEvent{})
	obs.OnShardMapInvalidation(ShardMapInvalidationEvent{})
}

func TestNewConnectionEvent(t *testing.T) {
	u, err := url.Parse("http://node1.example.com:9200")
	require.NoError(t, err)

	conn := &Connection{
		URL:   u,
		ID:    "node-abc",
		Name:  "node-1",
		Roles: roleSet{"data": {}, "ingest": {}},
	}
	conn.storeVersion("2.11.0")
	conn.failures.Store(3)
	conn.weight.Store(2)

	event := newConnectionEvent("roundrobin", conn, lifecycleCounts{active: 5, dead: 2})

	require.Equal(t, "http://node1.example.com:9200", event.URL)
	require.Equal(t, "node-abc", event.ID)
	require.Equal(t, "node-1", event.Name)
	require.Equal(t, "2.11.0", event.Version)
	require.Equal(t, "roundrobin", event.PoolName)
	require.Equal(t, int64(3), event.Failures)
	require.Equal(t, 2, event.Weight)
	require.Equal(t, 5, event.ActiveCount)
	require.Equal(t, 2, event.DeadCount)
	require.Equal(t, 0, event.StandbyCount)
	require.False(t, event.Timestamp.IsZero())

	// Roles should be populated (order may vary)
	require.Len(t, event.Roles, 2)
	require.Contains(t, event.Roles, "data")
	require.Contains(t, event.Roles, "ingest")
}

func TestNewConnectionEvent_EmptyRoles(t *testing.T) {
	u, err := url.Parse("http://node2.example.com:9200")
	require.NoError(t, err)

	conn := &Connection{
		URL: u,
		ID:  "node-xyz",
	}

	event := newConnectionEvent("test-pool", conn, lifecycleCounts{active: 1})

	require.Equal(t, "http://node2.example.com:9200", event.URL)
	require.Equal(t, "node-xyz", event.ID)
	require.Nil(t, event.Roles)
	require.Equal(t, 1, event.Weight) // default weight when unset
}

func TestNewConnectionEventWithStandby(t *testing.T) {
	u, err := url.Parse("http://node3.example.com:9200")
	require.NoError(t, err)

	conn := &Connection{
		URL:   u,
		ID:    "node-standby",
		Name:  "node-3",
		Roles: roleSet{"data": {}},
	}
	conn.storeVersion("2.12.0")
	conn.failures.Store(1)

	event := newConnectionEvent("role:data", conn, lifecycleCounts{active: 3, dead: 1, standby: 4})

	require.Equal(t, "http://node3.example.com:9200", event.URL)
	require.Equal(t, "role:data", event.PoolName)
	require.Equal(t, 3, event.ActiveCount)
	require.Equal(t, 1, event.DeadCount)
	require.Equal(t, 4, event.StandbyCount)
	require.Equal(t, int64(1), event.Failures)
	require.Len(t, event.Roles, 1)
	require.Contains(t, event.Roles, "data")
}

func TestObserverFromAtomic(t *testing.T) {
	t.Run("nil pointer returns nil", func(t *testing.T) {
		result := observerFromAtomic(nil)
		require.Nil(t, result)
	})

	t.Run("pointer storing nil returns nil", func(t *testing.T) {
		var p atomic.Pointer[ConnectionObserver]
		result := observerFromAtomic(&p)
		require.Nil(t, result)
	})

	t.Run("pointer storing observer returns observer", func(t *testing.T) {
		var p atomic.Pointer[ConnectionObserver]
		var obs ConnectionObserver = &BaseConnectionObserver{}
		p.Store(&obs)

		result := observerFromAtomic(&p)
		require.NotNil(t, result)
		require.Equal(t, obs, result)
	})
}
