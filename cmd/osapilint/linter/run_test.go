// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package linter

import (
	"errors"
	"os"
	"testing"
)

// TestRewriteUsageErrors locks the invocation-error classification the CLI shim
// relies on to pick exit code 2 (vs 1) and to avoid reprinting a message the
// flag package already wrote. An unknown flag yields a message-less UsageError
// (flag printed it); a bad -src/-dst value yields the message the shim prints.
func TestRewriteUsageErrors(t *testing.T) {
	// flag.ContinueOnError writes the parse error and usage to os.Stderr; discard
	// it so a deliberately-bad invocation does not pollute test output.
	saved := os.Stderr
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = devnull
	t.Cleanup(func() { os.Stderr = saved; devnull.Close() })

	tests := []struct {
		name    string
		args    []string
		wantMsg string // "" means the flag package already printed the error
	}{
		{"unknown flag", []string{"-nope"}, ""},
		{"bad dst value", []string{"-dst=v"}, `-dst: invalid version "v" (want e.g. v4)`},
		{"bad src value", []string{"-src=x"}, `-src: invalid version "x" (want e.g. v4)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Rewrite(tt.args)
			var ue *UsageError
			if !errors.As(err, &ue) {
				t.Fatalf("Rewrite(%q) = %v; want *UsageError", tt.args, err)
			}
			if ue.Msg != tt.wantMsg {
				t.Errorf("Msg = %q; want %q", ue.Msg, tt.wantMsg)
			}
		})
	}
}
