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
	_ RequestAwareSelector = (*SelectorMux)(nil)
)

func TestNewSelectorMux(t *testing.T) {
	routes := []Route{
		{"GET /test", NewRoundRobinSelector()},
	}

	mux := NewSelectorMux(routes)

	if mux == nil {
		t.Errorf("Expected NewSelectorMux to return non-nil mux")
		return
	}

	if mux.mux == nil {
		t.Errorf("Expected internal http.ServeMux to be initialized")
	}
}

func TestSelectorMuxSelectForRequestExactMatch(t *testing.T) {
	muxMockSelector := &muxMockSelector{shouldFail: false}
	routes := []Route{
		{"GET /test", muxMockSelector},
	}

	mux := NewSelectorMux(routes)

	connections := []*Connection{
		{URL: &url.URL{Host: "localhost:9200"}},
	}

	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: "/test"},
	}

	conn, err := mux.SelectForRequest(connections, req)
	if err != nil {
		t.Errorf("Expected SelectForRequest to succeed, got error: %v", err)
	}

	if conn != connections[0] {
		t.Errorf("Expected connection from mock selector")
	}

	if !muxMockSelector.called {
		t.Errorf("Expected mock selector to be called")
	}
}

func TestSelectorMuxSelectForRequestMethodMatch(t *testing.T) {
	muxMockSelector := &muxMockSelector{shouldFail: false}
	routes := []Route{
		{"POST /_bulk", muxMockSelector},
	}

	mux := NewSelectorMux(routes)

	connections := []*Connection{
		{URL: &url.URL{Host: "localhost:9200"}},
	}

	// Test matching request
	req := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Path: "/_bulk"},
	}

	conn, err := mux.SelectForRequest(connections, req)
	if err != nil {
		t.Errorf("Expected SelectForRequest to succeed, got error: %v", err)
	}

	if conn != connections[0] {
		t.Errorf("Expected connection from mock selector")
	}

	if !muxMockSelector.called {
		t.Errorf("Expected mock selector to be called")
	}
}

func TestSelectorMuxSelectForRequestMethodMismatch(t *testing.T) {
	muxMockSelector := &muxMockSelector{shouldFail: false}
	routes := []Route{
		{"POST /_bulk", muxMockSelector},
	}

	mux := NewSelectorMux(routes)

	connections := []*Connection{
		{URL: &url.URL{Host: "localhost:9200"}},
	}

	// Test non-matching request (wrong method)
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: "/_bulk"},
	}

	_, err := mux.SelectForRequest(connections, req)
	if err == nil {
		t.Errorf("Expected SelectForRequest to fail when no route matches")
	}

	if muxMockSelector.called {
		t.Errorf("Expected mock selector not to be called for non-matching request")
	}
}

func TestSelectorMuxSelectForRequestSubtreeMatch(t *testing.T) {
	muxMockSelector := &muxMockSelector{shouldFail: false}
	routes := []Route{
		{"GET /_search/", muxMockSelector}, // Note trailing slash for subtree match
	}

	mux := NewSelectorMux(routes)

	connections := []*Connection{
		{URL: &url.URL{Host: "localhost:9200"}},
	}

	// Test subtree match
	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: "/_search/template"},
	}

	conn, err := mux.SelectForRequest(connections, req)
	if err != nil {
		t.Errorf("Expected SelectForRequest to succeed for subtree match, got error: %v", err)
	}

	if conn != connections[0] {
		t.Errorf("Expected connection from mock selector")
	}

	if !muxMockSelector.called {
		t.Errorf("Expected mock selector to be called for subtree match")
	}
}

func TestSelectorMuxSelectForRequestWildcardMatch(t *testing.T) {
	muxMockSelector := &muxMockSelector{shouldFail: false}
	routes := []Route{
		{"POST /{index}/_bulk", muxMockSelector},
	}

	mux := NewSelectorMux(routes)

	connections := []*Connection{
		{URL: &url.URL{Host: "localhost:9200"}},
	}

	// Test wildcard match
	req := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Path: "/my-index/_bulk"},
	}

	conn, err := mux.SelectForRequest(connections, req)
	if err != nil {
		t.Errorf("Expected SelectForRequest to succeed for wildcard match, got error: %v", err)
	}

	if conn != connections[0] {
		t.Errorf("Expected connection from mock selector")
	}

	if !muxMockSelector.called {
		t.Errorf("Expected mock selector to be called for wildcard match")
	}
}

func TestSelectorMuxSelectForRequestNoConnections(t *testing.T) {
	routes := []Route{
		{"GET /test", NewRoundRobinSelector()},
	}

	mux := NewSelectorMux(routes)

	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: "/test"},
	}

	_, err := mux.SelectForRequest([]*Connection{}, req)
	if err == nil {
		t.Errorf("Expected SelectForRequest to fail with no connections")
	}

	if !errors.Is(err, ErrNoConnections) {
		t.Errorf("Expected ErrNoConnections, got: %v", err)
	}
}

func TestSelectorMuxSelectForRequestNoMatchingRoute(t *testing.T) {
	routes := []Route{
		{"GET /test", NewRoundRobinSelector()},
	}

	mux := NewSelectorMux(routes)

	connections := []*Connection{
		{URL: &url.URL{Host: "localhost:9200"}},
	}

	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: "/nomatch"},
	}

	_, err := mux.SelectForRequest(connections, req)
	if err == nil {
		t.Errorf("Expected SelectForRequest to fail when no route matches")
	}

	expectedPrefix := "no route matched request:"
	if len(err.Error()) < len(expectedPrefix) || err.Error()[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("Expected error to start with %q, got %q", expectedPrefix, err.Error())
	}
}

func TestSelectorMuxMultipleRoutes(t *testing.T) {
	muxMockSelector1 := &muxMockSelector{shouldFail: false}
	muxMockSelector2 := &muxMockSelector{shouldFail: false}

	routes := []Route{
		{"POST /_bulk", muxMockSelector1},
		{"GET /_search", muxMockSelector2},
	}

	mux := NewSelectorMux(routes)

	connections := []*Connection{
		{URL: &url.URL{Host: "localhost:9200"}},
	}

	// Test first route
	req1 := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Path: "/_bulk"},
	}

	conn1, err := mux.SelectForRequest(connections, req1)
	if err != nil {
		t.Errorf("Expected SelectForRequest to succeed for first route, got error: %v", err)
	}

	if conn1 != connections[0] {
		t.Errorf("Expected connection from first mock selector")
	}

	if !muxMockSelector1.called {
		t.Errorf("Expected first mock selector to be called")
	}

	if muxMockSelector2.called {
		t.Errorf("Expected second mock selector not to be called")
	}

	// Reset for second test
	muxMockSelector1.called = false
	muxMockSelector2.called = false

	// Test second route
	req2 := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Path: "/_search"},
	}

	conn2, err := mux.SelectForRequest(connections, req2)
	if err != nil {
		t.Errorf("Expected SelectForRequest to succeed for second route, got error: %v", err)
	}

	if conn2 != connections[0] {
		t.Errorf("Expected connection from second mock selector")
	}

	if muxMockSelector1.called {
		t.Errorf("Expected first mock selector not to be called")
	}

	if !muxMockSelector2.called {
		t.Errorf("Expected second mock selector to be called")
	}
}

// Mock selector for mux testing
type muxMockSelector struct {
	shouldFail bool
	called     bool
}

func (m *muxMockSelector) Select(connections []*Connection) (*Connection, error) {
	m.called = true
	if m.shouldFail {
		return nil, errors.New("mux mock selector failure")
	}
	if len(connections) == 0 {
		return nil, errors.New("no connections")
	}
	return connections[0], nil
}
