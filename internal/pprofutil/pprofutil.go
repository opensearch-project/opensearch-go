// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

// Package pprofutil starts a pprof HTTP server for benchmark and diagnostic
// runs. Start registers the net/http/pprof handlers on a private mux and serves
// them on its own listener, keeping the endpoints off http.DefaultServeMux.
package pprofutil

import (
	"errors"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"net/url"
	"strings"
	"time"
)

// pprofRWTimeout is the read/write timeout for the diagnostic server. It is a
// fixed Slowloris floor to satisfy gosec (G114), not a tuning knob: this server
// is a loopback benchmark/diagnostic endpoint hit by hand, not tuned per call.
const pprofRWTimeout = 5 * time.Second

// pprofPath is the mount point for the pprof handlers. pprof.Index strips this
// exact prefix when routing to sub-profiles (heap, goroutine, ...), so the
// index handler must be registered here for those links to resolve.
const pprofPath = "/debug/pprof/"

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

	// Register on a private mux rather than http.DefaultServeMux so these
	// endpoints stay scoped to this server. pprof.Index handles the sub-profile
	// routes (heap, goroutine, ...) under pprofPath.
	mux := http.NewServeMux()
	mux.HandleFunc(pprofPath, pprof.Index)
	mux.HandleFunc(pprofPath+"cmdline", pprof.Cmdline)
	mux.HandleFunc(pprofPath+"profile", pprof.Profile)
	mux.HandleFunc(pprofPath+"symbol", pprof.Symbol)
	mux.HandleFunc(pprofPath+"trace", pprof.Trace)

	served := url.URL{Scheme: "http", Host: ln.Addr().String(), Path: pprofPath}
	log.Printf("pprof server listening on %s", served.String())

	go func() {
		server := &http.Server{
			Handler:      mux,
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
