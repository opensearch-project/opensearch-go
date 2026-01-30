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
	"net/http"
	"net/url"
	"testing"
)

// Compile-time interface compliance checks
var (
	_ Selector             = (*ChainSelector)(nil)
	_ RequestAwareSelector = (*ChainSelector)(nil)
)

func TestNewSelectorDefault(t *testing.T) {
	selector := NewSelector()

	// Should return a round-robin selector when no options provided
	_, ok := selector.(*roundRobinSelector)
	if !ok {
		t.Errorf("Expected NewSelector() to return roundRobinSelector, got %T", selector)
	}
}

func TestNewSelectorSingle(t *testing.T) {
	rrSelector := NewRoundRobinSelector()
	selector := NewSelector(WithSelector(rrSelector))

	// Should return the single selector directly, not wrapped in a chain
	if selector != rrSelector {
		t.Errorf("Expected NewSelector() with single selector to return that selector directly")
	}
}

func TestNewSelectorMultiple(t *testing.T) {
	selector1 := NewRoundRobinSelector()
	selector2 := NewRoundRobinSelector()
	selector := NewSelector(
		WithSelector(selector1),
		WithSelector(selector2),
	)

	// Should return a ChainSelector when multiple selectors provided
	chainSelector, ok := selector.(*ChainSelector)
	if !ok {
		t.Errorf("Expected NewSelector() with multiple selectors to return ChainSelector, got %T", selector)
	}

	if len(chainSelector.selectors) != 2 {
		t.Errorf("Expected ChainSelector to have 2 selectors, got %d", len(chainSelector.selectors))
	}
}

func TestChainSelectorSelect(t *testing.T) {
	// Create mock selectors
	failingSelector := &chainMockSelector{shouldFail: true}
	workingSelector := &chainMockSelector{shouldFail: false}

	chain := NewSelector(
		WithSelector(failingSelector),
		WithSelector(workingSelector),
	).(*ChainSelector)

	connections := []*Connection{
		{URL: &url.URL{Host: "localhost:9200"}},
	}

	conn, err := chain.Select(connections)
	if err != nil {
		t.Errorf("Expected ChainSelector.Select() to succeed, got error: %v", err)
	}

	if conn != connections[0] {
		t.Errorf("Expected connection from working selector")
	}

	// Verify the first selector was called
	if !failingSelector.called {
		t.Errorf("Expected first selector to be called")
	}

	// Verify the second selector was called after first failed
	if !workingSelector.called {
		t.Errorf("Expected second selector to be called after first failed")
	}
}

func TestChainSelectorSelectAllFail(t *testing.T) {
	failingSelector1 := &chainMockSelector{shouldFail: true}
	failingSelector2 := &chainMockSelector{shouldFail: true}

	chain := NewSelector(
		WithSelector(failingSelector1),
		WithSelector(failingSelector2),
	).(*ChainSelector)

	connections := []*Connection{
		{URL: &url.URL{Host: "localhost:9200"}},
	}

	_, err := chain.Select(connections)
	if err == nil {
		t.Errorf("Expected ChainSelector.Select() to fail when all selectors fail")
	}

	expectedMsg := "all selectors in chain failed to return a connection"
	if err.Error() != expectedMsg {
		t.Errorf("Expected error message %q, got %q", expectedMsg, err.Error())
	}
}

func TestChainSelectorSelectForRequest(t *testing.T) {
	// Create mock selectors
	failingSelector := &chainMockRequestAwareSelector{shouldFail: true}
	workingSelector := &chainMockRequestAwareSelector{shouldFail: false}

	chain := NewSelector(
		WithSelector(failingSelector),
		WithSelector(workingSelector),
	).(*ChainSelector)

	connections := []*Connection{
		{URL: &url.URL{Host: "localhost:9200"}},
	}

	req := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/"}}

	conn, err := chain.SelectForRequest(connections, req)
	if err != nil {
		t.Errorf("Expected ChainSelector.SelectForRequest() to succeed, got error: %v", err)
	}

	if conn != connections[0] {
		t.Errorf("Expected connection from working selector")
	}

	// Verify the request-aware method was called
	if !workingSelector.selectForRequestCalled {
		t.Errorf("Expected SelectForRequest to be called on request-aware selector")
	}
}

func TestChainSelectorSelectForRequestMixed(t *testing.T) {
	// Test with mix of RequestAwareSelector and basic Selector
	failingSelector := &chainMockSelector{shouldFail: true}              // basic Selector
	workingSelector := &chainMockRequestAwareSelector{shouldFail: false} // RequestAwareSelector

	chain := NewSelector(
		WithSelector(failingSelector),
		WithSelector(workingSelector),
	).(*ChainSelector)

	connections := []*Connection{
		{URL: &url.URL{Host: "localhost:9200"}},
	}

	req := &http.Request{Method: http.MethodGet, URL: &url.URL{Path: "/"}}

	conn, err := chain.SelectForRequest(connections, req)
	if err != nil {
		t.Errorf("Expected ChainSelector.SelectForRequest() to succeed, got error: %v", err)
	}

	if conn != connections[0] {
		t.Errorf("Expected connection from working selector")
	}

	// Verify basic selector used Select() method
	if !failingSelector.called {
		t.Errorf("Expected basic selector's Select method to be called")
	}

	// Verify request-aware selector used SelectForRequest() method
	if !workingSelector.selectForRequestCalled {
		t.Errorf("Expected request-aware selector's SelectForRequest method to be called")
	}
}

// Mock selectors for testing

type chainMockSelector struct {
	shouldFail bool
	called     bool
}

func (m *chainMockSelector) Select(connections []*Connection) (*Connection, error) {
	m.called = true
	if m.shouldFail {
		return nil, errors.New("mock selector failure")
	}
	if len(connections) == 0 {
		return nil, errors.New("no connections")
	}
	return connections[0], nil
}

type chainMockRequestAwareSelector struct {
	shouldFail             bool
	called                 bool
	selectForRequestCalled bool
}

func (m *chainMockRequestAwareSelector) Select(connections []*Connection) (*Connection, error) {
	m.called = true
	if m.shouldFail {
		return nil, errors.New("mock selector failure")
	}
	if len(connections) == 0 {
		return nil, errors.New("no connections")
	}
	return connections[0], nil
}

func (m *chainMockRequestAwareSelector) SelectForRequest(connections []*Connection, _ *http.Request) (*Connection, error) {
	m.selectForRequestCalled = true
	if m.shouldFail {
		return nil, errors.New("mock request-aware selector failure")
	}
	if len(connections) == 0 {
		return nil, errors.New("no connections")
	}
	return connections[0], nil
}
