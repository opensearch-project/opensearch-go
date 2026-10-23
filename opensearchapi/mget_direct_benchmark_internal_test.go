// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// BenchmarkMGetDirect measures the direct driver's own work for one mget:
// resolving the documents to nodes, admitting each node's request, and merging
// the answers. Each node's answer is decoded once, before the timer starts, and
// handed back for every request with the same documents, so decoding, which the
// coordinator path pays as well, is not counted.
func BenchmarkMGetDirect(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("docs=%d", n), func(b *testing.B) {
			f := newMGetFake(b)
			c := f.client(MGetConfig{})

			var (
				mu      sync.Mutex
				answers = make(map[string]*MGetResp)
			)
			c.ext.mget.send = func(_ context.Context, _ *Client, node *opensearchtransport.Connection, req MGetReq) (*MGetResp, error) {
				ids := make([]string, len(req.Body.Docs))
				for i, op := range req.Body.Docs {
					ids[i] = op.ID
				}
				key := node.Name + "/" + strings.Join(ids, ",")

				mu.Lock()
				defer mu.Unlock()
				if resp, ok := answers[key]; ok {
					return resp, nil
				}
				docs := make([]string, len(req.Body.Docs))
				for i, op := range req.Body.Docs {
					docs[i] = mgetFound(f.indexOf(req, op), op.ID, node.Name)
				}
				resp, err := mgetResponse(b, http.StatusOK, docs)
				answers[key] = resp

				return resp, err
			}

			ids := make([]string, n)
			for i := range ids {
				ids[i] = fmt.Sprintf("doc-%d", i)
			}
			req := MGetReq{Body: &MGetBody{Docs: mgetDocs("idx", ids...)}}

			// Warm the answer cache, so that the timed loop measures only the driver.
			if _, err := c.Doc.MGet(b.Context(), req); err != nil {
				b.Fatal(err)
			}

			b.ReportAllocs()
			for b.Loop() {
				if _, err := c.Doc.MGet(b.Context(), req); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
