// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//nolint:testpackage // Benchmarks access unexported newDialContextDNSCache, fakeResolver, and testDNSSettings.
package opensearchtransport

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rs/dnscache"
)

// BenchmarkDNSResolveStep isolates the only step this PR changes: name
// resolution. Cached (primed dnscache.Resolver) serves a hit; Uncached resolves
// on every call (pre-PR behavior). The following TCP dial is identical for both,
// so it is excluded to avoid burying the delta under localhost-connect noise.
// The latency sweep shows the trade: ~equal when DNS is healthy, cache wins
// growing linearly as the resolver slows.
func BenchmarkDNSResolveStep(b *testing.B) {
	const host = "resolve.example"

	latencies := []struct {
		name string
		dur  time.Duration
	}{
		{"HealthyDNS", 0},
		{"SlowDNS_1ms", 1 * time.Millisecond},
		{"DegradedDNS_10ms", 10 * time.Millisecond},
	}

	for _, lat := range latencies {
		// Cached: primed once, so the resolver latency is paid zero times in the loop.
		b.Run("Cached/"+lat.name, func(b *testing.B) {
			fr := &fakeResolver{addrs: []string{"127.0.0.1"}, latency: lat.dur}
			r := &dnscache.Resolver{Resolver: fr}
			if _, err := r.LookupHost(context.Background(), host); err != nil {
				b.Fatalf("prime: %v", err)
			}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := r.LookupHost(context.Background(), host); err != nil {
					b.Fatalf("lookup: %v", err)
				}
			}
		})

		// Uncached: resolves every call, paying the resolver latency each time.
		b.Run("Uncached/"+lat.name, func(b *testing.B) {
			fr := &fakeResolver{addrs: []string{"127.0.0.1"}, latency: lat.dur}

			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := fr.LookupHost(context.Background(), host); err != nil {
					b.Fatalf("lookup: %v", err)
				}
			}
		})
	}
}

// BenchmarkDNSResolveOutage models the failure that motivates the feature: the
// resolver is down and lookups time out before failing (outageDelay stands in
// for that timeout). Cached serves the last-known-good address so the request
// proceeds; Uncached has nothing to fall back on and fails after the timeout.
func BenchmarkDNSResolveOutage(b *testing.B) {
	const (
		host        = "outage.example"
		outageDelay = 5 * time.Millisecond // stand-in for a resolver timeout
	)
	outage := errors.New("resolver unreachable")

	b.Run("Cached_ServesStale", func(b *testing.B) {
		fr := &fakeResolver{addrs: []string{"127.0.0.1"}}
		r := &dnscache.Resolver{Resolver: fr}
		if _, err := r.LookupHost(context.Background(), host); err != nil {
			b.Fatalf("prime: %v", err)
		}
		// Resolver goes down; the production refresh loop persists the entry.
		fr.setErr(outage)
		fr.mu.Lock()
		fr.latency = outageDelay
		fr.mu.Unlock()
		r.RefreshWithOptions(dnscache.ResolverRefreshOptions{ClearUnused: true, PersistOnFailure: true})

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			ips, err := r.LookupHost(context.Background(), host)
			if err != nil || len(ips) == 0 {
				b.Fatalf("serve-stale lookup should succeed during outage: %v", err)
			}
		}
	})

	b.Run("Uncached_FailsAfterTimeout", func(b *testing.B) {
		fr := &fakeResolver{err: outage, latency: outageDelay}

		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := fr.LookupHost(context.Background(), host); err == nil {
				b.Fatal("uncached lookup must fail when the resolver is down")
			}
		}
	})
}
