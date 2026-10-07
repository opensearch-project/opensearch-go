// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

//go:build !integration

package opensearchtransport_test

import (
	"compress/zlib"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/opensearch-project/opensearch-go/v5/opensearchtransport"
)

// zlibCompressor is a Compressor implemented outside the package, over
// compress/zlib, that sends "deflate" bodies.
type zlibCompressor struct{}

func (zlibCompressor) ContentEncoding() string { return "deflate" }

func (zlibCompressor) NewEncoder(w io.Writer) (opensearchtransport.Encoder, error) {
	return zlib.NewWriter(w), nil
}

// TestCompressor_ExternalImplementation verifies that a Compressor defined
// outside the package compresses requests end to end, including bodies written
// through a reused Encoder.
func TestCompressor_ExternalImplementation(t *testing.T) {
	t.Parallel()

	var received struct {
		sync.Mutex
		bodies []string
		errs   []error
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var (
			plain []byte
			err   error
		)
		if got := r.Header.Get("Content-Encoding"); got != "deflate" {
			err = fmt.Errorf("Content-Encoding is %q", got)
		} else if zr, zerr := zlib.NewReader(r.Body); zerr != nil {
			err = zerr
		} else {
			plain, err = io.ReadAll(zr)
		}

		received.Lock()
		defer received.Unlock()
		if err != nil {
			received.errs = append(received.errs, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received.bodies = append(received.bodies, string(plain))
	}))
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)

	tp, err := opensearchtransport.New(opensearchtransport.Config{
		URLs:              []*url.URL{u},
		Compressor:        zlibCompressor{},
		DisableRetry:      true,
		NodeStatsInterval: -1,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tp.Close() })

	want := []string{
		strings.Repeat("first|", 64),
		strings.Repeat("second|", 64),
		strings.Repeat("third|", 64),
	}
	for _, body := range want {
		req, err := http.NewRequest(http.MethodPost, "/abc", strings.NewReader(body))
		require.NoError(t, err)
		res, err := tp.Stream(req)
		require.NoError(t, err)
		require.NoError(t, res.Body.Close())
		require.Equal(t, http.StatusOK, res.StatusCode)
	}

	received.Lock()
	defer received.Unlock()
	require.Empty(t, received.errs)
	require.Equal(t, want, received.bodies)
}
