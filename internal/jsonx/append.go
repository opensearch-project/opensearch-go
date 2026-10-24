// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package jsonx

// AppendString appends s quoted, as the client's encoders write it, and reports
// false, appending nothing, when s holds a byte they would escape or replace: a
// control byte, '"', '\\', DEL or any byte outside ASCII, and, when escapeHTML is
// set, '<', '>' and '&', which encoding/json escapes unless SetEscapeHTML(false).
// It is for the fast paths that write a small, fixed set of fields without
// reflection; a string it refuses is left to the encoder, which writes every
// string correctly, only slower.
func AppendString(b []byte, s string, escapeHTML bool) ([]byte, bool) {
	// One loop per setting, so that the test of each byte does not test the
	// setting as well.
	if escapeHTML {
		for i := range len(s) {
			if c := s[i]; c < ' ' || c > '~' || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' {
				return b, false
			}
		}
	} else {
		for i := range len(s) {
			if c := s[i]; c < ' ' || c > '~' || c == '"' || c == '\\' {
				return b, false
			}
		}
	}
	b = append(b, '"')
	b = append(b, s...)

	return append(b, '"'), true
}
