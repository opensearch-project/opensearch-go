// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package corpus

import (
	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"
)

// removedType exercises the cross-hop removed-type diagnostic on the v4 -> v5
// hop: AliasDeleteResp exists in v4 but was removed outright in v5, so a
// reference to it must be reported as a MANUAL worklist item (not silently
// dropped). The import path still bumps to v5; the removed-type reference is
// what this fixture pins, so the golden keeps the reference in place under the
// bumped import. There is no v5 counterpart to rewrite it to - the report line
// is the whole point.
func removedType() bool {
	var r opensearchapi.AliasDeleteResp
	return r.Acknowledged
}
