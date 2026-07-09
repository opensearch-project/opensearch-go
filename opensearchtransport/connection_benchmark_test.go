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

//go:build !integration

//nolint:testpackage // Can't be testpackage, because it tests the function resurrectWithLock()
package opensearchtransport

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	_ "net/http/pprof" // Import pprof handlers for benchmark profiling
	"net/url"
	"os"
	"testing"
	"time"
)

func init() {
	startPprof(os.Getenv("PPROF_ADDR"))
}

// startPprof binds and serves the pprof server for benchmark runs, returning
// the listener (nil if not started). PPROF_ADDR selects the address:
//   - unset/empty: ephemeral loopback port ("localhost:0"), so back-to-back
//     `go test -bench` runs never collide on a TIME_WAIT socket.
//   - "disabled":  do not start.
//   - "host:port": explicit address, e.g. "localhost:6060". Use an explicit
//     "localhost" host; an empty host (":0") binds all interfaces.
func startPprof(pprofAddr string) net.Listener {
	if pprofAddr == "disabled" {
		return nil
	}
	if pprofAddr == "" {
		pprofAddr = "localhost:0"
	}

	// Validate the address shape before binding; malformed input is logged and
	// skipped rather than bound. PPROF_ADDR is a developer-controlled env var in
	// this benchmark-only test binary, so its value is trusted -- the sanitizing
	// here is a shape check, not a defense against a hostile caller.
	host, port, err := net.SplitHostPort(pprofAddr)
	if err != nil {
		log.Printf("ignoring invalid PPROF_ADDR: %v", err)
		return nil
	}
	addr := net.JoinHostPort(host, port)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("pprof server failed to listen: %v", err)
		return nil
	}

	log.Printf("pprof server listening on http://%s/debug/pprof/", ln.Addr())

	go func() {
		server := &http.Server{
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
		}
		// pprof handlers are registered on the default mux by the blank import.
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

func initSingleServerPool() *singleServerPool {
	return &singleServerPool{
		connection: &Connection{
			URL: &url.URL{
				Scheme: "http",
				Host:   "foo1",
			},
		},
	}
}

func BenchmarkSingleServerPool(b *testing.B) {
	b.ReportAllocs()

	b.Run("Next()", func(b *testing.B) {
		pool := initSingleServerPool()

		b.Run("Single          ", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, err := pool.Next()
				if err != nil {
					b.Errorf("Unexpected error: %v", err)
				}
			}
		})

		b.Run("Parallel (1000)", func(b *testing.B) {
			b.SetParallelism(1000)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, err := pool.Next()
					if err != nil {
						b.Errorf("Unexpected error: %v", err)
					}
				}
			})
		})
	})

	b.Run("OnFailure()", func(b *testing.B) {
		pool := initSingleServerPool()

		b.Run("Single     ", func(b *testing.B) {
			c, _ := pool.Next()

			for i := 0; i < b.N; i++ {
				if err := pool.OnFailure(c); err != nil {
					b.Errorf("Unexpected error: %v", err)
				}
			}
		})

		b.Run("Parallel (1000)", func(b *testing.B) {
			b.SetParallelism(1000)
			b.RunParallel(func(pb *testing.PB) {
				c, _ := pool.Next()

				for pb.Next() {
					if err := pool.OnFailure(c); err != nil {
						b.Errorf("Unexpected error: %v", err)
					}
				}
			})
		})
	})
}

func createMultiServerPool(conns []*Connection) *multiServerPool {
	pool := &multiServerPool{
		resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
		resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
	}
	// Copy the slice so pool.mu.ready doesn't alias the caller's backing array.
	// removeFromReadyWithLock nils and truncates the ready slice, which would
	// corrupt the caller's slice if they share backing storage.
	ready := make([]*Connection, len(conns))
	copy(ready, conns)
	for _, conn := range ready {
		conn.state.Store(int64(newConnState(lcActive)))
		conn.mu.Lock()
		conn.storeDeadSince(time.Time{}) // Reset from prior benchmark sub-runs
		conn.mu.Unlock()
	}
	pool.mu.ready = ready
	pool.mu.activeCount = len(ready)
	pool.mu.dead = []*Connection{}
	return pool
}

func BenchmarkMultiServerPool(b *testing.B) {
	b.ReportAllocs()

	conns := make([]*Connection, 1000)
	for i := range 1000 {
		conns[i] = &Connection{URL: &url.URL{Scheme: "http", Host: fmt.Sprintf("foo%d", i)}}
	}

	b.Run("Next()", func(b *testing.B) {
		pool := createMultiServerPool(conns)

		b.Run("Single     ", func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				_, err := pool.Next()
				if err != nil {
					b.Errorf("Unexpected error: %v", err)
				}
			}
		})

		b.Run("Parallel (100)", func(b *testing.B) {
			b.SetParallelism(100)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, err := pool.Next()
					if err != nil {
						b.Errorf("Unexpected error: %v", err)
					}
				}
			})
		})

		b.Run("Parallel (1000)", func(b *testing.B) {
			b.SetParallelism(1000)
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					_, err := pool.Next()
					if err != nil {
						b.Errorf("Unexpected error: %v", err)
					}
				}
			})
		})
	})

	b.Run("OnFailure()", func(b *testing.B) {
		pool := createMultiServerPool(conns)

		b.Run("Single     ", func(b *testing.B) {
			c, err := pool.Next()
			if err != nil {
				b.Fatalf("Unexpected error: %s", err)
			}

			for i := 0; i < b.N; i++ {
				if err := pool.OnFailure(c); err != nil {
					b.Errorf("Unexpected error: %v", err)
				}
			}
		})

		b.Run("Parallel (10)", func(b *testing.B) {
			b.SetParallelism(10)
			b.RunParallel(func(pb *testing.PB) {
				c, err := pool.Next()
				if err != nil {
					b.Fatalf("Unexpected error: %s", err)
				}

				for pb.Next() {
					if err := pool.OnFailure(c); err != nil {
						b.Errorf("Unexpected error: %v", err)
					}
				}
			})
		})

		b.Run("Parallel (100)", func(b *testing.B) {
			b.SetParallelism(100)
			b.RunParallel(func(pb *testing.PB) {
				c, err := pool.Next()
				if err != nil {
					b.Fatalf("Unexpected error: %s", err)
				}

				for pb.Next() {
					if err := pool.OnFailure(c); err != nil {
						b.Errorf("Unexpected error: %v", err)
					}
				}
			})
		})
	})

	b.Run("OnSuccess()", func(b *testing.B) {
		pool := createMultiServerPool(conns)

		b.Run("Single     ", func(b *testing.B) {
			c, err := pool.Next()
			if err != nil {
				b.Fatalf("Unexpected error: %s", err)
			}

			for i := 0; i < b.N; i++ {
				pool.OnSuccess(c)
			}
		})

		b.Run("Parallel (10)", func(b *testing.B) {
			b.SetParallelism(10)
			b.RunParallel(func(pb *testing.PB) {
				c, err := pool.Next()
				if err != nil {
					b.Fatalf("Unexpected error: %s", err)
				}

				for pb.Next() {
					pool.OnSuccess(c)
				}
			})
		})

		b.Run("Parallel (100)", func(b *testing.B) {
			b.SetParallelism(100)
			b.RunParallel(func(pb *testing.PB) {
				c, err := pool.Next()
				if err != nil {
					b.Fatalf("Unexpected error: %s", err)
				}

				for pb.Next() {
					pool.OnSuccess(c)
				}
			})
		})
	})

	b.Run("resurrect()", func(b *testing.B) {
		pool := createMultiServerPool(conns)

		b.Run("Single", func(b *testing.B) {
			c, err := pool.Next()
			if err != nil {
				b.Fatalf("Unexpected error: %s", err)
			}
			err = pool.OnFailure(c)
			if err != nil {
				b.Fatalf("Unexpected error: %s", err)
			}

			for i := 0; i < b.N; i++ {
				pool.mu.Lock()
				pool.resurrectWithLock(c)
				pool.mu.Unlock()
			}
		})

		b.Run("Parallel (10)", func(b *testing.B) {
			b.SetParallelism(10)
			b.RunParallel(func(pb *testing.PB) {
				c, err := pool.Next()
				if err != nil {
					b.Fatalf("Unexpected error: %s", err)
				}
				err = pool.OnFailure(c)
				if err != nil {
					b.Fatalf("Unexpected error: %s", err)
				}

				for pb.Next() {
					pool.mu.Lock()
					pool.resurrectWithLock(c)
					pool.mu.Unlock()
				}
			})
		})

		b.Run("Parallel (100)", func(b *testing.B) {
			b.SetParallelism(100)
			b.RunParallel(func(pb *testing.PB) {
				c, err := pool.Next()
				if err != nil {
					b.Fatalf("Unexpected error: %s", err)
				}
				err = pool.OnFailure(c)
				if err != nil {
					b.Fatalf("Unexpected error: %s", err)
				}

				for pb.Next() {
					pool.mu.Lock()
					pool.resurrectWithLock(c)
					pool.mu.Unlock()
				}
			})
		})
	})
}
