// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package emit

import (
	"fmt"
	"strings"
	"text/template"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

// PartialFailureFragment renders per-Resp partial-failure helper methods:
// one `<Wrapper>Failures()` method per emittable wrapper, plus a
// `PartialFailures(mask errmask.ErrorMask) []error` aggregator.
//
// The dispatch handler calls the aggregator and collapses the result
// (see DispatchFragment); callers wanting focused inspection at the call
// site invoke the per-wrapper methods directly without going through
// the dispatch error.
type PartialFailureFragment struct {
	Op       *ir.Operation
	Registry *ir.TypeRegistry
}

// Imports returns the imports the partial-failure-methods fragment needs.
func (f *PartialFailureFragment) Imports() []Import {
	if len(f.emittableWrappers()) == 0 {
		return nil
	}
	return []Import{{Path: errmaskImportPath}}
}

// Body renders the per-Resp helper methods + aggregator.
func (f *PartialFailureFragment) Body() (string, error) {
	wrappersList := f.emittableWrappers()
	if len(wrappersList) == 0 {
		return "", nil
	}

	respType := f.Op.TypePrefix + "Resp"

	var sb strings.Builder
	for _, w := range wrappersList {
		entry, ok := wrappers[w]
		if !ok || entry.RenderMethod == nil {
			continue
		}
		ctx := wrapperRenderCtx{
			Recv:     "r",
			Op:       f.Op,
			Registry: f.Registry,
			RespType: respType,
		}
		body, err := entry.RenderMethod(ctx)
		if err != nil {
			return "", fmt.Errorf("rendering %s method for %s: %w", w, f.Op.Group, err)
		}
		sb.WriteString(body)
	}

	// PartialFailures aggregator: gates each wrapper helper on the mask
	// bit and accumulates non-nil sub-errors.
	if err := f.renderAggregator(&sb, respType, wrappersList); err != nil {
		return "", err
	}

	return sb.String(), nil
}

// renderAggregator emits the `PartialFailures(mask)` method, calling
// each wrapper helper gated by the corresponding mask bit.
func (f *PartialFailureFragment) renderAggregator(sb *strings.Builder, respType string, wrappersList []string) error {
	type aggCall struct {
		MaskBit string // e.g. "BulkItems" -- same string as the wrapper constant
		Method  string // e.g. "BulkItemFailures"
	}
	calls := make([]aggCall, 0, len(wrappersList))
	for _, w := range wrappersList {
		calls = append(calls, aggCall{
			MaskBit: w, // wrapper constant matches the errmask bit identifier verbatim
			Method:  wrapperMethodName(w),
		})
	}

	tpl, err := template.New("partial-failures").Parse(`
// PartialFailures returns the partial-failure sub-errors detected on the
// {{.RespType}}, gated by mask. Mask bits suppress their corresponding
// wrapper category.
func (r *{{.RespType}}) PartialFailures(mask errmask.ErrorMask) []error {
	var errs []error
{{- range .Calls}}
	if !mask.Has(errmask.{{.MaskBit}}) {
		if e := r.{{.Method}}(); e != nil {
			errs = append(errs, e)
		}
	}
{{- end}}
	return errs
}

`)
	if err != nil {
		return fmt.Errorf("parsing PartialFailures aggregator template: %w", err)
	}
	return tpl.Execute(sb, struct {
		RespType string
		Calls    []aggCall
	}{RespType: respType, Calls: calls})
}

// emittableWrappers returns the subset of op.ErrorWrappers for which a
// hand-written method emission exists AND the response shape carries
// the required fields.
func (f *PartialFailureFragment) emittableWrappers() []string {
	if f.Op == nil || f.Op.Response == nil {
		return nil
	}
	var out []string
	for _, w := range f.Op.ErrorWrappers {
		entry, ok := wrappers[w]
		if !ok || entry.RenderMethod == nil {
			continue
		}
		if entry.Applies != nil && !entry.Applies(f.Op.Response, f.Registry) {
			continue
		}
		out = append(out, w)
	}
	return out
}

// wrapperMethodName returns the per-Resp helper method name for a
// wrapper schema. The rule is "singular form + Failures": the wrapper
// constants are PascalCase plurals (BulkItems, SearchShards,
// MultiSearchItems), and the Go method drops the trailing "s" and
// appends "Failures" so callers read e.g. r.BulkItemFailures(),
// r.SearchShardFailures(). Wrapper names ending in "Failures" already
// (NodeFailures, etc.) keep their suffix; that case isn't exercised
// today since none of those wrappers have a Render entry.
func wrapperMethodName(wrapper string) string {
	if strings.HasSuffix(wrapper, "Failures") {
		return wrapper
	}
	return strings.TrimSuffix(wrapper, "s") + "Failures"
}
