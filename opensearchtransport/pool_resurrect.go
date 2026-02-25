// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package opensearchtransport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"slices"
	"time"
)

// healthCheck performs a health check on this connection with concurrency protection.
// Updates deadSince and checkStartedAt state based on health check results.
// Returns error if health check fails or if already checking.
func (c *Connection) healthCheck(ctx context.Context, healthCheck HealthCheckFunc) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Skip if already checking to prevent concurrent health checks
	if !c.mu.checkStartedAt.IsZero() {
		duration := time.Since(c.mu.checkStartedAt)
		return fmt.Errorf("health check already in progress for %v", duration)
	}

	// Store original deadSince to detect race conditions
	originalDeadSince := c.mu.deadSince

	// Set checking timestamp
	c.mu.checkStartedAt = time.Now()
	c.setLifecycleBit(lcHealthChecking)
	defer func() {
		c.mu.checkStartedAt = time.Time{}
		c.clearLifecycleBit(lcHealthChecking)
	}()

	// Perform actual health check
	c.mu.Unlock() // Release lock during network call
	resp, err := healthCheck(ctx, c, c.URL)
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}
	c.mu.Lock() // Reacquire for state update

	// Check if connection was marked dead more recently than when we started
	if c.mu.deadSince.After(originalDeadSince) {
		// Connection was marked dead while we were checking, discard result
		return nil
	}

	// Update connection state based on health check result
	if err != nil {
		// Health check failed
		if c.mu.deadSince.IsZero() {
			c.mu.deadSince = time.Now()
		}
		return err
	}

	// Health check passed
	if !c.mu.deadSince.IsZero() {
		c.mu.deadSince = time.Time{} // Reset deadSince
	}

	return nil
}

// checkDead syncs dead/ready lists based on Connection.mu.isDead state and performs health checks.
func (cp *multiServerPool) checkDead(ctx context.Context, healthCheck HealthCheckFunc) error {
	if healthCheck == nil {
		return errors.New("healthCheck function cannot be nil")
	}

	// Get snapshot of dead connections without holding lock during health checks
	cp.mu.RLock()
	deadConns := make([]*Connection, len(cp.mu.dead))
	copy(deadConns, cp.mu.dead)
	cp.mu.RUnlock()

	// Perform health checks without holding the pool lock
	for _, conn := range deadConns {
		err := conn.healthCheck(ctx, healthCheck)
		if err != nil {
			// Health check failed or already in progress, skip
			continue
		}

		// Health check passed -- decrement quiescing counter. If still quiescing,
		// skip resurrection this cycle; the next health check will decrement again.
		if remaining := conn.decrementDrainingQuiescing(); remaining > 0 {
			continue
		}

		// Check if connection is now alive and resurrect if needed
		conn.mu.RLock()
		isDead := !conn.mu.deadSince.IsZero()
		conn.mu.RUnlock()

		if !isDead {
			// Connection is alive, resurrect it
			cp.mu.Lock()
			conn.mu.Lock()
			// Double-check state after acquiring locks to avoid race
			if conn.mu.deadSince.IsZero() {
				cp.resurrectWithLock(conn)
			}
			conn.mu.Unlock()
			cp.mu.Unlock()
		}
	}

	return nil
}

// performHealthCheck executes the health check for a connection.
// Returns true if health check passes, false if it fails.
// Note: This method does not reschedule on failure. The caller (resurrectWithLock) is responsible
// for ensuring checkStartedAt is reset (via defer), allowing future failures to trigger new checks.
func (cp *multiServerPool) performHealthCheck(ctx context.Context, c *Connection) bool {
	resp, err := cp.healthCheck(ctx, c, c.URL)
	if err != nil {
		if debugLogger != nil {
			debugLogger.Logf("[%s] Health check failed for %q: %s\n", cp.name, c.URL, err)
		}
		if obs := observerFromAtomic(&cp.observer); obs != nil {
			event := newConnectionEvent(cp.name, c, 0, 0)
			event.Error = err
			obs.OnHealthCheckFail(event)
		}
		return false
	}

	// Notify observer of health check success (before version update so snapshot is consistent)
	if obs := observerFromAtomic(&cp.observer); obs != nil {
		obs.OnHealthCheckPass(newConnectionEvent(cp.name, c, 0, 0))
	}

	// Health check passed -- decrement draining quiesce counter.
	// This is the primary path that decrements draining; OnSuccess deliberately skips
	// resurrection while draining is set, ensuring only verified health checks bring the node back.
	c.decrementDrainingQuiescing()

	// Try to extract version information from the response
	if resp == nil || resp.Body == nil {
		return true
	}

	// Read the response body to extract version information
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()

	if err != nil {
		return true
	}

	var info healthCheckInfo
	if json.Unmarshal(body, &info) != nil || info.Version.Number == "" {
		return true
	}

	// Log version changes during rolling upgrades (not on initial startup)
	if debugLogger != nil {
		prev := c.loadVersion()
		if prev != "" && prev != info.Version.Number {
			debugLogger.Logf("[%s] Version changed for %q: %s -> %s\n", cp.name, c.URL, prev, info.Version.Number)
		}
	}
	// Update the connection version
	c.storeVersion(info.Version.Number)

	return true
}

// calculateResurrectTimeout calculates the resurrection timeout based on failure count and cluster health.
//
// Three inputs compete via max():
//
//  1. Health-ratio timeout: exponential backoff scaled by cluster health.
//     Healthy clusters wait longer (no rush), degraded clusters retry sooner.
//     baseTimeout = resurrectTimeoutInitial * 2^min(failures-1, factorCutoff)
//     healthTimeout = baseTimeout * (liveNodes / totalNodes)
//
//  2. Rate-limited timeout: throttles health checks based on the estimated TLS handshake
//     pressure on recovering servers. New TLS connections are expensive (async crypto),
//     so as more servers come back online and clients reconnect, each server faces
//     increasing handshake load from all clients.
//     rateLimitedTimeout = (liveNodes * clientsPerServer) / serverMaxNewConnsPerSec
//     All dead (0 ready) -> 0 -> minimum floor (aggressive, we need capacity)
//     Recovering (some ready) -> increases (back off, servers busy with TLS ramp-up)
//     Mostly healthy -> capped at resurrectTimeoutMax (very conservative)
//
//  3. Minimum floor: minimumResurrectTimeout (absolute lower bound).
//
// The final timeout is max(healthTimeout, rateLimitedTimeout, minimum) + jitter.
func (cp *multiServerPool) calculateResurrectTimeout(c *Connection) time.Duration {
	// Calculate basic exponential backoff factor
	failures := c.failures.Load()
	factor := math.Min(float64(failures-1), float64(cp.resurrectTimeoutFactorCutoff))
	baseTimeout := time.Duration(cp.resurrectTimeoutInitial.Seconds() * math.Exp2(factor) * float64(time.Second))

	// Cap base timeout before applying cluster health adjustments
	if cp.resurrectTimeoutMax > 0 && baseTimeout > cp.resurrectTimeoutMax {
		baseTimeout = cp.resurrectTimeoutMax
	}

	// Get cluster health metrics
	cp.mu.RLock()
	totalNodes := len(cp.mu.ready) + len(cp.mu.dead)
	liveNodes := cp.mu.activeCount
	cp.mu.RUnlock()

	// Health-ratio timeout: scales backoff by cluster health
	healthRatio := float64(liveNodes) / float64(max(totalNodes, 1))
	healthTimeout := time.Duration(float64(baseTimeout) * healthRatio)

	// Rate-limited timeout: throttles based on TLS handshake pressure on recovering servers.
	// More ready servers = more clients actively reconnecting = more TLS load to absorb.
	// Uses liveNodes (not deadNodes) because the bottleneck is the recovering servers'
	// ability to handle new TLS sessions from all clients simultaneously.
	var rateLimitedTimeout time.Duration
	if liveNodes > 0 && cp.serverMaxNewConnsPerSec > 0 {
		rateLimitedTimeout = time.Duration(float64(time.Second) * float64(liveNodes) * cp.clientsPerServer / cp.serverMaxNewConnsPerSec)
		if cp.resurrectTimeoutMax > 0 && rateLimitedTimeout > cp.resurrectTimeoutMax {
			rateLimitedTimeout = cp.resurrectTimeoutMax
		}
	}

	// Final timeout: max of all three inputs
	finalTimeout := max(healthTimeout, rateLimitedTimeout, cp.minimumResurrectTimeout)

	// Add jitter to stagger retries across goroutines
	// #nosec G404 - Non-cryptographic randomness is acceptable for connection timing jitter
	jitter := time.Duration(rand.Float64() * cp.jitterScale * float64(finalTimeout))
	finalTimeout += jitter

	return finalTimeout
}

// scheduleResurrect schedules the connection to be resurrected using cluster-aware timing.
func (cp *multiServerPool) scheduleResurrect(ctx context.Context, c *Connection) {
	// Check if a health check is already scheduled for this connection (read lock first)
	c.mu.RLock()
	if !c.mu.checkStartedAt.IsZero() {
		// Health check already in progress
		c.mu.RUnlock()
		return
	}
	c.mu.RUnlock()

	// Upgrade to write lock and re-check
	c.mu.Lock()
	if !c.mu.checkStartedAt.IsZero() {
		// Another goroutine started a health check between our read and write lock
		c.mu.Unlock()
		return
	}
	// Mark that we're starting a health check
	c.mu.checkStartedAt = time.Now().UTC()
	c.setLifecycleBit(lcHealthChecking)
	c.mu.Unlock()

	// Spawn goroutine to handle resurrection attempts with retries
	go func() {
		// Reset checkStartedAt when done, regardless of outcome
		defer func() {
			c.mu.Lock()
			c.mu.checkStartedAt = time.Time{}
			c.clearLifecycleBit(lcHealthChecking)
			c.mu.Unlock()
		}()

		// Retry loop for health checks
		for {
			// Calculate timeout for this attempt
			timeout := cp.calculateResurrectTimeout(c)

			// Wait for either timeout or context cancellation
			select {
			case <-ctx.Done():
				if debugLogger != nil {
					debugLogger.Logf("[%s] Health check cancelled for %q: %v\n", cp.name, c.URL, ctx.Err())
				}
				return
			case <-time.After(timeout):
				// Timeout elapsed, proceed with resurrection attempt
			}

			// Attempt resurrection in a closure so defer executes at iteration end
			shouldReturn := func() bool {
				cp.mu.Lock()
				c.mu.Lock()
				defer func() {
					c.mu.Unlock()
					cp.mu.Unlock()
				}()

				// Check if connection was removed by DiscoveryUpdate
				// Connection should be in either ready or dead list; if in neither, it was removed
				if c.mu.deadSince.IsZero() {
					return true
				}

				// Check if connection is still in the pool (ready or dead lists)
				stillInPool := slices.Contains(cp.mu.ready, c) || slices.Contains(cp.mu.dead, c)
				if !stillInPool {
					if debugLogger != nil {
						debugLogger.Logf("[%s] Connection %q removed from pool by DiscoveryUpdate, stopping health checks\n", cp.name, c.URL)
					}
					return true
				}

				// If the connection is overload-demoted, the stats poller manages its lifecycle.
				// Stop the resurrection scheduler -- the stats poller will promote it back to ready
				// when metrics improve, or clear lcOverloaded if it can't reach the node.
				if c.loadConnState().lifecycle().has(lcOverloaded) {
					if debugLogger != nil {
						debugLogger.Logf("[%s] Connection %q is overload-demoted, stopping resurrection (stats poller manages lifecycle)\n", cp.name, c.URL)
					}
					return true
				}

				// Execute health check if configured before resurrecting
				if cp.healthCheck != nil {
					if shouldRetry := cp.attemptHealthCheckWithRelock(ctx, c, &stillInPool); shouldRetry != nil {
						return *shouldRetry
					}
				}

				// Health check passed (or no health check configured), resurrect the connection
				cp.resurrectWithLock(c)

				if obs := observerFromAtomic(&cp.observer); obs != nil {
					obs.OnPromote(newConnectionEvent(cp.name, c, cp.mu.activeCount, len(cp.mu.dead)))
				}

				return true // Successfully resurrected, exit loop
			}()

			if shouldReturn {
				return
			}
		}
	}()
}

// attemptHealthCheckWithRelock performs a health check with lock management.
// Returns nil if health check passed and caller should proceed with resurrection.
// Returns pointer to bool if caller should return: true to exit, false to retry.
func (cp *multiServerPool) attemptHealthCheckWithRelock(ctx context.Context, c *Connection, stillInPool *bool) *bool {
	// Release locks to perform I/O
	c.mu.Unlock()
	cp.mu.Unlock()

	healthCheckPassed := cp.performHealthCheck(ctx, c)

	// Re-acquire locks after health check
	cp.mu.Lock()
	c.mu.Lock()

	if !healthCheckPassed {
		// Health check failed, increment failures and retry with new timeout
		c.failures.Add(1)
		// Return false to continue loop
		shouldRetry := false
		return &shouldRetry
	}

	// Re-check if connection was resurrected while we were checking
	if c.mu.deadSince.IsZero() {
		shouldReturn := true
		return &shouldReturn
	}

	// Re-check if connection is still in pool after health check (ready or dead)
	*stillInPool = slices.Contains(cp.mu.ready, c) || slices.Contains(cp.mu.dead, c)
	if !*stillInPool {
		shouldReturn := true
		return &shouldReturn
	}

	// If connection is still quiescing (draining countdown > 0), continue the health check
	// loop without incrementing failures. performHealthCheck already decremented the counter,
	// so we just need to wait for the next resurrection interval (with jitter) and re-check.
	if c.drainingQuiescingRemaining.Load() > 0 {
		shouldRetry := false
		return &shouldRetry
	}

	// Health check passed and quiescing complete, proceed with resurrection
	return nil
}
