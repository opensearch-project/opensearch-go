// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package ttlcache

import (
	"strings"
	"testing"
	"time"
)

// TestOnStrayKey_PanicsUnderTest verifies the should-never-happen lockstep
// violation (a mapKeys entry with no backing sync.Map value) surfaces as a
// panic under `go test`. In a production binary the same path logs and
// reconciles instead; see onStrayKey.
func TestOnStrayKey_PanicsUnderTest(t *testing.T) {
	c := New[int](time.Minute)
	c.mu.mapKeys[Key(42)] = struct{}{} // inject a stray key with no cache entry

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("sweep did not panic on a stray key")
		}
		if msg, _ := r.(string); !strings.Contains(msg, "stray key 42") {
			t.Fatalf("panic message %q missing stray-key detail", r)
		}
	}()

	c.sweep()
}
