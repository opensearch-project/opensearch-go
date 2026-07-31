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

// UnionFragment renders oneOf/anyOf union types under every decode strategy.
// When Op is non-nil and represents a plugin operation, branch types are
// qualified with the core package prefix (e.g. opensearchapi.FieldSort)
// so the generated plugin file references shared types correctly.
type UnionFragment struct {
	Op       *ir.Operation
	Types    []*ir.Type
	Registry *ir.TypeRegistry
}

// Imports returns the imports the union-types fragment needs.
//
//   - bytes: the null-literal check, on every strategy except request selection
//     (whose UnmarshalJSON only stores the bytes).
//   - fmt: error construction, on the strategies that can fail to pick a branch
//     (discriminator, try-each, token-class switch) and on the request-selected
//     accessors, which wrap a branch mismatch.
//   - build: NullJSON everywhere, plus HasJSONKeys for try-each and
//     JSONDiscriminator for the discriminator strategy.
func (f *UnionFragment) Imports() []Import {
	if len(f.Types) == 0 {
		return nil
	}
	imps := []Import{
		{Path: "encoding/json"},
		{Path: LocalModule + "/internal/build"},
	}
	var needBytes, needFmt bool
	for _, t := range f.Types {
		switch {
		case t.Discriminator != nil:
			needBytes, needFmt = true, true
		case t.Merge != nil:
			needBytes = true
		case t.RequestSelected:
			needFmt = true // the As<Branch>() branch-mismatch error
		case t.Kind == ir.TypeAmbiguousWire: // try-each
			needBytes, needFmt = true, true
		default: // token-class switch
			needBytes, needFmt = true, true
		}
	}
	if needBytes {
		imps = append(imps, Import{Path: "bytes"})
	}
	if needFmt {
		imps = append(imps, Import{Path: "fmt"})
	}
	if f.Op != nil && f.Op.IsPlugin && f.Registry != nil && f.hasCrossPkgBranch() {
		imps = append(imps, Import{Path: f.Registry.CoreImport})
	}
	return imps
}

// hasCrossPkgBranch reports whether any union branch references a
// shared (core-package) type that needs cross-package qualification
// when this fragment is emitted into a plugin package.
func (f *UnionFragment) hasCrossPkgBranch() bool {
	for _, t := range f.Types {
		for _, b := range t.Branches {
			if isCrossPackageType(b.GoType, f.Registry) {
				return true
			}
		}
	}
	return false
}

// Body renders the union type definitions (and their UnmarshalJSON methods).
func (f *UnionFragment) Body() (string, error) {
	if len(f.Types) == 0 {
		return "", nil
	}

	var qualify func(string) string
	if f.Op != nil {
		qualify = qualifierFunc(f.Op.IsPlugin, f.Registry)
	} else {
		qualify = func(s string) string { return s }
	}

	// coreType names a hand-written core-package type (UnionBranchError). It is
	// not in the registry, so qualifyType cannot resolve it; a plugin package
	// must still reach it through the core package qualifier.
	coreType := func(name string) string { return name }
	if f.Op != nil && f.Op.IsPlugin && f.Registry != nil {
		coreType = func(name string) string { return f.Registry.CorePkg + "." + name }
	}

	var sb strings.Builder
	tmpl := template.Must(template.New("union").Funcs(template.FuncMap{
		"comment":     CommentWrap,
		"constName":   unionConstNameIR,
		"isTryEach":   func(k ir.TypeKind) bool { return k == ir.TypeAmbiguousWire },
		"qualify":     qualify,
		"quotedKeys":  quotedKeys,
		"embedField":  embedFieldName,
		"tokenObject": func() ir.TokenClass { return ir.TokenObject },
		"tokenArray":  func() ir.TokenClass { return ir.TokenArray },
		"tokenString": func() ir.TokenClass { return ir.TokenString },
		"tokenNumber": func() ir.TokenClass { return ir.TokenNumber },
		"tokenBool":   func() ir.TokenClass { return ir.TokenBool },
		"coreType":    coreType,
		"discField":   discriminatorFieldFor,
	}).Parse(unionFragTmplText))

	if err := tmpl.Execute(&sb, f.Types); err != nil {
		return "", fmt.Errorf("rendering UnionFragment: %w", err)
	}
	return sb.String(), nil
}

// discriminatorAssignment names the field a generated constructor must set, and
// the value to set it to, so a union built in Go carries its discriminator.
type discriminatorAssignment struct {
	Field string
	Value string
}

// discriminatorFieldFor returns the discriminator assignment for one branch of a
// union, or nil when there is none: the union declares no discriminator, or the
// branch does not expose the property as a settable string field.
//
// The union template's shared typedSurface block renders constructors for every
// decode strategy, so this lookup is how the discriminated strategy adds its
// field assignment without forking that block.
func discriminatorFieldFor(t *ir.Type, branchName string) *discriminatorAssignment {
	if t == nil || t.Discriminator == nil {
		return nil
	}
	for _, b := range t.Discriminator.Branches {
		if b.Name != branchName {
			continue
		}
		if b.DiscriminatorField == "" {
			return nil
		}
		return &discriminatorAssignment{Field: b.DiscriminatorField, Value: b.Value}
	}
	return nil
}

func unionConstNameIR(unionName, branchName string) string {
	return unionName + branchName + "Type"
}

// embedFieldName returns the selector used to reference an embedded type: the
// substring after the last package qualifier dot. "opensearchapi.GetResult"
// -> "GetResult"; "GetResult" -> "GetResult".
func embedFieldName(goType string) string {
	if i := strings.LastIndex(goType, "."); i >= 0 {
		return goType[i+1:]
	}
	return goType
}

// quotedKeys renders a slice of field names as a comma-separated list of
// Go double-quoted string literals, for splicing into a build.HasJSONKeys
// call in the generated try-each discriminator.
func quotedKeys(keys []string) string {
	quoted := make([]string, len(keys))
	for i, k := range keys {
		quoted[i] = fmt.Sprintf("%q", k)
	}
	return strings.Join(quoted, ", ")
}

// The union template is organized as one shared surface plus one UnmarshalJSON
// per decode strategy. "typedSurface" renders everything a union with a Type()
// needs (the struct, the <Union>Type enum, String, Type, RawJSON, SetRaw, the
// (T, error) branch accessors, and the New<Union>From<Branch> constructors), so
// the discriminator, merge, try-each, and token-class strategies share one
// definition of the public API and differ only in how they choose a branch.
//
// Request-selected unions do not use it: the wire never names their branch, so
// they have no Type() and their accessors are As<Branch>() rather than
// <Branch>().
const unionFragTmplText = `
{{- define "typedSurface"}}
type {{.Name}} struct {
	typ   {{.Name}}Type
	raw   json.RawMessage
	value any
}

// {{.Name}}Type names which branch of {{.Name}} is set.
type {{.Name}}Type int

const (
	{{.Name}}UnknownType {{.Name}}Type = iota
{{- range .Branches}}
	{{constName $.Name .Name}}
{{- end}}
)

// String names the branch, for diagnostics. Returns "unknown" when no branch has
// been decoded.
func (t {{.Name}}Type) String() string {
	switch t {
{{- range .Branches}}
	case {{constName $.Name .Name}}:
		return "{{.Name}}"
{{- end}}
	default:
		return "unknown"
	}
}

// Type returns which union branch was populated during decoding.
// Returns {{.Name}}UnknownType if the value has not been decoded.
func (u *{{.Name}}) Type() {{.Name}}Type { return u.typ }

// RawJSON returns the union's JSON bytes. After decoding these are borrowed
// from the response buffer: valid only while the owning response value is
// reachable, must not be mutated, and must be copied if retained beyond it.
func (u *{{.Name}}) RawJSON() json.RawMessage { return u.raw }

// SetRaw stages pre-encoded JSON for marshaling. MarshalJSON emits raw
// verbatim when no typed branch is set. Use the New{{.Name}}From*
// constructors to populate a typed branch instead; SetRaw is the typed
// escape hatch for callers that already have wire-format bytes.
func (u *{{.Name}}) SetRaw(raw json.RawMessage) {
	u.raw = raw
	u.value = nil
	u.typ = {{.Name}}UnknownType
}
{{range .Branches}}
// {{.Name}} returns the {{qualify .GoType}} branch value. It returns a
// *UnionBranchError when the union holds a different branch, naming the branch
// that is set; the returned value is the zero {{qualify .GoType}} in that case,
// which is indistinguishable from a decoded one, so check the error.
func (u *{{$.Name}}) {{.Name}}() ({{qualify .GoType}}, error) {
	if v, ok := u.value.(*{{qualify .GoType}}); ok {
		return *v, nil
	}
	var zero {{qualify .GoType}}
	return zero, &{{coreType "UnionBranchError"}}{Union: "{{$.Name}}", Want: "{{.Name}}", Got: u.typ.String()}
}

// New{{$.Name}}From{{.Name}} returns a {{$.Name}} populated with v
// on the {{.Name}} branch.
{{- $df := discField $ .Name}}
{{- if $df}}
//
// It sets v.{{$df.Field}} to {{printf "%q" $df.Value}} so the value marshals with the
// discriminator the spec requires, and so the result decodes back to this branch.
{{- end}}
func New{{$.Name}}From{{.Name}}(v {{qualify .GoType}}) {{$.Name}} {
{{- if $df}}
	v.{{$df.Field}} = {{printf "%q" $df.Value}}
{{- end}}
	return {{$.Name}}{
		typ:   {{constName $.Name .Name}},
		value: &v,
	}
}
{{end}}
{{- end}}
{{- define "marshalTyped"}}
func (u {{.Name}}) MarshalJSON() ([]byte, error) {
	if u.value != nil {
		return json.Marshal(u.value)
	}
	if len(u.raw) > 0 {
		return u.raw, nil
	}
	return build.NullJSON, nil
}
{{- end}}
{{- range $t := .}}
{{- if $t.Discriminator}}
{{- if $t.Comment}}
{{comment $t.Comment}}
{{- else}}
// {{$t.Name}} is a oneOf union whose branch the payload names itself.
{{- end}}
//
// The OpenAPI spec declares a discriminator on {{$t.Name}}: the {{$t.Discriminator.PropertyName}}
// property carries the branch name, so UnmarshalJSON reads that one property and
// decodes exactly that branch -- it never guesses, and a {{$t.Discriminator.PropertyName}} naming
// no known branch is an error rather than a silent mis-decode.
{{- if $t.Discriminator.DefaultValue}}
// A payload with no {{$t.Discriminator.PropertyName}} at all decodes as {{$t.Discriminator.DefaultValue}}, per the spec's
// x-default.
{{- end}}
//
// Use Type() to learn which branch was decoded, then call the corresponding
// accessor.
{{template "typedSurface" $t}}
func (u *{{$t.Name}}) UnmarshalJSON(data []byte) error {
	u.raw = data
	u.value = nil
	u.typ = {{$t.Name}}UnknownType
	if len(data) == 0 || bytes.Equal(data, build.NullJSON) {
		return nil
	}
	discriminator, present, err := build.JSONDiscriminator(data, "{{$t.Discriminator.PropertyName}}")
	if err != nil {
		return fmt.Errorf("{{$t.Name}}: reading {{$t.Discriminator.PropertyName}} discriminator: %w", err)
	}
{{- if $t.Discriminator.DefaultValue}}
	if !present {
		// The spec's x-default: this branch leaves {{$t.Discriminator.PropertyName}} optional, so its
		// absence identifies the branch just as a value would.
		discriminator = "{{$t.Discriminator.DefaultValue}}"
	}
{{- else}}
	if !present {
		return fmt.Errorf("{{$t.Name}}: payload has no {{$t.Discriminator.PropertyName}} discriminator: %s", data[:min(len(data), 64)])
	}
{{- end}}
	switch discriminator {
{{- range $t.Discriminator.Branches}}
	case "{{.Value}}":
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{.Const}}
		u.value = &v
{{- end}}
	default:
		return fmt.Errorf("{{$t.Name}}: unknown {{$t.Discriminator.PropertyName}} discriminator %q", discriminator)
	}
	return nil
}

{{template "marshalTyped" $t}}
{{- else if $t.Merge}}
{{- if $t.Comment}}
{{comment $t.Comment}}
{{- else}}
// {{$t.Name}} is a oneOf union decoded in a single pass.
{{- end}}
// The spec declares no discriminator, but each branch requires a JSON key the
// others lack, so one decode both populates the common branch and detects the
// others by key presence.
//
// Use Type() to determine which branch was decoded, then call
// the corresponding accessor.
{{template "typedSurface" $t}}
func (u *{{$t.Name}}) UnmarshalJSON(data []byte) error {
	u.raw = data
	u.value = nil
	u.typ = {{$t.Name}}UnknownType
	if len(data) == 0 || bytes.Equal(data, build.NullJSON) {
		return nil
	}
	// Single decode: embed the permissive (primary) branch and probe for the
	// distinguishing keys of the other branches in one pass. encoding/json
	// populates the embedded primary directly; the probes only test presence.
	type merged struct {
		{{qualify $t.Merge.PrimaryGoType}}
	{{- range $t.Merge.Probes}}
		{{.GoName}} json.RawMessage ` + "`json:\"{{.JSONKey}}\"`" + `
	{{- end}}
	}
	var m merged
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
{{- range $t.Merge.Branches}}
	if {{range $i, $p := .PresentProbes}}{{if $i}} && {{end}}len(m.{{$p}}) > 0{{end}} {
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{.Const}}
		u.value = &v
		return nil
	}
{{- end}}
	u.typ = {{$t.Merge.PrimaryConst}}
	u.value = &m.{{embedField (qualify $t.Merge.PrimaryGoType)}}
	return nil
}

{{template "marshalTyped" $t}}
{{- else if $t.RequestSelected}}
{{- if $t.Comment}}
{{comment $t.Comment}}
{{- else}}
// {{$t.Name}} is a oneOf union whose branch the request selects.
{{- end}}
//
// The spec declares no discriminator because the response payload carries no
// branch name: the caller picks the branch in its REQUEST and the response
// echoes it only in the enclosing map's key. Pass typed_keys=true and that key
// is prefixed with the type, e.g. "avg#my_agg" for the aggregation named
// my_agg; without it the key is the bare name and the type is only what the
// caller asked for.
//
// So there is deliberately no Type() method and no discriminant constants. Call
// the As<Branch>() accessor for the branch you requested. It returns a
// *UnionBranchError when the payload cannot be that branch, which catches
// asking for the wrong shape entirely (AsSum on a histogram result). It cannot
// catch a wrong branch of the SAME shape: several metric aggregates serialize
// identically as {"value": N}, so AsSum on an avg result succeeds. Only the
// request knows which it was.
type {{$t.Name}} struct {
	raw   json.RawMessage
	value any
}

// RawJSON returns the union's JSON bytes. After decoding these are borrowed
// from the response buffer: valid only while the owning response value is
// reachable, must not be mutated, and must be copied if retained beyond it.
func (u *{{$t.Name}}) RawJSON() json.RawMessage { return u.raw }

// SetRaw stages pre-encoded JSON for marshaling.
func (u *{{$t.Name}}) SetRaw(raw json.RawMessage) {
	u.raw = raw
	u.value = nil
}

// IsZero reports whether the union holds nothing: no decoded branch and no
// retained bytes. Distinguishes an absent union from one whose branch decoded to
// a zero value, which the As<Branch>() accessors cannot.
func (u *{{$t.Name}}) IsZero() bool { return u.value == nil && len(u.raw) == 0 }
{{range $t.Branches}}
// As{{.Name}} decodes the union as {{qualify .GoType}}, the branch the caller
// requested. It returns (zero, nil) when the union holds nothing -- test IsZero
// to tell that from a branch that decoded to a zero value.
//
// It returns a *UnionBranchError when the retained bytes cannot be a
// {{qualify .GoType}}: either they fail to decode, or they lack a property the
// branch requires. encoding/json ignores unknown properties, so the required-key
// probe is what catches a payload of the wrong shape entirely (asking a bucket
// aggregate for a single-metric one).
//
// It cannot catch a DIFFERENT branch that shares this one's wire shape; see
// {{$t.Name}} for why nothing in the payload could.
func (u *{{$t.Name}}) As{{.Name}}() ({{qualify .GoType}}, error) {
	if v, ok := u.value.(*{{qualify .GoType}}); ok {
		return *v, nil
	}
	var v {{qualify .GoType}}
	if len(u.raw) == 0 {
		return v, nil
	}
{{- if .Required}}
	if !build.HasJSONKeys(u.raw, {{quotedKeys .Required}}) {
		return v, &{{coreType "UnionBranchError"}}{
			Union: "{{$t.Name}}",
			Want:  "{{.Name}}",
			Got:   "incompatible payload",
			Err: fmt.Errorf("payload lacks required propert{{if gt (len .Required) 1}}ies{{else}}y{{end}} %q",
				[]string{ {{quotedKeys .Required}} }),
		}
	}
{{- end}}
	if err := json.Unmarshal(u.raw, &v); err != nil {
		return v, &{{coreType "UnionBranchError"}}{
			Union: "{{$t.Name}}",
			Want:  "{{.Name}}",
			Got:   "incompatible payload",
			Err:   err,
		}
	}
	return v, nil
}

// New{{$t.Name}}From{{.Name}} returns a {{$t.Name}} populated with v
// on the {{.Name}} branch.
func New{{$t.Name}}From{{.Name}}(v {{qualify .GoType}}) {{$t.Name}} {
	return {{$t.Name}}{
		value: &v,
	}
}
{{end}}
func (u *{{$t.Name}}) UnmarshalJSON(data []byte) error {
	u.raw = data
	u.value = nil
	return nil
}

func (u {{$t.Name}}) MarshalJSON() ([]byte, error) {
	if u.value != nil {
		return json.Marshal(u.value)
	}
	if len(u.raw) > 0 {
		return u.raw, nil
	}
	return build.NullJSON, nil
}
{{- else if isTryEach $t.Kind}}
{{- if $t.Comment}}
{{comment $t.Comment}}
{{- else}}
// {{$t.Name}} is a oneOf union decoded by trying each branch in turn.
{{- end}}
// The spec declares no discriminator and no single key tells the branches apart,
// so each is attempted (newest schema version first) until one decodes.
//
// Use Type() to determine which branch was decoded, then call
// the corresponding accessor.
{{template "typedSurface" $t}}
func (u *{{$t.Name}}) UnmarshalJSON(data []byte) error {
	u.raw = data
	u.value = nil
	u.typ = {{$t.Name}}UnknownType
	if len(data) == 0 || bytes.Equal(data, build.NullJSON) {
		return nil
	}
	// Pass 1: branches that declare required properties. A branch is eligible
	// only when the payload carries every required key, so a more specific branch
	// (e.g. an error sub-response keyed by "error") is not absorbed by a
	// structurally permissive success branch. encoding/json does not enforce a
	// schema's "required" set, hence the explicit key probe.
{{- range $t.Branches}}
{{- if .Required}}
	if build.HasJSONKeys(data, {{quotedKeys .Required}}) {
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err == nil {
			u.typ = {{constName $t.Name .Name}}
			u.value = &v
			return nil
		}
	}
{{- end}}
{{- end}}
	// Pass 2: permissive branches with no required properties, tried newest-first.
{{- range $t.Branches}}
{{- if not .Required}}
	{
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err == nil {
			u.typ = {{constName $t.Name .Name}}
			u.value = &v
			return nil
		}
	}
{{- end}}
{{- end}}
	return fmt.Errorf("{{$t.Name}}: no branch matched JSON: %s", data[:min(len(data), 64)])
}

{{template "marshalTyped" $t}}
{{- else}}
{{- if $t.Comment}}
{{comment $t.Comment}}
{{- else}}
// {{$t.Name}} is a oneOf union whose branches decode from different JSON tokens.
{{- end}}
// The spec declares no discriminator, but each branch is a different JSON token
// class (object, array, string, number, boolean), so the payload's first byte
// selects one.
//
// Use Type() to determine which branch was decoded, then call
// the corresponding accessor.
{{template "typedSurface" $t}}
func (u *{{$t.Name}}) UnmarshalJSON(data []byte) error {
	u.raw = data
	u.value = nil
	u.typ = {{$t.Name}}UnknownType
	if len(data) == 0 || bytes.Equal(data, build.NullJSON) {
		return nil
	}
	switch {
{{- range $t.Branches}}
{{- if eq .TokenClass tokenObject}}
	case data[0] == '{':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = &v
{{- else if eq .TokenClass tokenArray}}
	case data[0] == '[':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = &v
{{- else if eq .TokenClass tokenString}}
	case data[0] == '"':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = &v
{{- else if eq .TokenClass tokenNumber}}
	case data[0] >= '0' && data[0] <= '9' || data[0] == '-':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = &v
{{- else if eq .TokenClass tokenBool}}
	case data[0] == 't' || data[0] == 'f':
		var v {{qualify .GoType}}
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		u.typ = {{constName $t.Name .Name}}
		u.value = &v
{{- end}}
{{- end}}
	default:
		return fmt.Errorf("{{$t.Name}}: unexpected JSON token: %s", data[:1])
	}
	return nil
}

{{template "marshalTyped" $t}}
{{- end}}
{{end}}`
