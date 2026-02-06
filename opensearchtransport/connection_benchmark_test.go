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
	"fmt"
	"log"
	"net/http"
	_ "net/http/pprof"
	"net/url"
	"testing"
	"time"
)

func init() {
	go func() {
		server := &http.Server{
			Addr:         "localhost:6060",
			ReadTimeout:  5 * time.Second,
			WriteTimeout: 5 * time.Second,
		}
		log.Fatalln(server.ListenAndServe())
	}()
}

func initSingleConnectionPool() *singleConnectionPool {
	return &singleConnectionPool{
		connection: &Connection{
			URL: &url.URL{
				Scheme: "http",
				Host:   "foo1",
			},
		},
	}
}

func BenchmarkSingleConnectionPool(b *testing.B) {
	b.ReportAllocs()

	b.Run("Next()", func(b *testing.B) {
		pool := initSingleConnectionPool()

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
		pool := initSingleConnectionPool()

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

func createStatusConnectionPool(conns []*Connection) *statusConnectionPool {
	pool := &statusConnectionPool{
		resurrectTimeoutInitial:      defaultResurrectTimeoutInitial,
		resurrectTimeoutFactorCutoff: defaultResurrectTimeoutFactorCutoff,
	}
	pool.mu.live = conns
	return pool
}

func BenchmarkStatusConnectionPool(b *testing.B) {
	b.ReportAllocs()

	conns := make([]*Connection, 1000)
	for i := range 1000 {
		conns[i] = &Connection{URL: &url.URL{Scheme: "http", Host: fmt.Sprintf("foo%d", i)}}
	}

	b.Run("Next()", func(b *testing.B) {
		pool := createStatusConnectionPool(conns)

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
		pool := createStatusConnectionPool(conns)

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
		pool := createStatusConnectionPool(conns)

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
		pool := createStatusConnectionPool(conns)

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
