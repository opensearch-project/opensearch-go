// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchtransport

import (
	"testing"
	"time"
)

func TestParseDuration(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Duration
		ok   bool
	}{
		// time.ParseDuration format
		{name: "milliseconds", in: "500ms", want: 500 * time.Millisecond, ok: true},
		{name: "seconds_duration", in: "30s", want: 30 * time.Second, ok: true},
		{name: "minutes", in: "2m", want: 2 * time.Minute, ok: true},
		{name: "composite", in: "1m30s", want: 90 * time.Second, ok: true},
		{name: "zero_duration", in: "0s", want: 0, ok: true},
		{name: "negative_duration", in: "-5s", want: -5 * time.Second, ok: true},

		// Integer seconds
		{name: "integer_seconds", in: "30", want: 30 * time.Second, ok: true},
		{name: "integer_zero", in: "0", want: 0, ok: true},
		{name: "integer_negative", in: "-10", want: -10 * time.Second, ok: true},
		{name: "integer_large", in: "3600", want: time.Hour, ok: true},

		// Float seconds
		{name: "float_seconds", in: "1.5", want: 1500 * time.Millisecond, ok: true},
		{name: "float_fractional", in: "0.1", want: 100 * time.Millisecond, ok: true},
		{name: "float_small", in: "0.001", want: time.Millisecond, ok: true},
		{name: "float_negative", in: "-2.5", want: -2500 * time.Millisecond, ok: true},

		// Invalid input
		{name: "empty", in: "", want: 0, ok: false},
		{name: "garbage", in: "abc", want: 0, ok: false},
		{name: "unit_only", in: "ms", want: 0, ok: false},
		{name: "spaces", in: "30 s", want: 0, ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseDuration(tt.in)
			if ok != tt.ok {
				t.Errorf("parseDuration(%q) ok = %v, want %v", tt.in, ok, tt.ok)
			}
			if got != tt.want {
				t.Errorf("parseDuration(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
