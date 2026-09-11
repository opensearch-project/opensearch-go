// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package debuglog

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// pointerStringer has a String method that dereferences its receiver, the same
// shape as (*url.URL).String. It stands in for the 53 *url.URL values the client
// logs, without depending on net/url's internals.
type pointerStringer struct{ text string }

func (p *pointerStringer) String() string { return p.text }

func TestStringerText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		val  fmt.Stringer
		want string
	}{
		{
			name: "value renders its String result",
			val:  &pointerStringer{text: "node-1"},
			want: "node-1",
		},
		{
			name: "untyped nil",
			val:  nil,
			want: "<nil>",
		},
		{
			name: "typed nil pointer does not panic",
			val:  (*pointerStringer)(nil),
			want: "<nil>",
		},
		{
			name: "nil url does not panic",
			val:  (*url.URL)(nil),
			want: "<nil>",
		},
		{
			name: "non-pointer stringer is called",
			val:  1500 * time.Millisecond,
			want: "1.5s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, StringerText(tt.val))
		})
	}
}

// TestNopDiscards pins that every Nop method is safe to call and that the chain
// keeps returning an Event, which is what lets emitting sites drop their nil
// guard.
func TestNopDiscards(t *testing.T) {
	t.Parallel()

	nodeURL := &url.URL{Scheme: "https", Host: "localhost:9200"}

	require.NotPanics(t, func() {
		Nop().
			Str("a", "b").
			Strs("c", []string{"d"}).
			Int("e", 1).
			Int32("f", 2).
			Int64("g", 3).
			Uint32("h", 4).
			Float64("i", 0.5).
			Dur("j", time.Second).
			Time("k", time.Now()).
			Stringer("l", nodeURL).
			Err(fmt.Errorf("boom")).
			Msg("discarded")
	})
}
