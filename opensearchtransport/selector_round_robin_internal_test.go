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
	"net/url"
	"sync"
	"testing"
)

// Compile-time interface compliance checks
var (
	_ Selector = (*roundRobinSelector)(nil)
)

func TestNewRoundRobinSelector(t *testing.T) {
	selector := NewRoundRobinSelector()

	if selector == nil {
		t.Errorf("Expected NewRoundRobinSelector() to return a non-nil selector")
		return
	}

	// Check initial state - starts at -1 so first increment gives 0
	if selector.curr.Load() != -1 {
		t.Errorf("Expected initial current index to be -1, got %d", selector.curr.Load())
	}
}

func TestRoundRobinSelectorNoConnections(t *testing.T) {
	selector := NewRoundRobinSelector()

	_, err := selector.Select([]*Connection{})
	if err == nil {
		t.Errorf("Expected error when selecting from empty connection list")
	}

	expectedMsg := "no connections available"
	if err.Error() != expectedMsg {
		t.Errorf("Expected error message %q, got %q", expectedMsg, err.Error())
	}
}

func TestRoundRobinSelectorSingleConnection(t *testing.T) {
	selector := NewRoundRobinSelector()

	conn1 := &Connection{URL: &url.URL{Host: "localhost:9200"}}
	connections := []*Connection{conn1}

	// Multiple selections should always return the same connection
	for range 5 {
		selected, err := selector.Select(connections)
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}

		if selected != conn1 {
			t.Errorf("Expected connection %v, got %v", conn1, selected)
		}
	}
}

func TestRoundRobinSelectorMultipleConnections(t *testing.T) {
	selector := NewRoundRobinSelector()

	conn1 := &Connection{URL: &url.URL{Host: "localhost:9200"}}
	conn2 := &Connection{URL: &url.URL{Host: "localhost:9201"}}
	conn3 := &Connection{URL: &url.URL{Host: "localhost:9202"}}
	connections := []*Connection{conn1, conn2, conn3}

	expected := []*Connection{conn1, conn2, conn3, conn1, conn2, conn3}

	// Test round-robin behavior
	for i, expectedConn := range expected {
		selected, err := selector.Select(connections)
		if err != nil {
			t.Errorf("Selection %d: expected no error, got: %v", i, err)
		}

		if selected != expectedConn {
			t.Errorf("Selection %d: expected connection %v, got %v", i, expectedConn, selected)
		}
	}
}

func TestRoundRobinSelectorConcurrency(t *testing.T) {
	selector := NewRoundRobinSelector()

	conn1 := &Connection{URL: &url.URL{Host: "localhost:9200"}}
	conn2 := &Connection{URL: &url.URL{Host: "localhost:9201"}}
	conn3 := &Connection{URL: &url.URL{Host: "localhost:9202"}}
	connections := []*Connection{conn1, conn2, conn3}

	const numGoroutines = 100
	const selectionsPerGoroutine = 10

	var wg sync.WaitGroup
	results := make(chan *Connection, numGoroutines*selectionsPerGoroutine)

	// Start multiple goroutines selecting connections
	for range numGoroutines {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range selectionsPerGoroutine {
				selected, err := selector.Select(connections)
				if err != nil {
					t.Errorf("Concurrent selection failed: %v", err)
					return
				}
				results <- selected
			}
		}()
	}

	wg.Wait()
	close(results)

	// Count selections per connection
	counts := make(map[*Connection]int)
	totalSelections := 0

	for conn := range results {
		counts[conn]++
		totalSelections++
	}

	// Verify all connections were selected
	for _, conn := range connections {
		if counts[conn] == 0 {
			t.Errorf("Connection %v was never selected", conn)
		}
	}

	// Verify total selections
	expectedTotal := numGoroutines * selectionsPerGoroutine
	if totalSelections != expectedTotal {
		t.Errorf("Expected %d total selections, got %d", expectedTotal, totalSelections)
	}

	// In a perfectly round-robin scenario, each connection should get roughly equal selections
	// But due to concurrency, we just verify distribution is reasonable (not perfect)
	expectedPerConnection := expectedTotal / len(connections)
	tolerance := expectedPerConnection / 2 // Allow 50% tolerance

	for conn, count := range counts {
		if count < expectedPerConnection-tolerance || count > expectedPerConnection+tolerance {
			t.Logf("Warning: Connection %v got %d selections (expected ~%d ± %d)",
				conn, count, expectedPerConnection, tolerance)
			// Note: This is logged as warning, not error, because perfect distribution
			// isn't guaranteed with concurrent access
		}
	}
}

func TestRoundRobinSelectorAtomicIncrement(t *testing.T) {
	selector := NewRoundRobinSelector()

	conn1 := &Connection{URL: &url.URL{Host: "localhost:9200"}}
	conn2 := &Connection{URL: &url.URL{Host: "localhost:9201"}}
	connections := []*Connection{conn1, conn2}

	// Test wraparound behavior with atomic increment
	// Initial curr is -1, so first increment gives 0, second gives 1, third gives 2 % 2 = 0

	selected1, _ := selector.Select(connections)
	if selected1 != conn1 {
		t.Errorf("First selection: expected %v, got %v", conn1, selected1)
	}

	selected2, _ := selector.Select(connections)
	if selected2 != conn2 {
		t.Errorf("Second selection: expected %v, got %v", conn2, selected2)
	}

	selected3, _ := selector.Select(connections)
	if selected3 != conn1 {
		t.Errorf("Third selection (wraparound): expected %v, got %v", conn1, selected3)
	}
}
