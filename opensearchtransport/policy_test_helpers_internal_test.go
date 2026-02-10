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

func createTestConnection(urlStr string, roles ...string) *Connection {
	u, _ := url.Parse(urlStr)
	conn := &Connection{
		URL:   u,
		Roles: make(roleSet),
	}
	for _, role := range roles {
		conn.Roles[role] = struct{}{}
	}
	// Mark connection as dead initially - this is the state connections have
	// when added to the dead list via DiscoveryUpdate
	conn.mu.Lock()
	conn.markAsDeadWithLock()
	conn.mu.Unlock()
	return conn
}

func createTestConfig() policyConfig {
	return policyConfig{
		resurrectTimeoutInitial:      60 * time.Second,
		resurrectTimeoutFactorCutoff: 5,
	}
}
