// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	opensearch "github.com/opensearch-project/opensearch-go/v5"
	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// mgetReplayTransport is an http.RoundTripper that answers each mget from an
// answer it builds the first time it sees the request's body, and replays from
// then on. A replayed request costs it one *http.Response and one reader, so
// what a test measures over it is the client's own work: encoding each request,
// the transport, decoding each answer, and, for the direct driver, routing and
// merging.
type mgetReplayTransport struct {
	mu      sync.Mutex
	buf     bytes.Buffer
	answers map[string][]byte
}

// mgetReplayHeader is shared, read-only, by every replayed response.
var mgetReplayHeader = http.Header{"Content-Type": {"application/json"}}

func newMGetReplayTransport() *mgetReplayTransport {
	return &mgetReplayTransport{answers: make(map[string][]byte)}
}

func (rt *mgetReplayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	rt.buf.Reset()
	if req.Body != nil {
		_, _ = rt.buf.ReadFrom(req.Body)
		_ = req.Body.Close()
	}

	answer := []byte(`{}`)
	if strings.HasSuffix(req.URL.Path, "/_mget") {
		var ok bool
		// Indexing with string(bytes) does not allocate.
		if answer, ok = rt.answers[string(rt.buf.Bytes())]; !ok {
			answer = mgetReplayAnswer(rt.buf.Bytes())
			rt.answers[rt.buf.String()] = answer
		}
	}

	return &http.Response{
		StatusCode:    http.StatusOK,
		Header:        mgetReplayHeader,
		Body:          io.NopCloser(bytes.NewReader(answer)),
		ContentLength: int64(len(answer)),
		Request:       req,
	}, nil
}

// mgetReplayAnswer answers every document of an mget body as found.
func mgetReplayAnswer(body []byte) []byte {
	var req MGetBody
	if err := json.Unmarshal(body, &req); err != nil {
		panic(fmt.Sprintf("replay transport got a body that is not an mget: %v: %s", err, body))
	}
	docs := make([]string, len(req.Docs))
	for i, op := range req.Docs {
		docs[i] = mgetFound(*op.Index, op.ID, "replay")
	}

	return []byte(`{"docs":[` + strings.Join(docs, ",") + `]}`)
}

// mgetAllocMode is one way an mget can be fetched.
type mgetAllocMode struct {
	name   string
	driver MGetDriver
	fanOut MGetFanOut
}

// mgetAllocModes are the ways of fetching an mget that TestMGetAllocations and
// BenchmarkMGetAllocations measure.
var mgetAllocModes = []mgetAllocMode{
	{name: "coordinator", driver: MGetDriverCoordinator},
	{name: "direct/fewest-nodes", driver: MGetDriverDirect, fanOut: MGetFanOutFewestNodes},
	{name: "direct/least-loaded", driver: MGetDriverDirect, fanOut: MGetFanOutLeastLoaded},
}

// newMGetAllocClient returns a client whose requests the replay transport
// answers. The direct driver routes over the fake's shard map, and sends each
// node's request as it does against a cluster. The router is off, so the client
// starts no seed health check or discovery: AllocsPerRun counts mallocs
// process-wide, and either would allocate mid-measurement.
func newMGetAllocClient(tb testing.TB, mode mgetAllocMode) *Client {
	tb.Helper()

	tb.Setenv("OPENSEARCH_GO_ROUTER", "false")
	c, err := NewClient(Config{Client: opensearch.Config{
		Addresses: []string{"http://replay.invalid:9200"},
		Transport: newMGetReplayTransport(),
	}})
	require.NoError(tb, err)
	tb.Cleanup(func() { _ = c.Close() })

	d := newMGetDriver(MGetConfig{Driver: mode.driver, FanOut: mode.fanOut}, nil)
	d.driver, d.fanOut = mode.driver, mode.fanOut
	d.lookup = newMGetFake(tb)
	d.saturated = func(*opensearchtransport.Connection) bool { return false }
	c.ext.mget = d

	return c
}

func mgetAllocRequest(keys int) MGetReq {
	ids := make([]string, keys)
	for i := range ids {
		ids[i] = fmt.Sprintf("doc-%d", i)
	}

	return MGetReq{Body: &MGetBody{Docs: mgetDocs("idx", ids...)}}
}

// TestMGetAllocations holds the allocations of one mget, for each way of
// fetching it, under a bound: the bound is the score to beat, and a change that
// allocates more fails here. Lower a bound when a change lowers the count.
//
// Allocations per key are a bound divided by keys. The coordinator's per-key
// cost is decoding each answer, which every mode pays; what the direct modes
// add on top is a fixed cost per mget and a cost per node request. One table
// holds for every toolchain, because internal/jsonx runs the encoding/json/v2
// engine on each; on Go 1.26 the original encoding/json engine made about 11
// allocations per key here.
//
//nolint:paralleltest // testing.AllocsPerRun panics in a parallel test.
func TestMGetAllocations(t *testing.T) {
	// maxAllocs is the most allocations one mget of keys documents may make, by
	// mode and keys: what was measured, plus 1% or 2, whichever is more, for
	// sync.Pool entries a garbage collection drops during the run.
	maxAllocs := map[string]map[int]float64{
		"coordinator":         {1: 27, 10: 36, 100: 127, 1000: 1051},
		"direct/fewest-nodes": {1: 46, 10: 86, 100: 177, 1000: 1133},
		"direct/least-loaded": {1: 46, 10: 117, 100: 207, 1000: 1133},
	}

	for _, mode := range mgetAllocModes {
		for _, keys := range []int{1, 10, 100, 1000} {
			t.Run(fmt.Sprintf("%s/keys=%d", mode.name, keys), func(t *testing.T) {
				c := newMGetAllocClient(t, mode)
				req := mgetAllocRequest(keys)
				run := func() {
					resp, err := c.Doc.MGet(t.Context(), req)
					require.NoError(t, err)
					require.Len(t, resp.Docs, keys)
				}
				run()

				got := testing.AllocsPerRun(20, run)
				t.Logf("%-20s keys=%-5d allocs/mget=%7.0f allocs/key=%6.2f", mode.name, keys, got, got/float64(keys))
				if raceEnabled {
					t.Skip("the race detector allocates on its own")
				}
				require.LessOrEqual(t, got, maxAllocs[mode.name][keys], "allocations of one mget")
			})
		}
	}
}

// BenchmarkMGetAllocations runs the cases TestMGetAllocations bounds, for
// profiling with -memprofile. It reports allocations per key beside the usual
// allocations per mget.
func BenchmarkMGetAllocations(b *testing.B) {
	for _, mode := range mgetAllocModes {
		for _, keys := range []int{1, 10, 100, 1000} {
			b.Run(fmt.Sprintf("%s/keys=%d", mode.name, keys), func(b *testing.B) {
				c := newMGetAllocClient(b, mode)
				req := mgetAllocRequest(keys)
				if _, err := c.Doc.MGet(b.Context(), req); err != nil {
					b.Fatal(err)
				}

				b.ReportAllocs()
				var before, after runtime.MemStats
				runtime.ReadMemStats(&before)
				for b.Loop() {
					if _, err := c.Doc.MGet(b.Context(), req); err != nil {
						b.Fatal(err)
					}
				}
				runtime.ReadMemStats(&after)
				b.ReportMetric(float64(after.Mallocs-before.Mallocs)/float64(b.N)/float64(keys), "allocs/key")
			})
		}
	}
}
