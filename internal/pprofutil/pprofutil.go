// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package pprofutil starts a pprof HTTP server for benchmark and diagnostic
// runs. The blank net/http/pprof import registers the /debug/pprof handlers on
// the default mux; Start binds a listener and serves them.
package pprofutil

import (
	"errors"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof" //nolint:gosec // G108: intentional -- this package's purpose is to expose the pprof endpoints for benchmark/diagnostic runs
	"net/url"
	"strings"
	"time"
)

// pprofRWTimeout is the read/write timeout for the diagnostic server. It is a
// fixed Slowloris floor to satisfy gosec (G114), not a tuning knob: this server
// is a loopback benchmark/diagnostic endpoint hit by hand, not tuned per call.
const pprofRWTimeout = 5 * time.Second

// Start binds and serves the pprof server on addr, returning the listener
// (nil if it could not start). addr accepts an optional scheme:
//   - empty:       an ephemeral loopback port ("localhost:0"), so back-to-back
//     runs never collide on a TIME_WAIT socket.
//   - "host:port": an explicit address, e.g. "localhost:6060" or ":6060" (an
//     empty host binds all interfaces).
//
// A missing scheme is treated as "http://" and the address is parsed with
// url.Parse; malformed input is logged and skipped rather than bound.
func Start(addr string) net.Listener {
	if addr == "" {
		addr = "localhost:0"
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}

	u, err := url.Parse(addr)
	if err != nil {
		log.Printf("ignoring invalid pprof address: %v", err)
		return nil
	}

	ln, err := net.Listen("tcp", u.Host)
	if err != nil {
		log.Printf("pprof server failed to listen: %v", err)
		return nil
	}

	log.Printf("pprof server listening on http://%s/debug/pprof/", ln.Addr())

	go func() {
		server := &http.Server{
			ReadTimeout:  pprofRWTimeout,
			WriteTimeout: pprofRWTimeout,
		}
		// net.ErrClosed is the normal path when a caller closes the listener to
		// stop the server (e.g. a test's t.Cleanup); it is not a failure.
		if err := server.Serve(ln); err != nil &&
			!errors.Is(err, http.ErrServerClosed) &&
			!errors.Is(err, net.ErrClosed) {
			log.Printf("pprof server stopped: %v", err)
		}
	}()

	return ln
}
