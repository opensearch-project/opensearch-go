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
func FormatDuration(d time.Duration) string {
	if d < time.Millisecond {
		return strconv.FormatInt(int64(d), 10) + "nanos"
	}

	return strconv.FormatInt(int64(d)/int64(time.Millisecond), 10) + "ms"
}
