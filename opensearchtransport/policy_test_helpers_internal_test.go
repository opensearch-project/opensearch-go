// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"net/url"
	"time"
)

// Test helper functions for policy tests

// createTestConnection creates a connection that simulates the state after
// the allConns pool's partition logic has processed it as "ready". This is
// the state connections have when router.DiscoveryUpdate is called -- lcActive
// is set so that isReady() returns true.
//
// For connections that should simulate a dead/unhealthy state, use
// createDeadTestConnection instead.
func createTestConnection(urlStr string, roles ...string) *Connection {
	u, _ := url.Parse(urlStr)
	conn := &Connection{
		URL:   u,
		Roles: make(roleSet),
	}
	for _, role := range roles {
		conn.Roles[role] = struct{}{}
	}
	// Set lcActive to match the state after allConns pool partition logic
	// (discovery.go lines 911-922). Policy DiscoveryUpdate checks isReady()
	// which requires lcActive or lcStandby.
	conn.state.Store(int64(newConnState(lcActive | lcNeedsWarmup)))
	return conn
}

// createDeadTestConnection creates a connection in the dead state (lcDead
// with deadSince set). This simulates a connection that the allConns pool
// placed on the dead list -- the state when router.DiscoveryUpdate receives
// it in the "added" list but classified as unhealthy.
func createDeadTestConnection(urlStr string, roles ...string) *Connection {
	u, _ := url.Parse(urlStr)
	conn := &Connection{
		URL:   u,
		Roles: make(roleSet),
	}
	for _, role := range roles {
		conn.Roles[role] = struct{}{}
	}
	conn.state.Store(int64(newConnState(lcDead | lcNeedsWarmup)))
	conn.mu.Lock()
	conn.markAsDeadWithLock()
	conn.mu.Unlock()
	return conn
}

func createTestConfig() policyConfig {
	return policyConfig{
		resurrectTimeoutInitial:      60 * time.Second,
		resurrectTimeoutFactorCutoff: 5,
		minimumResurrectTimeout:      defaultMinimumResurrectTimeout,
		jitterScale:                  defaultJitterScale,
	}
}
