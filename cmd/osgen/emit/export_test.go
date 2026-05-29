// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

// Test bridges for unexported helpers used by black-box tests.
var (
	LowerFirst     = lowerFirst
	SplitFirstLine = splitFirstLine
)

// ImportSet exposes the unexported importSet type to black-box tests.
type ImportSet = importSet

// ImportGroup exposes importGroup to black-box tests.
var ImportGroup = importGroup

// BulkInnerItemType exposes bulkInnerItemType for the test caller
// without leaking the (string, bool) signature through the var-block.
func BulkInnerItemType(resp *ir.Type, reg *ir.TypeRegistry) (string, bool) {
	return bulkInnerItemType(resp, reg)
}
