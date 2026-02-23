// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
//go:build integration && (core || opensearchtransport)

package opensearchtransport_test

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi/testutil"
	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport"
)

// rotationObserver records standby promotion and demotion events using the
// ConnectionObserver interface. This gives deterministic visibility into
// rotation behavior without relying on which node happens to serve a request
// after cap enforcement's random shuffle.
type rotationObserver struct {
	opensearchtransport.BaseConnectionObserver

	mu                    sync.Mutex
	promoted              []string // node names promoted from standby
	demoted               []string // node names demoted to standby
	lastDemoteActiveSnap  int      // activeCount from the most recent demotion event
	lastDemoteStandbySnap int      // StandbyCount from the most recent demotion event
	lastDemoteDeadSnap    int      // DeadCount from the most recent demotion event
}

func (o *rotationObserver) OnStandbyPromote(event opensearchtransport.ConnectionEvent) {
	o.mu.Lock()
	o.promoted = append(o.promoted, event.Name)
	o.mu.Unlock()
}

func (o *rotationObserver) OnStandbyDemote(event opensearchtransport.ConnectionEvent) {
	o.mu.Lock()
	o.demoted = append(o.demoted, event.Name)
	o.lastDemoteActiveSnap = event.ActiveCount
	o.lastDemoteStandbySnap = event.StandbyCount
	o.lastDemoteDeadSnap = event.DeadCount
	o.mu.Unlock()
}

func (o *rotationObserver) promotedNodes() map[string]int {
	o.mu.Lock()
	defer o.mu.Unlock()
	m := make(map[string]int, len(o.promoted))
	for _, name := range o.promoted {
		m[name]++
	}
	return m
}

func (o *rotationObserver) promotionCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.promoted)
}

func (o *rotationObserver) demotionCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.demoted)
}

func (o *rotationObserver) lastDemotionActiveCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastDemoteActiveSnap
}

func (o *rotationObserver) lastDemotionStandbyCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastDemoteStandbySnap
}

func (o *rotationObserver) lastDemotionDeadCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.lastDemoteDeadSnap
}

// standbyTestConfig returns a transport Config tuned for standby rotation tests.
// Uses longer health check timeouts than the standard test config because
// standby tests are sensitive to transient health check failures under load.
func standbyTestConfig(t *testing.T) opensearchtransport.Config {
	t.Helper()
	cfg := testConfigWithAuth(t)
	cfg.ActiveListCap = 1
	cfg.StandbyPromotionChecks = 1
	cfg.StandbyRotationCount = 1
	cfg.StandbyRotationInterval = 0
	cfg.EnableMetrics = true
	cfg.DiscoveryHealthCheckRetries = 3
	cfg.HealthCheckTimeout = 3 * time.Second // Longer than default test timeout
	cfg.HealthCheckMaxRetries = 3            // More retries for concurrent load
	return cfg
}

// warmupSelections returns the total number of tryWarmupSkip calls needed to
// complete warmup for a single connection with the given parameters.
//
// This mirrors the smoothstep decay in tryWarmupSkip: each round's skip count
// follows 1-smoothstep(t) where t = delta/maxRounds, giving an S-shaped
// acceptance ramp. The total selections = sum(skipCount_r + 1) for each round.
func warmupSelections(rounds, skip int) int {
	total := 0
	rdRounds, rdSkip := rounds, skip
	for rdRounds > 0 {
		total += rdSkip + 1 // skips + 1 accept
		rdRounds--
		if rdRounds <= 0 {
			break
		}
		// smoothstepSkip: (1 - smoothstep(delta/R)) * maxSkip
		delta := rounds - rdRounds
		r3 := rounds * rounds * rounds
		num := skip * (r3 - 3*delta*delta*rounds + 2*delta*delta*delta)
		rdSkip = max(num/r3, 0)
	}
	return total
}

// drainWarmup pumps enough requests through the transport to complete warmup
// on all connections, allowing deferred cap enforcement to fire.
func drainWarmup(transport *opensearchtransport.Client) {
	const (
		nodeCount          = 3
		warmupRounds       = 4
		warmupSkipMultiple = 2
	)
	warmupSkip := warmupRounds * warmupSkipMultiple
	selectionsPerConn := warmupSelections(warmupRounds, warmupSkip)
	drainRequests := selectionsPerConn * nodeCount * 3 / 2

	for range drainRequests {
		req, err := http.NewRequest(http.MethodGet, "/", nil)
		if err != nil {
			break
		}
		resp, err := transport.Perform(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
	}
}

// discoverWithStandby runs DiscoverNodes then pumps requests to complete
// warmup so that deferred cap enforcement can fire. Retries until the expected
// number of standby connections is reached or maxAttempts is exhausted.
// Returns the final metrics. Handles transient discovery failures (e.g., EOF)
// by retrying with a short delay.
func discoverWithStandby(t *testing.T, transport *opensearchtransport.Client) opensearchtransport.Metrics {
	t.Helper()

	var m opensearchtransport.Metrics
	var lastErr error

	for attempt := range 5 {
		err := transport.DiscoverNodes(t.Context())
		if err != nil {
			lastErr = err
			t.Logf("Discovery attempt %d failed: %v", attempt+1, err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		lastErr = nil

		// Pump requests to drain warmup. Cap enforcement is deferred until
		// connections finish warming (via Next()'s deferred path).
		drainWarmup(transport)

		m, err = transport.Metrics()
		require.NoError(t, err)

		t.Logf("Discovery attempt %d: active=%d, standby=%d, dead=%d",
			attempt+1, m.LiveConnections-m.StandbyConnections, m.StandbyConnections, m.DeadConnections)

		if m.StandbyConnections >= 2 {
			return m
		}
		time.Sleep(500 * time.Millisecond)
	}

	if lastErr != nil {
		require.NoError(t, lastErr, "all discovery attempts failed")
	}
	return m
}

// TestStandbyRotation verifies that standby rotation works end-to-end against
// a real 3-node OpenSearch cluster:
//
//  1. Configure ActiveListCap=1 so only 1 node is active, 2 are standby after discovery
//  2. Verify only the active node serves requests
//  3. Trigger DiscoverNodes (which performs rotation) and verify standby metrics change
//  4. Verify the rotated-in connection works (was health-checked before serving traffic)
func TestStandbyRotation(t *testing.T) {
	// Ensure cluster is reachable
	_, err := testutil.InitClient(t)
	require.NoError(t, err)

	// These tests require a multi-node cluster (3 nodes) for standby rotation.
	testutil.SkipIfSingleNode(t, 3)

	t.Run("Discovery with cap creates standby pool", func(t *testing.T) {
		cfg := standbyTestConfig(t)

		transport, err := opensearchtransport.New(cfg)
		require.NoError(t, err)

		// Run discovery to find all 3 nodes (seeds only have 2).
		// Discovery also enforces the active cap and runs rotation.
		m := discoverWithStandby(t, transport)

		// With 3 discovered nodes and cap=1, we expect 1 active + 2 standby.
		activeCount := m.LiveConnections - m.StandbyConnections
		assert.Equal(t, 1, activeCount, "expected 1 active connection (ActiveListCap=1)")
		assert.Equal(t, 2, m.StandbyConnections, "expected 2 standby connections")

		// Verify per-connection metrics show standby flags
		standbyCount := 0
		for _, conn := range m.Connections {
			cm, ok := conn.(opensearchtransport.ConnectionMetric)
			if !ok {
				continue
			}
			if cm.IsStandby {
				standbyCount++
			}
		}
		assert.Equal(t, 2, standbyCount, "expected 2 connections marked as standby in per-connection metrics")

		// Perform requests -- only the active connection should serve them
		nodesSeen := make(map[string]bool)
		for range 6 {
			req, err := http.NewRequest(http.MethodGet, "/", nil)
			require.NoError(t, err)

			res, err := transport.Perform(req)
			require.NoError(t, err)

			var info struct {
				Name string `json:"name"`
			}
			require.NoError(t, json.NewDecoder(res.Body).Decode(&info))
			res.Body.Close()

			nodesSeen[info.Name] = true
		}

		// With cap=1, all requests should hit the same single active node
		assert.Len(t, nodesSeen, 1, "expected all requests to hit the same active node, saw: %v", nodesSeen)
		t.Logf("Active node: %v", nodesSeen)
	})

	t.Run("Rotation swaps standby into active", func(t *testing.T) {
		obs := &rotationObserver{}

		cfg := standbyTestConfig(t)
		cfg.Observer = obs

		transport, err := opensearchtransport.New(cfg)
		require.NoError(t, err)

		// Initial discovery -- establishes 1 active + 2 standby.
		// Cap enforcement during this phase fires OnStandbyDemote for the 2
		// connections demoted to standby, but no OnStandbyPromote events yet.
		m0 := discoverWithStandby(t, transport)
		require.Equal(t, 2, m0.StandbyConnections, "need 2 standby to test rotation")

		// Record which node is currently active
		req, err := http.NewRequest(http.MethodGet, "/", nil)
		require.NoError(t, err)
		res, err := transport.Perform(req)
		require.NoError(t, err)
		var info0 struct {
			Name string `json:"name"`
		}
		require.NoError(t, json.NewDecoder(res.Body).Decode(&info0))
		res.Body.Close()

		initialActive := info0.Name
		t.Logf("Initial active node: %s", initialActive)

		// Snapshot observer state after initial setup so we detect new events
		// from rotation, not from the initial cap enforcement.
		basePromotions := obs.promotionCount()
		baseDemotions := obs.demotionCount()
		t.Logf("After initial discovery: promotions=%d, demotions=%d", basePromotions, baseDemotions)

		// Keep running discovery+warmup cycles until the observer confirms
		// rotation fired (OnStandbyPromote) and the matching cap enforcement
		// demotion completed (OnStandbyDemote).
		//
		// Each DiscoverNodes call triggers rotateStandby at its end. When the
		// pool was just rebuilt from new Connection objects (all lcActive+warming),
		// rotation finds no standbys and is a no-op. drainWarmup then completes
		// warmup and fires deferredCapEnforcement, re-establishing the standby
		// partition. On the *next* DiscoverNodes, connections are reused with
		// their lcStandby state intact, so rotation finds standbys and promotes one.
		//
		// We wait for both promotion AND demotion before breaking because
		// deferredCapEnforcement runs asynchronously (goroutine spawned by
		// Next() when warmup completes). By requiring the demotion count to
		// advance, we ensure cap enforcement has finished before we assert
		// pool state. If the goroutine hasn't run yet, the next iteration's
		// DiscoverNodes acquires pool locks, guaranteeing it completes.
		//
		// We retry because transient health check failures during discovery can
		// cause connection recreation (losing lifecycle state) or node deaths.
		for attempt := range 10 {
			err := transport.DiscoverNodes(t.Context())
			if err != nil {
				t.Logf("Rotation attempt %d: discovery failed: %v", attempt+1, err)
				continue
			}
			drainWarmup(transport)

			promotions := obs.promotionCount()
			demotions := obs.demotionCount()
			t.Logf("Rotation attempt %d: promotions=%d (+%d), demotions=%d (+%d)",
				attempt+1, promotions, promotions-basePromotions, demotions, demotions-baseDemotions)

			if promotions > basePromotions && demotions > baseDemotions {
				break
			}
		}

		// Rotation should have occurred: observer recorded new promotion + demotion events.
		require.Greater(t, obs.promotionCount(), basePromotions, "expected at least 1 new standby promotion")
		require.Greater(t, obs.demotionCount(), baseDemotions, "expected at least 1 new standby demotion")

		// Verify pool state from the demotion event snapshot. The observer captures
		// counts at the exact moment enforceActiveCapWithLock runs (pool lock held).
		// A transient request failure during drainWarmup can move a connection to
		// dead, reducing the standby count below 2. The invariant that matters is
		// that cap enforcement set activeCount to the cap (1).
		t.Logf("Demotion snapshot: active=%d, standby=%d, dead=%d",
			obs.lastDemotionActiveCount(), obs.lastDemotionStandbyCount(), obs.lastDemotionDeadCount())
		require.Equal(t, 1, obs.lastDemotionActiveCount(), "active count should equal cap after enforcement")

		// Verify the active connection works for real requests (was warmed up before serving).
		// Use GET / instead of /_cluster/health because the cluster health endpoint
		// can return 500 transiently when the cluster is under heavy discovery load
		// (as seen in CI after 7+ rapid discovery+warmup cycles).
		require.Eventually(t, func() bool {
			req, reqErr := http.NewRequest(http.MethodGet, "/", nil)
			if reqErr != nil {
				return false
			}
			res, perfErr := transport.Perform(req)
			if perfErr != nil {
				return false
			}
			ok := res.StatusCode == http.StatusOK
			res.Body.Close()
			return ok
		}, 5*time.Second, 100*time.Millisecond, "active node should serve healthy responses")

		// Once stabilized, confirm 5 consecutive requests succeed
		for range 5 {
			req, err := http.NewRequest(http.MethodGet, "/", nil)
			require.NoError(t, err)
			res, err := transport.Perform(req)
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, res.StatusCode, "active node should serve healthy responses")
			res.Body.Close()
		}
	})

	t.Run("Multiple rotations cycle through all nodes", func(t *testing.T) {
		obs := &rotationObserver{}

		cfg := standbyTestConfig(t)
		cfg.Observer = obs

		transport, err := opensearchtransport.New(cfg)
		require.NoError(t, err)

		// Initial discovery -- wait for 2 standby
		discoverWithStandby(t, transport)

		// Run rotation cycles. Each cycle:
		//  1. Re-establish the standby pool (rotation health checks on slow
		//     clusters like 2.1.0 can move standbys to dead, so we need to
		//     wait for resurrection + cap enforcement before the next rotation).
		//  2. Call DiscoverNodes which triggers rotateStandbyConnections at the end.
		//  3. Drain warmup so deferredCapEnforcement can fire.
		//  4. Check if the observer recorded a new promotion.
		for cycle := range 12 {
			prevPromotions := obs.promotionCount()

			// Ensure standby partition is populated before attempting rotation.
			// On slow clusters, prior rotation health checks may have moved
			// standbys to dead. discoverWithStandby waits for resurrection
			// and cap enforcement to re-establish 2 standbys.
			discoverWithStandby(t, transport)

			for attempt := range 10 {
				err := transport.DiscoverNodes(t.Context())
				if err != nil {
					t.Logf("Cycle %d attempt %d: discovery failed: %v", cycle+1, attempt+1, err)
					continue
				}
				drainWarmup(transport)

				promotions := obs.promotionCount()
				t.Logf("Cycle %d attempt %d: promotions=%d (prev=%d)",
					cycle+1, attempt+1, promotions, prevPromotions)

				if promotions > prevPromotions {
					break
				}
			}
		}

		promoted := obs.promotedNodes()
		t.Logf("Promoted nodes: %v (total promotions=%d, demotions=%d)",
			promoted, obs.promotionCount(), obs.demotionCount())

		// With 12 cycles and StandbyRotationCount=1, the observer should have
		// recorded many promotion events across multiple distinct nodes.
		assert.GreaterOrEqual(t, obs.promotionCount(), 3,
			"expected at least 3 standby promotions over 12 rotation cycles")
		assert.GreaterOrEqual(t, obs.demotionCount(), 3,
			"expected at least 3 standby demotions over 12 rotation cycles")
		// With 3 nodes and 12 rotations, at least 2 distinct nodes should have
		// been promoted from standby.
		assert.GreaterOrEqual(t, len(promoted), 2,
			"expected at least 2 distinct nodes promoted from standby, saw: %v", promoted)
	})
}
