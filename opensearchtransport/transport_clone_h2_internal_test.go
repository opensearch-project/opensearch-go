// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

// IMPORTANT: every subtest below builds its own *http.Transport via a factory
// stored in the table row. Clone() and the original's nextProtoOnce both mutate
// state on the transport they're called on/with, so sharing one *http.Transport
// across subtests would let an earlier subtest's mutation leak into a later one
// and could make a broken case look fixed (or vice versa).

// TestShouldForceH2 tests the predicate in isolation: it never calls
// Clone or New, so it is not a regression test for the clone bug by itself --
// see TestNewClonedTransportSpeaksAdvertisedH2 and
// TestNewLeavesExplicitProtocolChoiceAlone for that.
//
// Every row supplies a non-nil TLSClientConfig, the precondition shouldForceH2
// documents and cloneForTLS guarantees.
func TestShouldForceH2(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		transportShape
		want bool
	}{
		{
			transportShape: transportShape{
				name: "h2 in NextProtos and nothing else set",
				newTransport: func(*testing.T) *http.Transport {
					return &http.Transport{TLSClientConfig: &tls.Config{NextProtos: []string{"h2", "http/1.1"}}}
				},
			},
			want: true,
		},
		{
			transportShape: transportShape{
				name: "ForceAttemptHTTP2 already true",
				newTransport: func(*testing.T) *http.Transport {
					return &http.Transport{
						ForceAttemptHTTP2: true,
						TLSClientConfig:   &tls.Config{NextProtos: []string{"h2", "http/1.1"}},
					}
				},
			},
			want: false,
		},
		{
			transportShape: transportShape{
				name: "Protocols set",
				newTransport: func(*testing.T) *http.Transport {
					return &http.Transport{
						Protocols:       &http.Protocols{},
						TLSClientConfig: &tls.Config{NextProtos: []string{"h2", "http/1.1"}},
					}
				},
			},
			want: false,
		},
		{
			transportShape: transportShape{
				name: "TLSNextProto set",
				newTransport: func(*testing.T) *http.Transport {
					return &http.Transport{
						TLSNextProto:    map[string]func(string, *tls.Conn) http.RoundTripper{},
						TLSClientConfig: &tls.Config{NextProtos: []string{"h2", "http/1.1"}},
					}
				},
			},
			want: false,
		},
		{
			transportShape: transportShape{
				name: "NextProtos is http/1.1 only",
				newTransport: func(*testing.T) *http.Transport {
					return &http.Transport{TLSClientConfig: &tls.Config{NextProtos: []string{"http/1.1"}}}
				},
			},
			want: false,
		},
		{
			transportShape: transportShape{
				name: "NextProtos is empty",
				newTransport: func(*testing.T) *http.Transport {
					return &http.Transport{TLSClientConfig: &tls.Config{}}
				},
			},
			want: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := shouldForceH2(tt.newTransport(t))
			require.Equal(t, tt.want, got)
		})
	}
}

// errNeverDialed is returned by the dummy dialers used by some of the shapes
// below. New never dials anything itself, so none of them are ever actually
// invoked; they only need to be non-nil to reproduce bug 1 (the nil
// TLSClientConfig panic).
var errNeverDialed = errors.New("dialer should never be invoked by New")

// TestNewCACertWithNilTLSClientConfigDoesNotPanic regression-tests bug 1: New
// used to dereference a nil TLSClientConfig when cfg.CACert was set. The root
// cause is broader than any one field: configureHTTP2 is the only thing that
// allocates TLSClientConfig, as a side effect of auto-configuring HTTP/2, so
// anything that makes net/http skip that auto-configuration leaves
// TLSClientConfig nil through Clone. That includes a custom
// Dial/DialContext/DialTLS/DialTLSContext hook, Protocols restricted to
// HTTP/1-only, and a non-nil TLSNextProto (empty, or already populated) --
// the documented way to disable HTTP/2 support outright.
func TestNewCACertWithNilTLSClientConfigDoesNotPanic(t *testing.T) {
	t.Parallel()

	caCertPEM := selfSignedCACertPEM(t)

	for _, tt := range []transportShape{
		{
			name: "Dial set",
			newTransport: func(*testing.T) *http.Transport {
				//nolint:staticcheck // exercising the deprecated field on purpose
				return &http.Transport{Dial: func(string, string) (net.Conn, error) { return nil, errNeverDialed }}
			},
		},
		{
			name: "DialContext set",
			newTransport: func(*testing.T) *http.Transport {
				return &http.Transport{DialContext: func(_ context.Context, _, _ string) (net.Conn, error) { return nil, errNeverDialed }}
			},
		},
		{
			name: "DialTLS set",
			newTransport: func(*testing.T) *http.Transport {
				//nolint:staticcheck // exercising the deprecated field on purpose
				return &http.Transport{DialTLS: func(string, string) (net.Conn, error) { return nil, errNeverDialed }}
			},
		},
		{
			name: "DialTLSContext set",
			newTransport: func(*testing.T) *http.Transport {
				return &http.Transport{DialTLSContext: func(_ context.Context, _, _ string) (net.Conn, error) { return nil, errNeverDialed }}
			},
		},
		{
			name: "Protocols set to HTTP/1-only",
			newTransport: func(*testing.T) *http.Transport {
				protocols := new(http.Protocols)
				protocols.SetHTTP1(true)
				return &http.Transport{Protocols: protocols}
			},
		},
		{
			name: "TLSNextProto non-nil but empty",
			newTransport: func(*testing.T) *http.Transport {
				return &http.Transport{TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{}}
			},
		},
		{
			name: `TLSNextProto contains "h2"`,
			newTransport: func(*testing.T) *http.Transport {
				return &http.Transport{TLSNextProto: map[string]func(string, *tls.Conn) http.RoundTripper{"h2": nil}}
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tp, err := New(Config{
				Transport:    tt.newTransport(t),
				CACert:       caCertPEM,
				DisableRetry: true,
				MaxRetries:   1,
			})
			require.NoError(t, err, "New must not panic or error for a %q transport with CACert set", tt.name)
			require.NotNil(t, tp)
			t.Cleanup(func() { _ = tp.Close() })
		})
	}
}

// TestNewRejectsNonHTTPTransportForTLSSettings covers cloneForTLS's type
// assertion. CACert and InsecureSkipVerify attach to a cloned *http.Transport,
// so any other RoundTripper is a configuration error rather than something New
// applies partially or ignores. The assertion asserts the message prefix rather
// than the full text, which ends in the offending type.
func TestNewRejectsNonHTTPTransportForTLSSettings(t *testing.T) {
	t.Parallel()

	caCertPEM := selfSignedCACertPEM(t)

	for _, tt := range []struct {
		name    string
		apply   func(cfg *Config)
		wantErr string
	}{
		{
			name:    "CACert",
			apply:   func(cfg *Config) { cfg.CACert = caCertPEM },
			wantErr: "unable to set CA certificate for transport of type",
		},
		{
			name:    "InsecureSkipVerify",
			apply:   func(cfg *Config) { cfg.InsecureSkipVerify = true },
			wantErr: "unable to set InsecureSkipVerify for transport of type",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := Config{Transport: &captureTripper{}, DisableRetry: true, MaxRetries: 1}
			tt.apply(&c)

			tp, err := New(c)
			require.ErrorContains(t, err, tt.wantErr)
			require.Nil(t, tp)
		})
	}
}

// TestNewDoesNotMutateCallersTransport covers the package's central promise:
// New attaches TLS settings to a clone, never to the caller's own
// *http.Transport. Clone() does have one documented side effect on the
// original -- it fires nextProtoOnce, so TLSClientConfig may come back
// non-nil with only NextProtos populated -- so this asserts the specific
// fields New's TLS branches write (RootCAs, InsecureSkipVerify), not that
// TLSClientConfig stays nil outright.
func TestNewDoesNotMutateCallersTransport(t *testing.T) {
	t.Parallel()

	caCertPEM := selfSignedCACertPEM(t)

	for _, tt := range tlsApplyCases(caCertPEM) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			original := &http.Transport{}
			c := Config{Transport: original, DisableRetry: true, MaxRetries: 1}
			tt.apply(&c)

			tp, err := New(c)
			require.NoError(t, err)
			t.Cleanup(func() { _ = tp.Close() })

			require.False(t, original.ForceAttemptHTTP2, "New must not force HTTP/2 on the caller's transport")
			if original.TLSClientConfig != nil {
				require.Nil(t, original.TLSClientConfig.RootCAs, "New must not attach a CA pool to the caller's transport")
				require.False(t, original.TLSClientConfig.InsecureSkipVerify, "New must not set InsecureSkipVerify on the caller's transport")
			}
		})
	}
}

// TestNewClonedTransportSpeaksAdvertisedH2 regression-tests bug 2 end to end
// through the public API: an HTTP/2-eligible transport, cloned by New to attach
// CACert or InsecureSkipVerify, must come back able to speak the h2 it
// advertises over ALPN rather than negotiating h2 and then writing HTTP/1.1
// framing (which the server below rejects).
func TestNewClonedTransportSpeaksAdvertisedH2(t *testing.T) {
	t.Parallel()

	runH2EligibleShapes(t, newHTTP2Server(t, okHandler()), "HTTP/2.0")
}

// TestNewHTTP2SpecialCases covers two end-to-end shapes that don't fit the
// newTransport-factory table above: a nil Transport (the http.DefaultTransport
// path) and a transport that already forces HTTP/2. Both must still reach
// HTTP/2.0 against an HTTP/2-capable server once CACert clones them.
func TestNewHTTP2SpecialCases(t *testing.T) {
	t.Parallel()

	srv := newHTTP2Server(t, okHandler())
	caCertPEM := caCertPEMFor(srv)

	for _, tt := range []struct {
		name      string
		transport http.RoundTripper
	}{
		{name: "nil transport (http.DefaultTransport)", transport: nil},
		{name: "ForceAttemptHTTP2 already true", transport: &http.Transport{ForceAttemptHTTP2: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := Config{
				URLs:         []*url.URL{mustParseURL(srv.URL)},
				Transport:    tt.transport,
				CACert:       caCertPEM,
				DisableRetry: true,
				MaxRetries:   1,
			}

			resp := doRequest(t, c) //nolint:bodyclose // doRequest closes the response body via t.Cleanup
			require.Equal(t, "HTTP/2.0", resp.Proto)
		})
	}
}

// TestNewLeavesExplicitProtocolChoiceAlone covers the "do not touch a
// transport that already settled its own protocol" side of bug 2: a transport
// that a custom dialer, a caller-supplied TLSClientConfig, or an explicit
// NextProtos already leaves speaking HTTP/1.1 must keep doing so, even against
// an HTTP/2-capable server, once CACert or InsecureSkipVerify clones it.
func TestNewLeavesExplicitProtocolChoiceAlone(t *testing.T) {
	t.Parallel()

	runNoOverrideCases(t, newHTTP2Server(t, okHandler()))
}

// TestNewOverHTTP1OnlyServer runs every shape from the two tests above against
// a server that never offers h2 over ALPN. shouldForceH2 must never
// fire here -- the bug this file guards against is invisible against an
// HTTP/1.1-only peer -- so every case must succeed as plain HTTP/1.1.
func TestNewOverHTTP1OnlyServer(t *testing.T) {
	t.Parallel()

	srv := httptest.NewUnstartedServer(okHandler())
	srv.StartTLS()
	t.Cleanup(srv.Close)

	runH2EligibleShapes(t, srv, "HTTP/1.1")
	runNoOverrideCases(t, srv)
}

// runH2EligibleShapes runs every HTTP/2-eligible shape against srv under each
// TLS case and asserts the protocol each one ends up negotiating. Shared by the
// HTTP/2-capable and HTTP/1.1-only servers, which differ only in wantProto.
func runH2EligibleShapes(t *testing.T, srv *httptest.Server, wantProto string) {
	t.Helper()

	caCertPEM := caCertPEMFor(srv)

	for _, shape := range h2EligibleShapes() {
		for _, tlsCase := range tlsApplyCases(caCertPEM) {
			t.Run(shape.name+"/"+tlsCase.name, func(t *testing.T) {
				t.Parallel()

				c := Config{
					URLs:         []*url.URL{mustParseURL(srv.URL)},
					Transport:    shape.newTransport(t),
					DisableRetry: true,
					MaxRetries:   1,
				}
				tlsCase.apply(&c)

				resp := doRequest(t, c) //nolint:bodyclose // doRequest closes the response body via t.Cleanup
				require.Equal(t, wantProto, resp.Proto)
			})
		}
	}
}

// runNoOverrideCases runs every already-settled-protocol shape against srv and
// asserts it still speaks HTTP/1.1. Shared by the HTTP/2-capable and
// HTTP/1.1-only servers, which expect that for different reasons -- the first
// because New must leave a stated protocol intent alone, the second because no
// peer offers h2 in the first place.
func runNoOverrideCases(t *testing.T, srv *httptest.Server) {
	t.Helper()

	for _, tt := range noOverrideCases(caCertPEMFor(srv)) {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := Config{
				URLs:         []*url.URL{mustParseURL(srv.URL)},
				Transport:    tt.newTransport(t),
				DisableRetry: true,
				MaxRetries:   1,
			}
			tt.apply(&c)

			resp := doRequest(t, c) //nolint:bodyclose // doRequest closes the response body via t.Cleanup
			require.Equal(t, "HTTP/1.1", resp.Proto)
		})
	}
}

// okHandler answers every request with 200 and an empty body. None of the tests
// in this file assert on the response payload, only on resp.Proto.
func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
}

// doRequest builds a Transport from cfg, issues one GET, and returns the
// response. The HTTP/2 vs HTTP/1.1 mismatch this file guards against surfaces
// as a nondeterministic error -- EOF on one run, a malformed-response error on
// another -- so callers assert on success and resp.Proto, never on error text.
func doRequest(t *testing.T, cfg Config) *http.Response {
	t.Helper()

	tp, err := New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, cfg.URLs[0].String(), nil)
	require.NoError(t, err)

	res, err := tp.Request(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = res.Body.Close() })

	return res
}

// tlsApplyCase is one way to trigger New's TLS branches.
type tlsApplyCase struct {
	name string
	// apply sets the TLS knob(s) under test on a Config that already has
	// URLs/DisableRetry/MaxRetries filled in.
	apply func(cfg *Config)
}

// tlsApplyCases returns the three ways New's TLS branches can be triggered:
// CACert alone, InsecureSkipVerify alone, and both together -- the double-clone
// path, where the CACert branch clones the caller's transport and the
// InsecureSkipVerify branch clones that clone again.
func tlsApplyCases(caCertPEM []byte) []tlsApplyCase {
	return []tlsApplyCase{
		{name: "CACert", apply: func(cfg *Config) { cfg.CACert = caCertPEM }},
		{name: "InsecureSkipVerify", apply: func(cfg *Config) { cfg.InsecureSkipVerify = true }},
		{name: "both", apply: func(cfg *Config) { cfg.CACert = caCertPEM; cfg.InsecureSkipVerify = true }},
	}
}

// transportShape is a named factory for one table row's transport. Every row
// builds its own *http.Transport; see the file-level comment on why this must
// not be a shared value. newTransport takes the subtest's *testing.T so a
// factory can assert its own precondition (see alreadyUsedBareTransport).
type transportShape struct {
	name         string
	newTransport func(t *testing.T) *http.Transport
}

// h2EligibleShapes returns the transport shapes that are HTTP/2-eligible
// before New clones them: a bare transport, one with unrelated pool tuning,
// and a bare transport the caller already made one request with (so its
// nextProtoOnce already fired and its TLSClientConfig is already non-nil with
// NextProtos [h2 http/1.1] that the caller never explicitly set).
func h2EligibleShapes() []transportShape {
	return []transportShape{
		{name: "bare", newTransport: func(*testing.T) *http.Transport { return &http.Transport{} }},
		{name: "pool-tuned", newTransport: func(*testing.T) *http.Transport { return &http.Transport{MaxIdleConnsPerHost: 7} }},
		{name: "already used by caller", newTransport: alreadyUsedBareTransport},
	}
}

// alreadyUsedBareTransport fires tr's nextProtoOnce the same way a caller's
// prior request would, by making one RoundTrip attempt. net/http runs
// nextProtoOnce as the first step of RoundTrip, before dialing, so the target
// need not be reachable: onceSetNextProtoDefaults only inspects the
// transport's own fields, never the destination, so the dial failure below is
// expected and discarded. It asserts the precondition this shape exists to
// exercise -- TLSClientConfig ends up non-nil with "h2" in NextProtos, set by
// net/http rather than the caller -- so that if that ever stops holding, this
// shape fails loudly instead of silently degenerating into the "bare" shape.
func alreadyUsedBareTransport(t *testing.T) *http.Transport {
	t.Helper()

	tr := &http.Transport{}
	res, err := (&http.Client{Transport: tr}).Get("https://127.0.0.1:1/")
	if err == nil {
		_ = res.Body.Close()
	}

	require.NotNil(t, tr.TLSClientConfig, "nextProtoOnce should have populated TLSClientConfig")
	require.Contains(t, tr.TLSClientConfig.NextProtos, "h2", "nextProtoOnce should advertise an h2 the caller never set")

	return tr
}

// noOverrideCase pairs a transport that already settled its own protocol with
// the TLS knob that makes New clone it.
type noOverrideCase struct {
	transportShape
	apply func(cfg *Config)
}

// noOverrideCases covers transports that already settled their own protocol
// and must be left alone even though CACert or InsecureSkipVerify clones them.
func noOverrideCases(caCertPEM []byte) []noOverrideCase {
	return []noOverrideCase{
		{
			transportShape: transportShape{
				name: "custom DialContext plus InsecureSkipVerify",
				newTransport: func(*testing.T) *http.Transport {
					return &http.Transport{DialContext: func(_ context.Context, network, addr string) (net.Conn, error) {
						return net.Dial(network, addr)
					}}
				},
			},
			apply: func(cfg *Config) { cfg.InsecureSkipVerify = true },
		},
		{
			transportShape: transportShape{
				name: "caller-supplied TLSClientConfig",
				newTransport: func(*testing.T) *http.Transport {
					return &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
				},
			},
			apply: func(cfg *Config) { cfg.CACert = caCertPEM },
		},
		{
			transportShape: transportShape{
				name: "caller NextProtos http/1.1 only",
				newTransport: func(*testing.T) *http.Transport {
					return &http.Transport{TLSClientConfig: &tls.Config{NextProtos: []string{"http/1.1"}}}
				},
			},
			apply: func(cfg *Config) { cfg.InsecureSkipVerify = true },
		},
	}
}

// caCertPEMFor PEM-encodes srv's certificate the way a caller would build
// Config.CACert from a server's own cert.
func caCertPEMFor(srv *httptest.Server) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
}

// selfSignedCACertPEM returns a valid CACert PEM. Bug 1 panics during New's
// construction, before any request is sent, so the cert need not correspond
// to any server actually used in the test -- it only needs to parse.
func selfSignedCACertPEM(t *testing.T) []byte {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(srv.Close)
	return caCertPEMFor(srv)
}
