// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mockhttp

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

// Constants for mock server management
const (
	DefaultPollingInterval = 100 * time.Millisecond
	DefaultTestTimeout     = 30 * time.Second
)

// ServerPool manages a collection of mock HTTP servers for testing
type ServerPool struct {
	servers []*Server
	ports   []int
	name    string
}

// Server represents a single mock HTTP server
type Server struct {
	server   *http.Server
	port     int
	url      *url.URL
	name     string
	isActive bool
}

// NewServerPool creates a new pool of mock servers
func NewServerPool(t *testing.T, name string, count int) (*ServerPool, error) {
	t.Helper()

	if count <= 0 {
		return nil, fmt.Errorf("server count must be positive, got %d", count)
	}

	pool := &ServerPool{
		servers: make([]*Server, count),
		ports:   make([]int, count),
		name:    name,
	}

	// Allocate ports for all servers
	for i := range count {
		serverName := fmt.Sprintf("%s-server-%d", name, i)
		port, err := AllocateMockPort(serverName)
		if err != nil {
			// Cleanup already allocated ports
			pool.cleanup()
			return nil, fmt.Errorf("failed to allocate port for server %d: %w", i, err)
		}

		mockServer := &Server{
			port: port,
			url:  GetMockServerURL(port),
			name: serverName,
		}

		pool.servers[i] = mockServer
		pool.ports[i] = port
	}

	// Ensure cleanup on test completion
	t.Cleanup(func() {
		pool.Stop(t)
	})

	return pool, nil
}

// Start starts all servers in the pool with the given handler
func (p *ServerPool) Start(t *testing.T, handler http.Handler) error {
	t.Helper()

	for i := range p.servers {
		if err := p.StartServer(t, i, handler); err != nil {
			return fmt.Errorf("failed to start server %d: %w", i, err)
		}
	}

	return nil
}

// StartServer starts a specific server in the pool
func (p *ServerPool) StartServer(t *testing.T, index int, handler http.Handler) error {
	t.Helper()

	if index < 0 || index >= len(p.servers) {
		return fmt.Errorf("server index %d out of range [0, %d)", index, len(p.servers))
	}

	mockServer := p.servers[index]
	if mockServer.isActive {
		return fmt.Errorf("server %d is already running", index)
	}

	addr := fmt.Sprintf("%s:%d", MockServerHost, mockServer.port)
	mockServer.server = &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start server in goroutine
	go func() {
		if err := mockServer.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			t.Errorf("Mock server %s failed: %v", mockServer.name, err)
		}
	}()

	// Wait for server to be ready
	if err := WaitForServerReady(t, addr, DefaultTestTimeout); err != nil {
		return fmt.Errorf("server %s not ready: %w", mockServer.name, err)
	}

	mockServer.isActive = true
	return nil
}

// Stop stops all servers in the pool and releases ports
func (p *ServerPool) Stop(t *testing.T) {
	t.Helper()

	for i := range p.servers {
		p.StopServer(t, i)
	}

	p.cleanup()
}

// StopServer stops a specific server in the pool
func (p *ServerPool) StopServer(t *testing.T, index int) {
	t.Helper()

	if index < 0 || index >= len(p.servers) {
		t.Errorf("Server index %d out of range [0, %d)", index, len(p.servers))
		return
	}

	mockServer := p.servers[index]
	if !mockServer.isActive || mockServer.server == nil {
		return
	}

	if err := mockServer.server.Close(); err != nil {
		t.Errorf("Failed to close server %s: %v", mockServer.name, err)
	}

	mockServer.isActive = false
	mockServer.server = nil
}

// RestartServer stops and starts a specific server with a new handler
func (p *ServerPool) RestartServer(t *testing.T, index int, handler http.Handler) error {
	t.Helper()

	p.StopServer(t, index)

	// Brief pause to allow port to be released
	time.Sleep(DefaultPollingInterval)

	return p.StartServer(t, index, handler)
}

// URLs returns all server URLs
func (p *ServerPool) URLs() []*url.URL {
	urls := make([]*url.URL, len(p.servers))
	for i, server := range p.servers {
		urls[i] = server.url
	}
	return urls
}

// Ports returns all allocated ports
func (p *ServerPool) Ports() []int {
	return append([]int(nil), p.ports...) // Return a copy
}

// GetServer returns a specific mock server
func (p *ServerPool) GetServer(index int) (*Server, error) {
	if index < 0 || index >= len(p.servers) {
		return nil, fmt.Errorf("server index %d out of range [0, %d)", index, len(p.servers))
	}
	return p.servers[index], nil
}

// IsActive returns true if the server at the given index is active
func (p *ServerPool) IsActive(index int) bool {
	if index < 0 || index >= len(p.servers) {
		return false
	}
	return p.servers[index].isActive
}

// Count returns the number of servers in the pool
func (p *ServerPool) Count() int {
	return len(p.servers)
}

// cleanup releases all allocated ports
func (p *ServerPool) cleanup() {
	for _, port := range p.ports {
		ReleasePort(port)
	}
}

// URL returns the server's URL
func (s *Server) URL() *url.URL {
	return s.url
}

// Port returns the server's port
func (s *Server) Port() int {
	return s.port
}

// Name returns the server's name
func (s *Server) Name() string {
	return s.name
}

// IsActive returns true if the server is currently running
func (s *Server) IsActive() bool {
	return s.isActive
}

// CreateMockOpenSearchResponse returns a standard mock OpenSearch root response
func CreateMockOpenSearchResponse() string {
	return `{
  "name": "test-node",
  "cluster_name": "test-cluster",
  "cluster_uuid": "test-cluster-uuid",
  "version": {
    "number": "3.4.0",
    "build_type": "tar",
    "build_hash": "test-hash",
    "build_date": "2024-01-01T00:00:00.000Z",
    "build_snapshot": false,
    "lucene_version": "9.11.0",
    "minimum_wire_compatibility_version": "7.10.0",
    "minimum_index_compatibility_version": "7.0.0",
    "distribution": "opensearch"
  },
  "tagline": "The OpenSearch Project: https://opensearch.org/"
}`
}

// WaitForServerReady waits for a server to be ready by trying to connect to it
func WaitForServerReady(t *testing.T, addr string, timeout time.Duration) error {
	t.Helper()
	start := time.Now()
	for time.Since(start) < timeout {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr, nil)
		if err != nil {
			cancel()
			time.Sleep(100 * time.Millisecond)
			continue
		}

		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil && resp.StatusCode == http.StatusOK {
			resp.Body.Close()
			return nil
		}
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
		time.Sleep(DefaultPollingInterval)
	}
	return fmt.Errorf("server %s not ready after %v", addr, timeout)
}
