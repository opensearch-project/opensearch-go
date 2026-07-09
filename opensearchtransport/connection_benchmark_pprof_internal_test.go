// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"net"
	"strconv"
	"testing"
)

func TestStartPprof(t *testing.T) {
	tests := []struct {
		name       string
		addr       string
		wantNil    bool
		wantNzPort bool // expect a non-zero bound port
	}{
		{name: "disabled", addr: "disabled", wantNil: true},
		{name: "invalid", addr: "bad::addr", wantNil: true},
		{name: "empty default", addr: "", wantNil: false, wantNzPort: true},
		{name: "explicit ephemeral", addr: "localhost:0", wantNil: false, wantNzPort: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ln := startPprof(tt.addr)
			if ln != nil {
				t.Cleanup(func() { _ = ln.Close() })
			}

			if tt.wantNil {
				if ln != nil {
					t.Fatalf("startPprof(%q) = %v, want nil", tt.addr, ln.Addr())
				}
				return
			}

			if ln == nil {
				t.Fatalf("startPprof(%q) = nil, want a listener", tt.addr)
			}

			tcpAddr, ok := ln.Addr().(*net.TCPAddr)
			if !ok {
				t.Fatalf("startPprof(%q) addr = %T, want *net.TCPAddr", tt.addr, ln.Addr())
			}
			if !tcpAddr.IP.IsLoopback() {
				t.Errorf("startPprof(%q) bound to %v, want loopback", tt.addr, tcpAddr.IP)
			}
			if tt.wantNzPort && tcpAddr.Port == 0 {
				t.Errorf("startPprof(%q) bound port = 0, want non-zero", tt.addr)
			}
		})
	}
}

// TestStartPprofNoCollisionOnRepeatedCalls guards issue #864 Bug 5. It proves
// ephemeral-port allocation hands out distinct ports, so repeated startups do
// not fight over one address. The real-world trigger -- a TIME_WAIT socket held
// by a prior `go test -bench` process -- cannot be reproduced within a single
// test binary, so two in-process starts is the closest observable proxy.
func TestStartPprofNoCollisionOnRepeatedCalls(t *testing.T) {
	ln1 := startPprof("localhost:0")
	if ln1 == nil {
		t.Fatal("first startPprof returned nil, want a listener")
	}
	t.Cleanup(func() { _ = ln1.Close() })

	ln2 := startPprof("localhost:0")
	if ln2 == nil {
		t.Fatal("second startPprof returned nil, want a listener")
	}
	t.Cleanup(func() { _ = ln2.Close() })

	addr1, ok := ln1.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("first listener addr = %T, want *net.TCPAddr", ln1.Addr())
	}
	addr2, ok := ln2.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("second listener addr = %T, want *net.TCPAddr", ln2.Addr())
	}
	if addr1.Port == addr2.Port {
		t.Errorf("both listeners bound the same port %d, want distinct ephemeral ports", addr1.Port)
	}
}

// TestStartPprofExplicitPort covers the PPROF_ADDR="host:port" override -- the
// path a developer uses to pin pprof to a known port. It has a different shape
// from the table above (it must first discover a free port to avoid hardcoding
// one that CI might already hold), so it lives in its own function. Grab a free
// ephemeral port, release it, then hand that explicit "localhost:<port>" back to
// startPprof and assert it binds exactly there.
func TestStartPprofExplicitPort(t *testing.T) {
	probe, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("could not reserve a probe port: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	addr := net.JoinHostPort("localhost", strconv.Itoa(port))
	ln := startPprof(addr)
	if ln == nil {
		t.Fatalf("startPprof(%q) = nil, want a listener", addr)
	}
	t.Cleanup(func() { _ = ln.Close() })

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("startPprof(%q) addr = %T, want *net.TCPAddr", addr, ln.Addr())
	}
	if tcpAddr.Port != port {
		t.Errorf("startPprof(%q) bound port = %d, want %d", addr, tcpAddr.Port, port)
	}
}
