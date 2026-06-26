// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"fmt"
	"go/token"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

type walker struct {
	registry *typeRegistry
	spec     *openapi3.T
	inFlight map[string]struct{} // cycle detection
	vrange   VersionRange

	// excludedFields collects properties dropped by the version-range
	// filter so the caller can render breadcrumb comments. Each entry is
	// keyed as "<TypeName>.<FieldName>" with a "(req)" or "(resp)" suffix
	// to disambiguate the same type appearing on both sides of a request.
	excludedFields []ir.Exclusion
	excSeen        map[string]bool // dedupe key: qualified field name
}

// walkSchema resolves a SchemaRef and returns the Go type expression.
// For named schemas it registers a goType; for inline schemas it returns
// a primitive or composite type string.
func (w *walker) walkSchema(ref *openapi3.SchemaRef, schemaKey, group string, isRespBody bool) string {
	if ref == nil {
		return "json.RawMessage"
	}

	if ref.Ref != "" {
		return w.walkRef(ref, schemaKey, group, isRespBody)
	}

	// Inline schema.
	if ref.Value == nil {
		return "json.RawMessage"
	}
	return w.resolveInlineSchema(ref.Value, schemaKey, group, isRespBody)
}

// walkRef resolves a $ref schema, including alias/cycle detection and the
// special-case for parent-scoped oneOf/anyOf unions.
func (w *walker) walkRef(ref *openapi3.SchemaRef, schemaKey, group string, isRespBody bool) string {
	key := refToSchemaKey(ref.Ref)
	if goType, ok := isScalarAlias(key); ok {
		return goType
	}
	if existing, ok := w.registry.lookup(key); ok {
		return existing.Name
	}
	if _, cycling := w.inFlight[key]; cycling {
		return schemaTypeName(key, false)
	}

	if resolved, ok := w.resolveParentScopedUnion(ref, schemaKey, group); ok {
		return resolved
	}

	return w.resolveNamedSchema(key, ref.Value, group, isRespBody)
}

// resolveParentScopedUnion handles the case where a $ref points to a pure
// oneOf/anyOf schema (no properties). Such a schema would otherwise be routed
// to resolveNamedSchema and registered as an empty struct (it has no
// properties to collect), so it is sent to resolveUnionType instead.
//
// By default the union is keyed by the CALLER's schemaKey so it attaches to its
// parent field context. There is one exception: if that parent-scoped key would
// derive a Go type name identical to the parent struct's own name, the union
// registers first and the parent struct is then silently dropped by the
// registry (its name is already taken), degrading the parent response to raw
// json.RawMessage. In that case the union is re-keyed by the REFERENCED
// schema's own canonical key (e.g. tasks._common___TaskInfos -> TasksTaskInfos)
// so it owns a distinct name and the parent struct survives. Every other
// parent-scoped union keeps its existing name, keeping this fix narrow.
func (w *walker) resolveParentScopedUnion(ref *openapi3.SchemaRef, schemaKey, group string) (string, bool) {
	if ref.Value == nil || len(ref.Value.Properties) != 0 {
		return "", false
	}
	if len(ref.Value.OneOf) == 0 && len(ref.Value.AnyOf) == 0 {
		return "", false
	}
	if resolvedGoType := resolveOneOfGoType(ref.Value); resolvedGoType != "" {
		return resolvedGoType, true
	}

	unionKey := schemaKey
	// schemaKey is the parent field key "<parentKey>.<field>". If the union's
	// parent-scoped Go name would equal the parent struct's own name (in either
	// its resp- or non-resp-bodied form), re-key by the referenced schema to
	// avoid the collision that would drop the parent struct.
	if dot := strings.LastIndexByte(schemaKey, '.'); dot >= 0 {
		parentKey := schemaKey[:dot]
		unionName := schemaTypeName(schemaKey, false)
		if unionName == schemaTypeName(parentKey, false) || unionName == schemaTypeName(parentKey, true) {
			if k := refToSchemaKey(ref.Ref); k != "" {
				unionKey = k
			}
		}
	}
	return w.resolveUnionType(ref.Value, unionKey, group), true
}

func (w *walker) resolveNamedSchema(key string, schema *openapi3.Schema, group string, isRespBody bool) string {
	if schema != nil && extensionBool(schema.Extensions, extGenericTypeParam) {
		return "json.RawMessage"
	}

	if got, ok := w.resolvePropertylessSchema(schema, key, group, isRespBody); ok {
		return got
	}

	name := schemaTypeName(key, isRespBody)
	shared := isSharedSchema(key)

	// Derive the owning group from the schema key itself, not the operation
	// that triggered the traversal. This prevents cross-package misrouting
	// when schema A references schema B from a different group.
	ownerGroup := group
	if g := schemaGroup(key); g != "" {
		ownerGroup = g
	}

	t := &goType{
		Name:      name,
		Pkg:       typePkg(shared, ownerGroup, w.registry),
		SchemaRef: key,
		IsResp:    isRespBody,
		IsShared:  shared,
	}

	w.inFlight[key] = struct{}{}
	defer delete(w.inFlight, key)

	if schema != nil {
		t.Fields = w.collectFields(schema, key, group, name, isRespBody)
		t.Comment = schema.Description
	}

	if registered, ok := w.registry.register(t); ok {
		return registered.Name
	}
	return name
}

func (w *walker) resolveInlineSchema(schema *openapi3.Schema, schemaKey, group string, isRespBody bool) string {
	// allOf: flatten into a single struct.
	if len(schema.AllOf) > 0 {
		return w.resolveAllOf(schema, schemaKey, group, isRespBody)
	}

	// oneOf/anyOf: use resolved type if all branches are the same primitive.
	if len(schema.OneOf) > 0 || len(schema.AnyOf) > 0 {
		if resolvedGoType := resolveOneOfGoType(schema); resolvedGoType != "" {
			return resolvedGoType
		}
		return w.resolveUnionType(schema, schemaKey, group)
	}

	if schema.Type == nil {
		if len(schema.Properties) > 0 {
			return w.resolveObjectSchema(schema, schemaKey, group, isRespBody)
		}
		return "json.RawMessage"
	}

	// Primitive types.
	if schema.Type.Is(openapi3.TypeString) {
		if name, ok := w.resolveStringEnum(schema, group); ok {
			return name
		}
		return goStringType(schema)
	}
	if schema.Type.Is(openapi3.TypeInteger) {
		return goIntType(schema)
	}
	if schema.Type.Is(openapi3.TypeNumber) {
		return goNumberType(schema)
	}
	if schema.Type.Is(openapi3.TypeBoolean) {
		return "bool"
	}

	// Array.
	if schema.Type.Is(openapi3.TypeArray) {
		elemType := "json.RawMessage"
		if schema.Items != nil {
			elemType = w.walkSchema(schema.Items, schemaKey+"Item", group, false)
		}
		return "[]" + elemType
	}

	// Object.
	if schema.Type.Is(openapi3.TypeObject) {
		return w.resolveObjectSchema(schema, schemaKey, group, isRespBody)
	}

	// OpenAPI 3.1 nullable scalar, e.g. type: ["null", "string"]. The single-type
	// checks above miss it (Type.Is needs one element); resolve the lone non-null
	// primitive so the field is typed (*string etc.) rather than json.RawMessage.
	if gt := nullablePrimitiveGoType(schema); gt != "" {
		return gt
	}

	return "json.RawMessage"
}

func (w *walker) resolveObjectSchema(schema *openapi3.Schema, schemaKey, group string, isRespBody bool) string {
	// map type: additionalProperties without named properties.
	if schema.AdditionalProperties.Schema != nil && len(schema.Properties) == 0 {
		valType := w.walkSchema(schema.AdditionalProperties.Schema, schemaKey+"Value", group, false)
		return "map[string]" + valType
	}

	// Named object with properties: register as a type.
	if len(schema.Properties) > 0 {
		name := schemaTypeName(schemaKey, isRespBody)
		shared := isSharedSchema(schemaKey)
		t := &goType{
			Name:      name,
			Pkg:       typePkg(shared, group, w.registry),
			SchemaRef: schemaKey,
			IsResp:    isRespBody,
			IsShared:  shared,
			Fields:    w.walkProperties(schema, schemaKey, group, name, isRespBody),
			Comment:   schema.Description,
		}
		if registered, ok := w.registry.register(t); ok {
			return registered.Name
		}
		return name
	}

	// Empty object or additionalProperties: true.
	if schema.AdditionalProperties.Has != nil && *schema.AdditionalProperties.Has {
		return "map[string]json.RawMessage"
	}

	return "json.RawMessage"
}

func (w *walker) resolveAllOf(schema *openapi3.Schema, schemaKey, group string, isRespBody bool) string {
	name := schemaTypeName(schemaKey, isRespBody)
	shared := isSharedSchema(schemaKey)

	t := &goType{
		Name:      name,
		Pkg:       typePkg(shared, group, w.registry),
		SchemaRef: schemaKey,
		IsResp:    isRespBody,
		IsShared:  shared,
		Comment:   schema.Description,
	}

	w.inFlight[schemaKey] = struct{}{}
	defer delete(w.inFlight, schemaKey)

	seen := make(map[string]bool)
	for _, sub := range schema.AllOf {
		if sub.Ref != "" {
			goTypeName := w.walkSchema(sub, schemaKey, group, false)
			if goTypeName != "json.RawMessage" {
				t.Fields = append(t.Fields, goField{
					GoType:  goTypeName,
					IsEmbed: true,
				})
				continue
			}
		}
		var subSchema *openapi3.Schema
		if sub.Value != nil {
			subSchema = sub.Value
		}
		if subSchema == nil {
			continue
		}
		for _, f := range w.walkProperties(subSchema, schemaKey, group, name, isRespBody) {
			if !seen[f.JSONName] {
				seen[f.JSONName] = true
				t.Fields = append(t.Fields, f)
			}
		}
	}

	if registered, ok := w.registry.register(t); ok {
		return registered.Name
	}
	return name
}

// walkProperties iterates the properties of a schema and returns the
// corresponding goField slice. parentTypeName and isRespBody are used to
// qualify version-filter exclusions for breadcrumb rendering.
func (w *walker) walkProperties(schema *openapi3.Schema, parentKey, group, parentTypeName string, isRespBody bool) []goField {
	if len(schema.Properties) == 0 {
		return nil
	}

	required := make(map[string]bool, len(schema.Required))
	for _, r := range schema.Required {
		required[r] = true
	}

	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)

	// Pre-compute Go names and resolve collisions. baseGoName strips leading
	// underscores, so a JSON field like "_score" produces the same Go name as a
	// sibling "score" (both legitimately exist in a server response: e.g.
	// CompletionSuggestOption has _score from SearchHit inlining and score from
	// the suggestion itself). A property without a leading underscore claims the
	// bare Go name; each underscore-prefixed sibling yields and takes a "Raw"
	// suffix, bumped ("Raw", "Raw2", ...) until unique so three-plus colliding
	// properties each get a distinct field.
	goNames := make(map[string]string, len(names))
	for _, name := range names {
		goNames[name] = baseGoName(name)
	}
	taken := make(map[string]struct{}, len(names))
	assign := func(name string) {
		base := goNames[name]
		candidate := base
		for suffix := 0; ; suffix++ {
			if _, dup := taken[candidate]; !dup {
				break
			}
			candidate = base + "Raw"
			if suffix > 0 {
				candidate += strconv.Itoa(suffix + 1)
			}
		}
		taken[candidate] = struct{}{}
		goNames[name] = candidate
	}
	// Non-underscore properties claim the bare name first; underscore-prefixed
	// properties disambiguate after. names is already sorted, so both passes are
	// deterministic and output is stable.
	for _, name := range names {
		if !strings.HasPrefix(name, "_") {
			assign(name)
		}
	}
	for _, name := range names {
		if strings.HasPrefix(name, "_") {
			assign(name)
		}
	}

	fields := make([]goField, 0, len(schema.Properties))
	for _, name := range names {
		propRef := schema.Properties[name]
		childKey := parentKey + "." + name
		goType := w.walkSchema(propRef, childKey, group, false)

		isRequired := required[name]
		nullable := isNullableSchema(propRef)
		isPointer := (!isRequired || nullable) && !isCollectionType(goType)
		omitEmpty := !isRequired && !nullable

		// json.RawMessage is inherently nullable (nil means absent/null),
		// so a pointer wrapper + omitempty loses null values on roundtrip.
		if goType == "json.RawMessage" {
			isPointer = false
			omitEmpty = false
		}

		if isPointer {
			goType = "*" + goType
		}

		var comment, versionAdded, versionDeprecated, deprecMsg string
		if propRef != nil && propRef.Value != nil {
			comment = propRef.Value.Description
			versionAdded = extensionString(propRef.Value.Extensions, extVersionAdded)
			versionDeprecated = extensionString(propRef.Value.Extensions, extVersionDeprecated)
			deprecMsg = extensionString(propRef.Value.Extensions, extDeprecationMessage)
		}

		vRemoved := ""
		if propRef != nil && propRef.Value != nil {
			vRemoved = extensionString(propRef.Value.Extensions, extVersionRemoved)
		}
		if !w.vrange.Includes(versionAdded, vRemoved, versionDeprecated) { //nolint:nestif // version-filter branching is intentional
			side := "(resp)"
			if !isRespBody {
				side = "(req)"
			}
			qualified := parentTypeName + "." + name + " " + side
			if !w.excSeen[qualified] {
				if w.excSeen == nil {
					w.excSeen = make(map[string]bool)
				}
				w.excSeen[qualified] = true
				if exc := w.vrange.Exclusion(qualified, versionAdded, vRemoved, versionDeprecated); exc != nil {
					w.excludedFields = append(w.excludedFields, ir.Exclusion{
						Name:    exc.Name,
						Reason:  exc.Reason,
						IsOlder: exc.IsOlder,
					})
				}
			}
			continue
		}

		fields = append(fields, goField{
			GoName:            goNames[name],
			JSONName:          name,
			GoType:            goType,
			IsPointer:         isPointer,
			OmitEmpty:         omitEmpty,
			Comment:           comment,
			VersionAdded:      versionAdded,
			VersionDeprecated: versionDeprecated,
			DeprecationMsg:    deprecMsg,
		})
	}

	return fields
}

// refToSchemaKey extracts the schema key from a $ref string.
// e.g. "#/components/schemas/_common___ShardStatistics" -> "_common___ShardStatistics"
func refToSchemaKey(ref string) string {
	const prefix = "#/components/schemas/"
	if strings.HasPrefix(ref, prefix) {
		return ref[len(prefix):]
	}
	return ""
}

// resolveSchemaAlias follows a chain of bare-$ref alias component schemas to the
// terminal (non-alias) schema key. A bare alias is a component declared as
// `Foo: {$ref: Bar}`; kin-openapi sets such a component's SchemaRef.Ref to the
// target while still resolving .Value. The walker registers the terminal type
// under its own key, so an operation whose ResponseRef is an alias key would
// miss registry.lookup and degrade the whole response to raw json.RawMessage.
// Resolving to the terminal key lets the lookup find the registered struct.
//
// Returns key unchanged if it is not an alias, is unknown, or the chain cycles.
func resolveSchemaAlias(key string, spec *openapi3.T) string {
	if spec == nil || spec.Components == nil || spec.Components.Schemas == nil {
		return key
	}
	seen := make(map[string]struct{})
	for {
		if _, cycling := seen[key]; cycling {
			return key
		}
		seen[key] = struct{}{}
		sr, ok := spec.Components.Schemas[key]
		if !ok || sr.Ref == "" {
			return key
		}
		next := refToSchemaKey(sr.Ref)
		if next == "" {
			return key
		}
		key = next
	}
}

// isSharedSchema returns true if the schema key belongs to a shared namespace
// (_common___, _common.X___, or group._common___) that should be emitted to
// types_gen.go.
func isSharedSchema(key string) bool {
	if strings.HasPrefix(key, "_common") {
		if strings.HasPrefix(key, "_common___") {
			return true
		}
		if idx := indexTripleUnderscore(key); idx >= 0 {
			return true
		}
	}
	if idx := indexTripleUnderscore(key); idx >= 0 {
		group := key[:idx]
		return strings.HasSuffix(group, "._common")
	}
	return false
}

// isCollectionType returns true if the Go type is a slice or map (should
// not get pointer treatment since nil is their zero value).
func isCollectionType(goType string) bool {
	return strings.HasPrefix(goType, "[]") || strings.HasPrefix(goType, "map[")
}

// resolveStringEnum handles a string schema that opts into typed-enum
// generation via the x-enum-name extension. When the marker is present and the
// schema carries a non-empty enum: constraint, it registers a shared
// int-backed iota enum type (named by the marker) and returns its Go name. The
// type is registered once and reused across every field that references it
// (e.g. security status across all security responses). Returns ("", false)
// when the schema does not opt in, so the caller falls back to a plain string.
func (w *walker) resolveStringEnum(schema *openapi3.Schema, group string) (string, bool) {
	name := extensionString(schema.Extensions, extEnumName)
	if name == "" {
		return "", false
	}
	if !token.IsIdentifier(name) {
		panic(fmt.Sprintf("resolveStringEnum: %s value %q is not a valid Go identifier", extEnumName, name))
	}
	values := enumStringValues(schema.Enum)
	if len(values) == 0 {
		return "", false
	}

	// Key the enum by its Go name under _common so it is shared (emitted once
	// in types_gen.go) and deduplicated across all referencing fields.
	key := "_common___" + name
	if existing, ok := w.registry.lookup(key); ok {
		// Two schemas sharing an x-enum-name must declare the same value set;
		// otherwise the second field would silently adopt the first enum's
		// values, yielding a closed-set type that rejects values it should
		// accept. Fail loudly rather than merge.
		if !slices.Equal(existing.EnumValues, values) {
			panic(fmt.Sprintf("resolveStringEnum: %s %q declared with conflicting value sets %q and %q",
				extEnumName, name, existing.EnumValues, values))
		}
		return existing.Name, true
	}

	t := &goType{
		Name:       name,
		Pkg:        typePkg(true, group, w.registry),
		SchemaRef:  key,
		IsShared:   true,
		IsEnum:     true,
		EnumValues: values,
		Comment:    schema.Description,
	}
	if registered, ok := w.registry.register(t); ok {
		return registered.Name, true
	}
	// Name collided with an existing type; fall back to plain string rather
	// than dropping to json.RawMessage.
	return "", false
}

// enumStringValues converts a schema's enum constraint to a string slice,
// preserving declaration order and skipping any non-string entries.
func enumStringValues(enum []any) []string {
	values := make([]string, 0, len(enum))
	for _, e := range enum {
		if s, ok := e.(string); ok {
			values = append(values, s)
		}
	}
	return values
}

// goStringType returns the Go type for a string schema, considering format.
func goStringType(schema *openapi3.Schema) string {
	return "string"
}

// goIntType returns the Go type for an integer schema.
func goIntType(schema *openapi3.Schema) string {
	switch schema.Format {
	case "int64":
		return "int64"
	default:
		return "int"
	}
}

// goNumberType returns the Go type for a number schema.
func goNumberType(schema *openapi3.Schema) string {
	switch schema.Format {
	case "float":
		return "float32"
	default:
		return "float64"
	}
}

// primitiveGoType returns the Go primitive type for a schema that is a simple
// scalar (string, integer, number, boolean) with no composition. Returns ""
// if the schema is not a simple primitive.
func primitiveGoType(schema *openapi3.Schema) string {
	if schema.Type == nil {
		return ""
	}
	switch {
	case schema.Type.Is(openapi3.TypeString):
		return goStringType(schema)
	case schema.Type.Is(openapi3.TypeInteger):
		return goIntType(schema)
	case schema.Type.Is(openapi3.TypeNumber):
		return goNumberType(schema)
	case schema.Type.Is(openapi3.TypeBoolean):
		return "bool"
	}
	// OpenAPI 3.1 nullable form: a type array such as ["null", "string"].
	// Type.Is requires a single-element type set, so the cases above miss it;
	// resolve the lone non-null primitive (callers apply pointer/omitempty via
	// isNullableSchema). Returns "" for non-nullable multi-type unions, which
	// remain genuine unions / json.RawMessage.
	return nullablePrimitiveGoType(schema)
}

// nullablePrimitiveGoType handles the OpenAPI 3.1 nullable spelling where a
// scalar is declared as a two-element type set {"null", <primitive>} (e.g.
// `type: ["null", "string"]`). kin-openapi's Type.Is matches only single-element
// sets, so such fields would otherwise fall through to json.RawMessage. Returns
// the Go type for the single non-null primitive, or "" if the schema is not
// exactly null + one supported primitive (arrays/objects/multi-type unions are
// left to the union/object paths).
func nullablePrimitiveGoType(schema *openapi3.Schema) string {
	// Only the exact two-element set {"null", <primitive>} is resolved here.
	// Type sets with 3+ members (e.g. ["null","string","integer"]) are genuine
	// multi-type unions with no single Go primitive, so they intentionally
	// return "" and stay json.RawMessage.
	if schema.Type == nil || len(*schema.Type) != 2 || !schema.Type.Includes(openapi3.TypeNull) {
		return ""
	}
	switch {
	case schema.Type.Includes(openapi3.TypeString):
		return goStringType(schema)
	case schema.Type.Includes(openapi3.TypeInteger):
		return goIntType(schema)
	case schema.Type.Includes(openapi3.TypeNumber):
		return goNumberType(schema)
	case schema.Type.Includes(openapi3.TypeBoolean):
		return "bool"
	}
	return ""
}

// typePkg returns the full import path for a generated type based on whether
// it is shared (emitted to types_gen.go) or operation-specific.
func typePkg(shared bool, group string, reg *typeRegistry) string {
	if shared {
		return reg.CoreImport
	}
	return importPathForPkg(group, reg.CorePkg)
}

// resolveOneOfGoType examines a oneOf/anyOf schema and returns a Go primitive
// type if all non-null branches resolve to the same type. For example, a union
// of multiple string enums returns "string". A union of string and null returns
// "string" (the nullable branch is ignored since Go uses pointer/omitempty for
// optionality). Returns "" if the branches have incompatible types.
func resolveOneOfGoType(schema *openapi3.Schema) string {
	branches := schema.OneOf
	if len(branches) == 0 {
		branches = schema.AnyOf
	}
	if len(branches) == 0 {
		return ""
	}

	var resolvedGoType string
	for _, branch := range branches {
		if branch == nil {
			continue
		}

		// Follow $ref to get the actual schema value.
		s := branch.Value
		if s == nil {
			return ""
		}

		// Skip null branches (nullable unions).
		if s.Type != nil && s.Type.Is(openapi3.TypeNull) {
			continue
		}

		goType := primitiveGoType(s)
		if goType == "" {
			return ""
		}
		if resolvedGoType == "" {
			resolvedGoType = goType
		} else if resolvedGoType != goType {
			return ""
		}
	}
	return resolvedGoType
}

// isNullableSchema returns true if the schema is a oneOf/anyOf that includes
// a "null" type branch, or uses the OpenAPI 3.1 multi-type syntax with "null",
// indicating the field is always present but may be null.
func isNullableSchema(ref *openapi3.SchemaRef) bool {
	if ref == nil || ref.Value == nil {
		return false
	}
	s := ref.Value
	if s.Type != nil && s.Type.Includes(openapi3.TypeNull) {
		return true
	}
	branches := s.OneOf
	if len(branches) == 0 {
		branches = s.AnyOf
	}
	for _, b := range branches {
		if b != nil && b.Value != nil && b.Value.Type != nil && b.Value.Type.Includes(openapi3.TypeNull) {
			return true
		}
	}
	return false
}

// collectFields gathers struct fields for a named schema. For pure-allOf
// schemas (allOf without local properties) it merges each sub-schema by
// embedding $refs and inlining inline properties. Otherwise it walks
// properties directly. parentTypeName and isRespBody are forwarded to
// walkProperties for breadcrumb qualification.
func (w *walker) collectFields(schema *openapi3.Schema, key, group, parentTypeName string, isRespBody bool) []goField {
	if len(schema.AllOf) == 0 || len(schema.Properties) != 0 {
		return w.walkProperties(schema, key, group, parentTypeName, isRespBody)
	}

	var fields []goField
	seen := make(map[string]bool)
	for _, sub := range schema.AllOf {
		if sub.Ref != "" {
			if goTypeName := w.walkSchema(sub, key, group, false); goTypeName != "json.RawMessage" {
				fields = append(fields, goField{GoType: goTypeName, IsEmbed: true})
				continue
			}
		}
		if sub.Value == nil {
			continue
		}
		for _, f := range w.walkProperties(sub.Value, key, group, parentTypeName, isRespBody) {
			if !seen[f.JSONName] {
				seen[f.JSONName] = true
				fields = append(fields, f)
			}
		}
	}
	return fields
}

// resolvePropertylessSchema returns a Go type expression for schemas that
// have no explicit properties: primitives, arrays, and additional-properties
// maps. Response bodies and allOf-merged schemas fall through (return false).
func (w *walker) resolvePropertylessSchema(schema *openapi3.Schema, key, group string, isRespBody bool) (string, bool) {
	if schema == nil || len(schema.Properties) != 0 {
		return "", false
	}
	if len(schema.AllOf) != 0 || isRespBody {
		return "", false
	}
	// A string schema carrying x-enum-name opts into a typed enum even when it
	// arrives here via a component $ref (resolveInlineSchema's inline-string
	// branch is bypassed for $ref'd schemas). Check before the plain-primitive
	// fallback so the marker is honored on either path.
	if schema.Type != nil && schema.Type.Is(openapi3.TypeString) {
		if name, ok := w.resolveStringEnum(schema, group); ok {
			return name, true
		}
	}
	if goType := primitiveGoType(schema); goType != "" {
		return goType, true
	}
	if schema.Type != nil && schema.Type.Is(openapi3.TypeArray) {
		elemType := "json.RawMessage"
		if schema.Items != nil {
			elemType = w.walkSchema(schema.Items, key+"Item", group, false)
		}
		return "[]" + elemType, true
	}
	if schema.AdditionalProperties.Schema != nil {
		valType := w.walkSchema(schema.AdditionalProperties.Schema, key+"Value", group, false)
		return "map[string]" + valType, true
	}
	if schema.AdditionalProperties.Has != nil && *schema.AdditionalProperties.Has {
		return "map[string]json.RawMessage", true
	}
	return "", false
}
