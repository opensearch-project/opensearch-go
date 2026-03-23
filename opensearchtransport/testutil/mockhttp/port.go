// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package mockhttp

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/opensearch-project/opensearch-go/v4/opensearchtransport/testutil"
)

// Constants for mock server ports and default schemes
const (
	MockServerHost = "localhost"
	MockPortStart  = 9700
	MockPortEnd    = 9799

	DefaultOpenSearchSchemeInsecure = "http"
	DefaultOpenSearchSchemeSecure   = "https"

	// DefaultOpenSearchHost is the default host used when talking to test OpenSearch instances.
	DefaultOpenSearchHost = "localhost"

	// DefaultOpenSearchPort is the default port used when talking to test OpenSearch instances.
	DefaultOpenSearchPort = 9200
)

// TestPort represents an allocated port for testing
type TestPort struct {
	Port  int
	Owner string
	Pool  string
}

// Port pool configuration
type portPoolConfig struct {
	start, end int
	counter    *atomic.Uint32
}

// Track all allocated ports for the mock pool
var (
	mockPortPool   *portPoolConfig          //nolint:gochecknoglobals // Global test utility state
	allocatedPorts = make(map[int]TestPort) //nolint:gochecknoglobals // Global test utility state
)

// init initializes the mock port pool to avoid race conditions
func init() { //nolint:gochecknoinits // Required for test utility initialization
	mockPortPool = &portPoolConfig{MockPortStart, MockPortEnd, &atomic.Uint32{}}
}

// GetOpenSearchURL returns the OpenSearch URL as a parsed *url.URL.
// Delegates to testutil.GetTestURL for canonical URL construction.
func GetOpenSearchURL(t *testing.T) *url.URL {
	t.Helper()
	return testutil.GetTestURL(t)
}

// Port allocation functions

// AllocateMockPort allocates a port from the mock pool for the given name
func AllocateMockPort(name string) (int, error) {
	testPort, err := AllocatePort("mock", name)
	if err != nil {
		return 0, err
	}
	return testPort.Port, nil
}

// AllocatePort allocates a port from the mock pool for the given owner
func AllocatePort(poolName, owner string) (TestPort, error) {
	if poolName != "mock" {
		return TestPort{}, fmt.Errorf("unsupported pool: %s", poolName)
	}

	pool := mockPortPool
	poolSize := pool.end - pool.start + 1
	startOffset := int(pool.counter.Add(1) % uint32(poolSize)) //nolint:gosec // Port numbers are safe for uint32

	// Try ports starting from the counter position
	for i := range poolSize {
		offset := (startOffset + i) % poolSize
		port := pool.start + offset

		if _, inUse := allocatedPorts[port]; !inUse {
			testPort := TestPort{
				Port:  port,
				Owner: owner,
				Pool:  poolName,
			}
			allocatedPorts[port] = testPort
			return testPort, nil
		}
	}

	return TestPort{}, fmt.Errorf("no available ports in %s pool (range %d-%d)", poolName, pool.start, pool.end)
}

// ReleasePort releases a port back to the pool
func ReleasePort(port int) {
	delete(allocatedPorts, port)
}

// GetPort returns the TestPort for the given port number, or zero value if not allocated
func GetPort(port int) TestPort {
	if testPort, exists := allocatedPorts[port]; exists {
		return testPort
	}
	return TestPort{}
}

// GetMockServerURL returns a URL for a mock server at the given port.
// Mock servers always use HTTP (not HTTPS) since they don't have TLS certificates.
func GetMockServerURL(port int) *url.URL {
	return &url.URL{
		Scheme: DefaultOpenSearchSchemeInsecure,
		Host:   net.JoinHostPort(MockServerHost, strconv.Itoa(port)),
	}
}
