// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package params

import (
	"strings"
	"time"

	"github.com/opensearch-project/opensearch-go/v5/internal/apiutil"
)

// TimeoutParams holds timeout parameters shared across many operations.
type TimeoutParams struct {
	// Timeout is the operation timeout (e.g. how long to wait for a response).
	// A negative value sends -1 (see opensearchapi.DurationUnbounded); zero
	// leaves the parameter unset.
	Timeout time.Duration

	// ClusterManagerTimeout is how long to wait for a connection to the cluster manager.
	// A negative value sends -1; zero leaves the parameter unset.
	ClusterManagerTimeout time.Duration

	// MasterTimeout is the deprecated alias for ClusterManagerTimeout (pre-2.0 clusters).
	// A negative value sends -1; zero leaves the parameter unset.
	MasterTimeout time.Duration
}

// DebugParams holds the diagnostic parameters every operation accepts (the
// spec's global parameters). Display parameters such as format, h and v are
// fields of the operations that take them.
type DebugParams struct {
	// Pretty enables indented JSON responses for debugging readability.
	Pretty bool

	// Human enables human-friendly values in responses (e.g. "1.5gb" instead of bytes).
	Human bool

	// ErrorTrace includes the Java stack trace of errors in the response.
	ErrorTrace bool

	// FilterPath restricts the response to the specified JSON paths.
	FilterPath []string

	// Source provides an alternative way to pass the request body as a query parameter.
	Source string
}

// EncodeTimeout serializes non-zero timeout fields into query parameters.
func EncodeTimeout(p TimeoutParams, set func(k, v string)) {
	if p.Timeout != 0 {
		set("timeout", apiutil.FormatDuration(p.Timeout))
	}
	if p.ClusterManagerTimeout != 0 {
		set("cluster_manager_timeout", apiutil.FormatDuration(p.ClusterManagerTimeout))
	}
	if p.MasterTimeout != 0 {
		set("master_timeout", apiutil.FormatDuration(p.MasterTimeout))
	}
}

// EncodeDebug serializes non-zero debug fields into query parameters.
func EncodeDebug(p DebugParams, set func(k, v string)) {
	if p.Pretty {
		set("pretty", "true")
	}
	if p.Human {
		set("human", "true")
	}
	if p.ErrorTrace {
		set("error_trace", "true")
	}
	if len(p.FilterPath) > 0 {
		set("filter_path", strings.Join(p.FilterPath, ","))
	}
	if p.Source != "" {
		set("source", p.Source)
	}
}
