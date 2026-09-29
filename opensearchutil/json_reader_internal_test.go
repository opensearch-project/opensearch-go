// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

// Licensed to Elasticsearch B.V. under one or more contributor
// license agreements. See the NOTICE file distributed with
// this work for additional information regarding copyright
// ownership. Elasticsearch B.V. licenses this file to you under
// the Apache License, Version 2.0 (the "License"); you may
// not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

//go:build !integration

package opensearchutil

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type errReader struct{}

func (errReader) Read(_ []byte) (int, error)         { return 1, errors.New("MOCK ERROR") }
func (errReader) Write(_ []byte) (int, error)        { return 0, errors.New("MOCK ERROR") }
func (errReader) WriteTo(_ io.Writer) (int64, error) { return 0, errors.New("MOCK ERROR") }

type Foo struct {
	Bar string
}

func (f Foo) EncodeJSON(w io.Writer) error {
	_, err := w.Write([]byte(`{"bar":"` + strings.ToUpper(f.Bar) + `"}` + "\n"))
	if err != nil {
		return err
	}
	return nil
}

// failingJSONEncoder writes prefix then returns an error, so encode can leave
// partial bytes in the destination writer.
type failingJSONEncoder struct {
	prefix string
}

func (f *failingJSONEncoder) EncodeJSON(w io.Writer) error {
	if f.prefix != "" {
		if _, err := w.Write([]byte(f.prefix)); err != nil {
			return err
		}
	}
	return errors.New("encode boom")
}

func TestJSONReader(t *testing.T) {
	t.Run("Default", func(t *testing.T) {
		out, _ := io.ReadAll(NewJSONReader(map[string]string{"foo": "bar"}))
		if string(out) != `{"foo":"bar"}`+"\n" {
			t.Fatalf("Unexpected output: %s", out)
		}
	})

	t.Run("Custom", func(t *testing.T) {
		out, _ := io.ReadAll(NewJSONReader(Foo{Bar: "baz"}))
		if string(out) != `{"bar":"BAZ"}`+"\n" {
			t.Fatalf("Unexpected output: %s", out)
		}
	})

	t.Run("WriteTo", func(t *testing.T) {
		b := bytes.NewBuffer([]byte{})
		r := JSONReader{val: map[string]string{"foo": "bar"}}
		n, err := r.WriteTo(b)
		if err != nil {
			t.Fatalf("Unexpected error: %s", err)
		}
		if int(n) != b.Len() {
			t.Fatalf("WriteTo returned %d, but wrote %d bytes", n, b.Len())
		}
		if b.String() != `{"foo":"bar"}`+"\n" {
			t.Fatalf("Unexpected output: %s", b.String())
		}
	})

	t.Run("Read error", func(t *testing.T) {
		b := []byte{}
		r := JSONReader{val: map[string]string{"foo": "bar"}, buf: errReader{}}
		_, err := r.Read(b)
		if err == nil {
			t.Fatalf("Expected error, got: %#v", err)
		}
	})

	t.Run("encode error leaves reader reusable with error", func(t *testing.T) {
		// A JSONEncoder that writes partial output then fails. Before the fix,
		// the first Read returned the error but left r.buf non-nil with the
		// partial bytes, so a later ReadAll returned that leftover with no error.
		partial := &failingJSONEncoder{prefix: `{"partial":`}
		r := JSONReader{val: partial}

		n, err := r.Read(make([]byte, 64))
		if n != 0 || err == nil {
			t.Fatalf("first Read: got n=%d err=%v, want n=0 and encode error", n, err)
		}

		out, err := io.ReadAll(&r)
		if err == nil {
			t.Fatalf("ReadAll after encode failure returned %q with nil error; want encode error again", out)
		}
		if len(out) != 0 {
			t.Fatalf("ReadAll after encode failure returned leftover %q; want empty", out)
		}

		// Unsupported values take the same path (empty buffer, encode error).
		r = JSONReader{val: make(chan int)}
		_, err = r.Read(make([]byte, 64))
		if err == nil {
			t.Fatal("expected encode error for unsupported type")
		}
		out, err = io.ReadAll(&r)
		if err == nil {
			t.Fatalf("second ReadAll after unsupported-type encode failure returned %q with nil error", out)
		}
	})

	t.Run("HTML characters are not escaped", func(t *testing.T) {
		tests := []struct {
			name string
			val  any
			want string
		}{
			{
				"angle brackets",
				map[string]string{"id": "prefix|<root_account>|suffix"},
				`{"id":"prefix|<root_account>|suffix"}` + "\n",
			},
			{
				"ampersand",
				map[string]string{"q": "foo&bar"},
				`{"q":"foo&bar"}` + "\n",
			},
			{
				"mixed",
				map[string]string{"v": "a&b<c>d"},
				`{"v":"a&b<c>d"}` + "\n",
			},
		}
		for _, tt := range tests {
			t.Run(tt.name+"/Read", func(t *testing.T) {
				out, _ := io.ReadAll(NewJSONReader(tt.val))
				if string(out) != tt.want {
					t.Fatalf("got %s, want %s", out, tt.want)
				}
			})
			t.Run(tt.name+"/WriteTo", func(t *testing.T) {
				var b bytes.Buffer
				r := JSONReader{val: tt.val}
				n, err := r.WriteTo(&b)
				if err != nil {
					t.Fatalf("Unexpected error: %s", err)
				}
				if int(n) != b.Len() {
					t.Fatalf("WriteTo returned %d, but wrote %d bytes", n, b.Len())
				}
				if b.String() != tt.want {
					t.Fatalf("got %s, want %s", b.String(), tt.want)
				}
			})
		}
	})
}
