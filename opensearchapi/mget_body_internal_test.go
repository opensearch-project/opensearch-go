// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchapi

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	bufbody "github.com/opensearch-project/opensearch-go/v5/internal/body"
)

func TestEncodeMGetBody(t *testing.T) {
	t.Parallel()

	ops := []MGetOperation{
		{ID: "a", Index: new("idx")},
		{ID: "b<&>", Index: new("idx"), Routing: new("r")},
		{ID: "c", Index: new("other")},
	}

	tests := []struct {
		name  string
		slots []int
		want  []MGetOperation
	}{
		{name: "the documents at the slots, in slot order", slots: []int{2, 0}, want: []MGetOperation{ops[2], ops[0]}},
		{name: "one document", slots: []int{1}, want: []MGetOperation{ops[1]}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body, err := encodeMGetBody(ops, tt.slots)
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = body.Close()
				body.release()
			})

			var got MGetBody
			require.NoError(t, json.Unmarshal(body.Bytes(), &got), "%s", body.Bytes())
			require.Equal(t, tt.want, got.Docs)

			want, err := json.Marshal(MGetBody{Docs: tt.want})
			require.NoError(t, err)
			require.JSONEq(t, string(want), string(body.Bytes()), "the body says what the generated request would")
		})
	}
}

func TestAppendMGetOperation(t *testing.T) {
	t.Parallel()

	noSource := NewSearchSourceConfigFromBool(false)
	tests := []struct {
		name   string
		op     MGetOperation
		wantOK bool
	}{
		{name: "id only", op: MGetOperation{ID: "a"}, wantOK: true},
		{name: "empty id", op: MGetOperation{ID: ""}, wantOK: true},
		{
			name: "every field it writes",
			op: MGetOperation{
				ID: "a", Index: new("idx"), Routing: new("r"), StoredFields: new("f1,f2"),
				Version: new(int64(-7)), VersionType: new(VersionTypeExternal),
			},
			wantOK: true,
		},
		{name: "_source is left to the encoder", op: MGetOperation{ID: "a", Source: &noSource}},
		{name: "html in the id", op: MGetOperation{ID: "b<&>"}},
		{name: "quote in the index", op: MGetOperation{ID: "a", Index: new(`i"x`)}},
		{name: "backslash in the routing", op: MGetOperation{ID: "a", Routing: new(`r\`)}},
		{name: "control byte in the stored fields", op: MGetOperation{ID: "a", StoredFields: new("f\n")}},
		{name: "non-ASCII in the version type", op: MGetOperation{ID: "a", VersionType: new(VersionType("é"))}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prefix := []byte(`{"docs":[`)
			got, ok := appendMGetOperation(prefix, &tt.op)
			require.Equal(t, tt.wantOK, ok)
			if !ok {
				require.Equal(t, prefix, got, "appends nothing when it leaves op to the encoder")
				return
			}

			want, err := json.Marshal(&tt.op)
			require.NoError(t, err)
			require.Equal(t, string(prefix)+string(want), string(got), "writes what encoding/json does")
		})
	}
}

// TestAppendMGetOperationKnowsEveryField fails when the generated MGetOperation
// gains or renames a field, which appendMGetOperation would otherwise drop from
// the body without a word.
func TestAppendMGetOperationKnowsEveryField(t *testing.T) {
	t.Parallel()

	var tags []string
	for f := range reflect.TypeFor[MGetOperation]().Fields() {
		tags = append(tags, f.Tag.Get("json"))
	}
	require.Equal(t, []string{
		"_id", "_index,omitempty", "_source,omitempty", "routing,omitempty",
		"stored_fields,omitempty", "version,omitempty", "version_type,omitempty",
	}, tags, "update appendMGetOperation to write the new field, then this list")
}

func TestMGetBodyIsABufferedReader(t *testing.T) {
	t.Parallel()

	body, err := encodeMGetBody([]MGetOperation{{ID: "a"}, {ID: "b"}}, []int{0, 1})
	require.NoError(t, err)
	content := string(body.Bytes())

	var br bufbody.BufferedReader = body
	require.Equal(t, len(content), br.Len())

	read, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, content, string(read))
	require.Zero(t, body.Len(), "reading the body consumes it")

	replay := bufbody.Replay(body)
	again, err := io.ReadAll(replay)
	require.NoError(t, err)
	require.Equal(t, content, string(again), "a replay reads the whole body from its start")

	require.NoError(t, replay.Close())
	require.NoError(t, body.Close())
	body.release()
}

func TestMGetBodyBufferOutlivesEveryReader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// releaseOrder releases the driver's reference, the body's own, and one
		// retry reader's, in the order given.
		releaseOrder []string
	}{
		{name: "the driver last", releaseOrder: []string{"body", "replay", "driver"}},
		{name: "the body last", releaseOrder: []string{"driver", "replay", "body"}},
		{name: "a retry reader last", releaseOrder: []string{"driver", "body", "replay"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body, err := encodeMGetBody([]MGetOperation{{ID: "a"}}, []int{0})
			require.NoError(t, err)
			replay, ok := body.NewReader().(io.ReadCloser)
			require.True(t, ok, "a retry reader can be closed, so the transport tells the body when it is done")

			release := map[string]func(){
				"driver": body.release,
				"body":   func() { _ = body.Close() },
				"replay": func() { _ = replay.Close() },
			}
			for i, who := range tt.releaseOrder {
				require.NotNil(t, body.b, "the buffer is kept while %d references remain", len(tt.releaseOrder)-i)
				release[who]()
			}
			require.Nil(t, body.b, "the last release returns the buffer to the pool")
		})
	}
}

func TestMGetBodyCloseReleasesOnce(t *testing.T) {
	t.Parallel()

	body, err := encodeMGetBody([]MGetOperation{{ID: "a"}}, []int{0})
	require.NoError(t, err)
	replay, ok := body.NewReader().(io.ReadCloser)
	require.True(t, ok)

	require.NoError(t, body.Close())
	require.NoError(t, body.Close())
	require.NoError(t, replay.Close())
	require.NoError(t, replay.Close())
	require.NotNil(t, body.b, "closing twice does not drop the driver's reference")

	body.release()
	require.Nil(t, body.b)
}

// BenchmarkMGetSubRequestBody compares the two ways a sub-request's body can be
// built: copying the documents into an MGetBody that the generated request then
// marshals, and encoding them into a pooled mgetBody that is released once the
// transport has closed it.
func BenchmarkMGetSubRequestBody(b *testing.B) {
	for _, n := range []int{10, 100, 1000} {
		ops := make([]MGetOperation, n)
		slots := make([]int, n)
		for i := range ops {
			ops[i] = MGetOperation{ID: fmt.Sprintf("doc-%d", i), Index: new("idx")}
			slots[i] = i
		}

		b.Run(fmt.Sprintf("docs=%d/marshal", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				docs := make([]MGetOperation, len(slots))
				for i, pos := range slots {
					docs[i] = ops[pos]
				}
				if _, err := json.Marshal(&MGetBody{Docs: docs}); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(fmt.Sprintf("docs=%d/pooled", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				body, err := encodeMGetBody(ops, slots)
				if err != nil {
					b.Fatal(err)
				}
				_ = body.Close()
				body.release()
			}
		})
	}
}
