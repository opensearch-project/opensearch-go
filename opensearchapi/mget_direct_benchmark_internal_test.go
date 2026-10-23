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
	"sync"
	"testing"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// BenchmarkMGetDirect measures the direct driver's own work for one mget:
// routing the documents to nodes, encoding each node's request, admitting it,
// and merging the answers. Each node's answer is decoded once, before the timer
// starts, and handed back for every request with the same body, so decoding,
// which the coordinator path pays as well, is not counted.
func BenchmarkMGetDirect(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("docs=%d", n), func(b *testing.B) {
			f := newMGetFake(b)
			c := f.client(MGetConfig{})

			var (
				mu sync.Mutex
				// answers holds each node's decoded answer by the request body it
				// answers. Indexing it with string(body) does not allocate.
				answers = make(map[string]map[string]*MGetResp)
			)
			c.ext.mget.send = func(_ context.Context, _ *Client, node *opensearchtransport.Connection, req MGetReq) (*MGetResp, error) {
				body, _ := req.BodyReader.(*mgetBody)
				// The transport closes a request body once it has sent it.
				defer body.Close()

				mu.Lock()
				defer mu.Unlock()
				if resp, ok := answers[node.Name][string(body.Bytes())]; ok {
					return resp, nil
				}

				req = f.decodeBody(req)
				docs := make([]string, len(req.Body.Docs))
				for i, op := range req.Body.Docs {
					docs[i] = mgetFound(f.indexOf(req, op), op.ID, node.Name)
				}
				resp, err := mgetResponse(b, http.StatusOK, docs)
				if answers[node.Name] == nil {
					answers[node.Name] = make(map[string]*MGetResp)
				}
				answers[node.Name][string(body.Bytes())] = resp

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
