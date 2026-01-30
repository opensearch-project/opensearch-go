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
	"fmt"
	"net/http"
)

// ChainSelector tries multiple selectors in sequence using chain-of-responsibility pattern.
type ChainSelector struct {
	selectors []Selector // Chain of selectors to try in order
}

// ChainSelectorOption configures a chain selector.
type ChainSelectorOption func(*ChainSelector)

// WithSelector adds a selector to the chain.
func WithSelector(selector Selector) ChainSelectorOption {
	return func(c *ChainSelector) {
		c.selectors = append(c.selectors, selector)
	}
}

// NewDefaultSelector returns a smart selector with intelligent routing and fallback.
// This provides the recommended default selector behavior for most use cases.
func NewDefaultSelector() Selector {
	return NewSmartSelector().(Selector)
}

// NewChainSelector creates a new ChainSelector with the provided options.
// Selectors are tried in order until one returns a connection.
func NewChainSelector(opts ...ChainSelectorOption) *ChainSelector {
	c := &ChainSelector{}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// NewSelector creates a selector using sensible defaults.
// If no selectors are provided, defaults to round-robin selection.
// If one selector is provided, returns it directly.
// If multiple selectors are provided, chains them using ChainSelector.
func NewSelector(opts ...ChainSelectorOption) Selector {
	c := &ChainSelector{}

	for _, opt := range opts {
		opt(c)
	}

	// Default to round-robin if no selectors provided
	if len(c.selectors) == 0 {
		return NewRoundRobinSelector()
	}

	// If only one selector, return it directly
	if len(c.selectors) == 1 {
		return c.selectors[0]
	}

	return c
}

// Select implements the basic Selector interface by trying each selector in the chain.
func (c *ChainSelector) Select(connections []*Connection) (*Connection, error) {
	if len(connections) == 0 {
		return nil, ErrNoConnections
	}

	for _, selector := range c.selectors {
		if conn, err := selector.Select(connections); err == nil && conn != nil {
			return conn, nil
		}
	}

	return nil, fmt.Errorf("all selectors in chain failed to return a connection")
}

// SelectForRequest implements RequestAwareSelector by trying each selector in the chain.
func (c *ChainSelector) SelectForRequest(connections []*Connection, req *http.Request) (*Connection, error) {
	if len(connections) == 0 {
		return nil, ErrNoConnections
	}

	for _, selector := range c.selectors {
		switch s := selector.(type) {
		case RequestAwareSelector:
			if conn, err := s.SelectForRequest(connections, req); err == nil && conn != nil {
				return conn, nil
			}
		default:
			if conn, err := s.Select(connections); err == nil && conn != nil {
				return conn, nil
			}
		}
	}

	return nil, fmt.Errorf("all selectors in chain failed to return a connection")
}
