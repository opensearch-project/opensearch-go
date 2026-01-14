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
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultResurrectTimeoutInitial      = 60 * time.Second
	defaultResurrectTimeoutFactorCutoff = 5
)

// Selector defines the interface for selecting connections from the pool.
type Selector interface {
	Select([]*Connection) (*Connection, error)
}

// ConnectionPool defines the interface for the connection pool.
type ConnectionPool interface {
	Next() (*Connection, error)  // Next returns the next available connection.
	OnSuccess(*Connection)       // OnSuccess reports that the connection was successful.
	OnFailure(*Connection) error // OnFailure reports that the connection failed.
	URLs() []*url.URL            // URLs returns the list of URLs of available connections.
}

// Connection represents a connection to a node.
type Connection struct {
	URL        *url.URL
	ID         string
	Name       string
	Roles      []string
	Attributes map[string]interface{}

	failures atomic.Int64

	mu struct {
		sync.RWMutex
		isDead    bool
		deadSince time.Time
	}
}

type singleConnectionPool struct {
	connection *Connection

	metrics *metrics
}

type statusConnectionPool struct {
	mu struct {
		sync.RWMutex
		live []*Connection // List of live connections
		dead []*Connection // List of dead connections
	}

	selector                     Selector
	resurrectTimeoutInitial      time.Duration
	resurrectTimeoutFactorCutoff int

	metrics *metrics
}

type roundRobinSelector struct {
	curr atomic.Int64 // Index of the current connection
}

// Compile-time checks to ensure interface compliance
var (
	_ ConnectionPool = (*statusConnectionPool)(nil)
	_ ConnectionPool = (*singleConnectionPool)(nil)
)

// NewConnectionPool creates and returns a default connection pool.
func NewConnectionPool(conns []*Connection, selector Selector) ConnectionPool {
	if len(conns) == 1 {
		return &singleConnectionPool{connection: conns[0]}
	}

	if selector == nil {
		s := &roundRobinSelector{}
		s.curr.Store(-1)
		selector = s
	}

	pool := &statusConnectionPool{
		selector:                     selector,
		resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
		resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
	}
	pool.mu.live = conns
	return pool
}

// Next returns the connection from pool.
func (cp *singleConnectionPool) Next() (*Connection, error) {
	return cp.connection, nil
}

// OnSuccess is a no-op for single connection pool.
func (cp *singleConnectionPool) OnSuccess(*Connection) {}

// OnFailure is a no-op for single connection pool.
func (cp *singleConnectionPool) OnFailure(*Connection) error { return nil }

// URLs returns the list of URLs of available connections.
func (cp *singleConnectionPool) URLs() []*url.URL { return []*url.URL{cp.connection.URL} }

func (cp *singleConnectionPool) connections() []*Connection { return []*Connection{cp.connection} }

// Next returns a connection from pool, or an error.
func (cp *statusConnectionPool) Next() (*Connection, error) {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	// Return next live connection
	if len(cp.mu.live) > 0 {
		return cp.selector.Select(cp.mu.live)
	} else if len(cp.mu.dead) > 0 {
		// No live connection is available, resurrect one of the dead ones.
		c := cp.mu.dead[len(cp.mu.dead)-1]
		cp.mu.dead = cp.mu.dead[:len(cp.mu.dead)-1]
		c.mu.Lock()
		defer c.mu.Unlock()
		cp.resurrectWithLock(c, false)
		return c, nil
	}

	return nil, errors.New("no connection available")
}

// OnSuccess marks the connection as successful.
func (cp *statusConnectionPool) OnSuccess(c *Connection) {
	// Establish consistent lock ordering: Pool → Connection
	cp.mu.Lock()
	defer cp.mu.Unlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Short-circuit for live connection
	if !c.mu.isDead {
		return
	}

	c.markAsHealthyWithLock()
	cp.resurrectWithLock(c, true)
}

// OnFailure marks the connection as failed.
func (cp *statusConnectionPool) OnFailure(c *Connection) error {
	cp.mu.Lock()
	defer cp.mu.Unlock()

	c.mu.Lock()

	if c.mu.isDead {
		if debugLogger != nil {
			debugLogger.Logf("Already removed %s\n", c.URL)
		}
		c.mu.Unlock()

		return nil
	}

	if debugLogger != nil {
		debugLogger.Logf("Removing %s...\n", c.URL)
	}

	c.markAsDeadWithLock()
	cp.scheduleResurrect(c)
	c.mu.Unlock()

	// Push item to dead list and sort slice by number of failures
	cp.mu.dead = append(cp.mu.dead, c)
	sort.Slice(cp.mu.dead, func(i, j int) bool {
		c1 := cp.mu.dead[i]
		c2 := cp.mu.dead[j]

		// Use atomic loads for failure counts - no locking needed
		failures1 := c1.failures.Load()
		failures2 := c2.failures.Load()

		return failures1 > failures2
	})

	// Check if connection exists in the list, return error if not.
	index := -1

	for i, conn := range cp.mu.live {
		if conn == c {
			index = i
		}
	}

	if index < 0 {
		// Does this error even get raised? Under what conditions can the connection not be in the cp.mu.live list?
		// If the connection is marked dead the function already ended
		return errors.New("connection not in live list")
	}

	// Remove item; https://github.com/golang/go/wiki/SliceTricks
	copy(cp.mu.live[index:], cp.mu.live[index+1:])
	cp.mu.live = cp.mu.live[:len(cp.mu.live)-1]

	return nil
}

// URLs returns the list of URLs of available connections.
func (cp *statusConnectionPool) URLs() []*url.URL {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	urls := make([]*url.URL, len(cp.mu.live))
	for idx, c := range cp.mu.live {
		urls[idx] = c.URL
	}

	return urls
}

func (cp *statusConnectionPool) connections() []*Connection {
	cp.mu.RLock()
	defer cp.mu.RUnlock()

	conns := make([]*Connection, 0, len(cp.mu.live)+len(cp.mu.dead))
	conns = append(conns, cp.mu.live...)
	conns = append(conns, cp.mu.dead...)

	return conns
}

// resurrect adds the connection to the list of available connections.
// When removeDead is true, it also removes it from the dead list.
//
// CALLER RESPONSIBILITIES:
//   - Caller should verify external connectivity/health before resurrection
//     (this method only updates internal bookkeeping, not connection health)
//   - Caller must handle any errors from subsequent connection attempts
func (cp *statusConnectionPool) resurrectWithLock(c *Connection, removeDead bool) {
	if debugLogger != nil {
		debugLogger.Logf("Resurrecting %s\n", c.URL)
	}

	c.markAsLiveWithLock()
	cp.mu.live = append(cp.mu.live, c)

	if removeDead {
		index := -1

		for i, conn := range cp.mu.dead {
			if conn == c {
				index = i
			}
		}

		if index >= 0 {
			// Remove item; https://github.com/golang/go/wiki/SliceTricks
			copy(cp.mu.dead[index:], cp.mu.dead[index+1:])
			cp.mu.dead = cp.mu.dead[:len(cp.mu.dead)-1]
		}
	}
}

// scheduleResurrect schedules the connection to be resurrected.
func (cp *statusConnectionPool) scheduleResurrect(c *Connection) {
	failures := c.failures.Load()
	factor := min(failures-1, int64(cp.resurrectTimeoutFactorCutoff))
	timeout := time.Duration(cp.resurrectTimeoutInitial.Seconds() * math.Exp2(float64(factor)) * float64(time.Second))

	if debugLogger != nil {
		c.mu.RLock()
		deadSince := c.mu.deadSince
		c.mu.RUnlock()

		debugLogger.Logf(
			"Resurrect %s (failures=%d, factor=%d, timeout=%s) in %s\n",
			c.URL,
			failures,
			factor,
			timeout,
			deadSince.Add(timeout).Sub(time.Now().UTC()).Truncate(time.Second),
		)
	}

	time.AfterFunc(timeout, func() {
		cp.mu.Lock()
		defer cp.mu.Unlock()

		c.mu.Lock()
		defer c.mu.Unlock()

		if !c.mu.isDead {
			if debugLogger != nil {
				debugLogger.Logf("Already resurrected %s\n", c.URL)
			}
			return
		}

		cp.resurrectWithLock(c, true)
	})
}

// Select returns the connection in a round-robin fashion.
func (s *roundRobinSelector) Select(conns []*Connection) (*Connection, error) {
	if len(conns) == 0 {
		return nil, errors.New("no connections available")
	}

	// Atomic increment with wrap-around
	next := s.curr.Add(1)
	index := int(next % int64(len(conns)))
	return conns[index], nil
}

// markAsDead marks the connection as dead.
func (c *Connection) markAsDead() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.markAsDeadWithLock()
}

// markAsDeadWithLock marks the connection as dead (caller must hold lock).
func (c *Connection) markAsDeadWithLock() {
	c.mu.isDead = true
	if c.mu.deadSince.IsZero() {
		c.mu.deadSince = time.Now().UTC()
	}
	c.failures.Add(1)
}

// markAsLiveWithLock marks the connection as alive (caller must hold lock).
func (c *Connection) markAsLiveWithLock() {
	c.mu.isDead = false
}

// markAsHealthyWithLock marks the connection as healthy (caller must hold lock).
func (c *Connection) markAsHealthyWithLock() {
	c.mu.isDead = false
	c.mu.deadSince = time.Time{}
	c.failures.Store(0)
}

// String returns a readable connection representation.
func (c *Connection) String() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return fmt.Sprintf("<%s> dead=%v failures=%d", c.URL, c.mu.isDead, c.failures.Load())
}
