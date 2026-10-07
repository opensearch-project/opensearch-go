// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearchapi

import "time"

// SetSearchAfterRetryBackoff sets the wait before the first retry of a
// search_after page for c's scans, so retry tests don't wait seconds. It is
// compiled only into this package's tests.
func SetSearchAfterRetryBackoff(c *Client, d time.Duration) {
	c.searchAfterRetryBackoff = d
}
