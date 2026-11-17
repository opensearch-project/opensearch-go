// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apiutil

import (
	"strconv"
	"time"
)

// FormatDuration converts a duration to a string in the format accepted by OpenSearch.
//
// Every negative duration is sent as the bare string "-1", the only negative
// value the server accepts (it reads it as "no timeout / unbounded / disabled"
// for the parameter). Callers skip a zero duration, so zero is never formatted:
// zero means the parameter is not set.
func FormatDuration(d time.Duration) string {
	if d < 0 {
		return "-1"
	}

	if d < time.Millisecond {
		return strconv.FormatInt(int64(d), 10) + "nanos"
	}

	return strconv.FormatInt(int64(d)/int64(time.Millisecond), 10) + "ms"
}
