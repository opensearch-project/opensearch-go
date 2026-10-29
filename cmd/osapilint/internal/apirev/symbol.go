// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package apirev

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// ownRefPrefix opens an own-package type reference (see ownPkgQualifier).
const ownRefPrefix = ownPkgQualifier + "."

// canonTokens matches a double-quoted string (escapes included), a backquoted
// string, an own-package type reference, or a module-major-version segment
// ("/vN" before "/" or "."). Alternation scans left to right, so a marker or
// version segment inside a struct tag is consumed by the quoted alternative
// and left alone.
var canonTokens = regexp.MustCompile("\"(?:\\\\.|[^\"\\\\])*\"|`[^`]*`|" +
	regexp.QuoteMeta(ownRefPrefix) + `[\pL_][\pL\pN_]*|/v[0-9]+[/.]`)

// canonType maps the own-package type references of a rendered type through
// renamed and strips module-major-version segments, so two spellings of one
// type compare equal. Field names, method names, tags and other-package names
// are never mapped.
func canonType(typ string, renamed map[string]string) string {
	return canonTokens.ReplaceAllStringFunc(typ, func(tok string) string {
		switch {
		case strings.HasPrefix(tok, ownRefPrefix):
			if to, ok := renamed[strings.TrimPrefix(tok, ownRefPrefix)]; ok {
				return ownRefPrefix + to
			}
			return tok
		case strings.HasPrefix(tok, "/v"):
			return tok[len(tok)-1:]
		}
		return tok
	})
}

// EquivalentSymbol reports nil when to equals from after applying renamed (old
// name to new name, for names in the symbol's own package; only the from side
// is mapped), or an error naming the first difference: kind, underlying type
// or signature, constant value, or a declared method (compared by name,
// signature and receiver kind).
func EquivalentSymbol(from, to Symbol, renamed map[string]string) error {
	if from.Kind != to.Kind {
		return fmt.Errorf("kind differs: %q vs %q", from.Kind, to.Kind)
	}
	if f, t := canonType(from.Type, renamed), canonType(to.Type, nil); f != t {
		return fmt.Errorf("type differs: %q vs %q", f, t)
	}
	if from.Value != to.Value {
		return fmt.Errorf("value differs: %q vs %q", from.Value, to.Value)
	}
	toMethods := make(map[string]Method, len(to.Methods))
	for _, m := range to.Methods {
		toMethods[m.Name] = m
	}
	for _, m := range from.Methods {
		tm, ok := toMethods[m.Name]
		if !ok {
			return fmt.Errorf("method %q missing from target", m.Name)
		}
		if f, t := canonType(m.Signature, renamed), canonType(tm.Signature, nil); f != t || m.PointerReceiver != tm.PointerReceiver {
			return fmt.Errorf("method %q differs: %q (pointer %t) vs %q (pointer %t)", m.Name, f, m.PointerReceiver, t, tm.PointerReceiver)
		}
		delete(toMethods, m.Name)
	}
	if len(toMethods) > 0 {
		return fmt.Errorf("method %q only on target", slices.Min(slices.Collect(maps.Keys(toMethods))))
	}
	return nil
}
