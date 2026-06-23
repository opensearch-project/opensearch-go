// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/renameio/v2/maybe"

	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/emit"
	"github.com/opensearch-project/opensearch-go/v5/cmd/osgen/ir"
)

// runAPI implements the "osgen api" subcommand. It parses flags and delegates
// to generateAPI for the actual work.
func runAPI() error {
	fs := flag.NewFlagSet("api", flag.ExitOnError)
	specPath := fs.String("spec", "", "path to OpenAPI spec YAML (single combined file)")
	groups := fs.String("groups", "", "comma-separated x-operation-group names (empty = all)")
	outDir := fs.String("out", "", "output directory for core API files (opensearchapi/)")
	pluginsDir := fs.String("plugins-out", "", "output directory for plugin files (plugins/)")
	pkg := fs.String("pkg", opensearchAPIPkgName, "Go package name for core API output")
	minVer := fs.String("min-version", versionEpoch, "minimum OpenSearch version (default operator: >=)")
	maxVer := fs.String("max-version", versionLatest, "maximum OpenSearch version (default operator: <=)")
	removeDepr := fs.String("remove-deprecated", versionEpoch,
		"treat operations deprecated at or before this version as removed (default: epoch, meaning keep all)")
	preserveOpt := fs.Bool("min-version-preserve-optional", false,
		"keep version-gated fields as pointers even when min-version guarantees their presence")
	bcOpsFlag := fs.String("version-breadcrumb-operations", breadcrumbModeAll, "emit comments for excluded operations: all, older, newer")
	bcTypesFlag := fs.String("version-breadcrumb-types", breadcrumbModeAll, "emit comments for excluded types: all, older, newer")
	bcFieldsFlag := fs.String("version-breadcrumb-fields", breadcrumbModeAll, "emit comments for excluded struct fields: all, older, newer")
	bcPathsFlag := fs.String("version-breadcrumb-paths", breadcrumbModeAll, "emit comments for excluded path builders: all, older, newer")
	bcParamsFlag := fs.String("version-breadcrumb-params", breadcrumbModeAll, "emit comments for excluded query parameters: all, older, newer")
	emitV4Compat := fs.Bool("emit-v4-compat", true,
		"emit backward-compatibility forwarder methods (e.g. top-level Client.Bulk forwarding to Doc.Bulk)")
	emitV4Deprecation := fs.Bool("emit-v4-deprecation", false,
		"mark the v4 compatibility forwarders with a Deprecated doc comment (requires -emit-v4-compat)")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}

	if *specPath == "" || *outDir == "" {
		return fmt.Errorf("usage: osgen api -spec <openapi-spec.yaml> -out <dir/> [-pkg <name>] -plugins-out <plugins/>")
	}

	var filter map[string]bool
	if *groups != "" {
		filter = make(map[string]bool)
		for g := range strings.SplitSeq(*groups, ",") {
			filter[strings.TrimSpace(g)] = true
		}
	}

	vrange, err := ParseVersionRange(*minVer, *maxVer, *removeDepr, *preserveOpt)
	if err != nil {
		return err
	}

	bc, err := parseBreadcrumbFlags(*bcOpsFlag, *bcTypesFlag, *bcFieldsFlag, *bcPathsFlag, *bcParamsFlag)
	if err != nil {
		return err
	}

	return generateAPI(*specPath, filter, *outDir, *pluginsDir, *pkg, vrange, bc,
		CompatConfig{V4Compat: *emitV4Compat, V4Deprecation: *emitV4Deprecation})
}

// generateAPI uses the two-phase pipeline (Parse -> IR -> Emit -> Targets).
//
// bc filters which version-range exclusions render as breadcrumb comments.
// Operations, query params, and struct fields are tracked. bc.Types is
// rejected as unsupported because the OpenSearch spec does not currently
// attach x-version-* extensions to component schemas as a whole; honoring
// the flag would always emit an empty set, which is precisely the silent
// no-op behavior we are avoiding.
func generateAPI(
	specPath string,
	filter map[string]bool,
	outDir, pluginsDir, corePkg string,
	vrange VersionRange,
	bc BreadcrumbConfig,
	compat CompatConfig,
) error {
	if bc.Types != BreadcrumbAll {
		return fmt.Errorf("--version-breadcrumb-types is not implemented for `osgen api`: " +
			"the spec attaches x-version-* extensions to operations, parameters, and properties, " +
			"but not to component schemas as wholes, so this flag would always produce an empty set")
	}

	ops, spec, opExclusions, paramExclusions, err := extractOperations(specPath, filter, vrange)
	if err != nil {
		return err
	}

	registry := newTypeRegistry(corePkg)
	respFieldExc := populateResponseTypes(ops, spec, registry, vrange)
	reqFieldExc := populateRequestBodyTypes(ops, spec, registry, vrange)
	reportCollisions(os.Stderr, registry)
	fieldExclusions := append(respFieldExc, reqFieldExc...) //nolint:gocritic // intentional concat into new slice
	sort.Slice(fieldExclusions, func(i, j int) bool { return fieldExclusions[i].Name < fieldExclusions[j].Name })

	irSpec := convertToIR(ops, registry)
	irSpec.Exclusions = ir.Exclusions{
		Operations: filterExclusions(opExclusions, bc.Operations),
		Fields:     filterExclusions(fieldExclusions, bc.Fields),
		Params:     filterExclusions(paramExclusions, bc.Params),
	}

	// Apply the v4 compatibility-forwarder policy before sub-client filtering so
	// dropped forwarders don't keep an otherwise-dead sub-client alive.
	applyCompatPolicy(irSpec.Operations, compat)

	// Emit only the sub-clients at least one operation routes to (with their
	// ancestors), so dead fields never reach clients_gen.go.
	hierarchy := filterSubClients(usedSubClientTypes(irSpec.Operations))
	subClients := make([]emit.SubClient, len(hierarchy))
	for i, sc := range hierarchy {
		subClients[i] = emit.SubClient{
			TypeName:  sc.TypeName,
			FieldName: sc.FieldName,
			Parent:    sc.Parent,
			Aliases:   sc.Aliases,
		}
	}

	pluginSC := buildPluginSubClients(irSpec)

	cfg := emit.BuildConfig{
		OutDir:           outDir,
		PluginsDir:       pluginsDir,
		CorePkg:          corePkg,
		ModulePath:       modulePath,
		SubClients:       subClients,
		PluginSubClients: pluginSC,
	}

	targets := emit.Build(irSpec, cfg)

	// Render and write targets in parallel. Each target writes to a unique
	// path, so workers can both render and write without coordination beyond
	// the shared written-set, log slice, and first-error capture.
	written := make(map[string]struct{}, len(targets))
	var (
		mu       sync.Mutex
		writeLog []string
		wrote    int
		firstErr error
	)
	setErr := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
	}
	jobs := make(chan emit.Target, len(targets))
	for _, t := range targets {
		jobs <- t
	}
	close(jobs)

	workerCount := min(runtime.NumCPU(), len(targets))
	var wg sync.WaitGroup
	for range workerCount {
		wg.Go(func() {
			for t := range jobs {
				absPath, err := filepath.Abs(t.Path())
				if err != nil {
					setErr(fmt.Errorf("resolving path: %w", err))
					continue
				}
				dir := filepath.Dir(absPath)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					setErr(fmt.Errorf("creating %q: %w", dir, err))
					continue
				}
				src, err := t.Render()
				if err != nil {
					setErr(fmt.Errorf("render %q: %w", absPath, err))
					continue
				}
				changed, werr := writeIfChanged(absPath, src)
				if werr != nil {
					setErr(werr)
					continue
				}
				mu.Lock()
				written[absPath] = struct{}{}
				if changed {
					writeLog = append(writeLog, repoRelPath(absPath))
					wrote++
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}

	sort.Strings(writeLog)
	for _, p := range writeLog {
		fmt.Fprintf(os.Stderr, "  -> %s\n", p)
	}

	removed, err := removeStaleGenFiles(outDir, written)
	if err != nil {
		return err
	}
	if pluginsDir != "" {
		n, err := removeStaleGenFiles(pluginsDir, written)
		if err != nil {
			return err
		}
		removed += n
	}

	fmt.Fprintf(os.Stderr, "generated %d operations (%d files written, %d stale removed)\n", len(irSpec.Operations), wrote, removed)
	return nil
}

// buildPluginSubClients computes sub-client hierarchies for all plugin packages
// from the IR spec. Returns a map keyed by plugin package name, where each
// value maps operation group to its *PluginSubClient (nil for root Client ops).
func buildPluginSubClients(spec *ir.Spec) map[string]map[string]*emit.PluginSubClient {
	// Group plugin operations by package.
	pluginGroups := make(map[string][]string)
	for _, op := range spec.Operations {
		if !op.IsPlugin {
			continue
		}
		prefix := groupPrefix(op.Group)
		pluginGroups[prefix] = append(pluginGroups[prefix], op.Group)
	}

	result := make(map[string]map[string]*emit.PluginSubClient, len(pluginGroups))
	for pkg, groups := range pluginGroups {
		r := resolvePluginSubClients(groups)

		byGroup := make(map[string]*emit.PluginSubClient, len(r.Assignment))
		// Allocate emit-side sub-clients and build a lookup by field name.
		emitSCs := make(map[string]*emit.PluginSubClient, len(r.SubClients))
		for _, sc := range r.SubClients {
			psc := &emit.PluginSubClient{
				TypeName:  sc.TypeName,
				FieldName: sc.FieldName,
			}
			emitSCs[sc.FieldName] = psc
		}

		for group, fieldName := range r.Assignment {
			byGroup[group] = emitSCs[fieldName]
		}
		result[pkg] = byGroup
	}
	return result
}

// removeStaleGenFiles removes *_gen.go files under root that are not in
// the written set. It resolves root, verifies it is inside the git working
// tree, and uses os.OpenRoot to confine removal.
func removeStaleGenFiles(root string, written map[string]struct{}) (int, error) {
	abs, err := resolveGenRoot(root)
	if err != nil {
		return 0, err
	}

	dir, err := os.OpenRoot(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("opening root %q: %w", abs, err)
	}
	defer dir.Close()

	var removed int
	err = fs.WalkDir(dir.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(d.Name(), genFileSuffix) {
			return nil
		}
		full := filepath.Join(abs, path)
		if _, ok := written[full]; ok {
			return nil
		}
		if err := dir.Remove(path); err != nil {
			return fmt.Errorf("removing stale %q: %w", path, err)
		}
		fmt.Fprintf(os.Stderr, "  removed stale %s\n", repoRelPath(full))
		removed++
		return nil
	})
	return removed, err
}

// repoRoot returns the absolute path of the git working tree root.
// Tests may swap the implementation via the repoRoot var.
//
//nolint:gochecknoglobals // function var swapped by tests
var repoRoot = repoRootGit

func repoRootGit() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not inside a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// resolveGenRoot cleans and resolves root to an absolute path, then verifies
// it is a subdirectory of the git working tree (unless OSGEN_SKIP_GIT_CHECK
// is set). Returns an error if the path is the filesystem root, the user's
// home directory, or outside the repo.
func resolveGenRoot(root string) (string, error) {
	cleaned := filepath.Clean(root)
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolving %q: %w", root, err)
	}

	if abs == "/" || abs == filepath.Dir(abs) {
		return "", fmt.Errorf("refusing to operate on filesystem root %q", abs)
	}
	if home, err := os.UserHomeDir(); err == nil && abs == home {
		return "", fmt.Errorf("refusing to operate on home directory %q", abs)
	}

	if !skipGitCheck() {
		gitTop, err := repoRoot()
		if err != nil {
			return "", fmt.Errorf("%w (set %s=1 to bypass)", err, envSkipGitCheck)
		}
		if abs != gitTop && !strings.HasPrefix(abs, gitTop+string(filepath.Separator)) {
			return "", fmt.Errorf("refusing to operate on %q: outside git root %q (set %s=1 to bypass)", abs, gitTop, envSkipGitCheck)
		}
	}

	return abs, nil
}

// skipGitCheck returns true when envSkipGitCheck is set to a truthy value
// (per strconv.ParseBool: 1, t, true, yes, on).
func skipGitCheck() bool {
	v, err := strconv.ParseBool(os.Getenv(envSkipGitCheck))
	return err == nil && v
}

// writeIfChanged compares data against the existing file at path. If the
// content differs (or the file does not exist), it writes data and returns
// true. If the content is identical, it returns false without touching the
// file. The write is atomic on Unix via renameio; on Windows it falls back
// to os.WriteFile, which is non-atomic but acceptable for one-shot codegen.
func writeIfChanged(path string, data []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, data) {
		return false, nil
	}
	if err := maybe.WriteFile(path, data, 0o600); err != nil {
		return false, fmt.Errorf("writing %q: %w", path, err)
	}
	return true, nil
}

// repoRelPath returns path relative to the git working tree root for
// display purposes. Falls back to the absolute path if the root is
// unavailable or the path is not under it.
func repoRelPath(abs string) string {
	root, err := repoRoot()
	if err != nil {
		return abs
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return abs
	}
	return rel
}

// populateResponseTypes walks response schemas for all operations, registering
// types in the registry. It then populates each operation's RespFields and
// SiblingTypes from the registry.
func populateResponseTypes(ops []apiOperation, spec *openapi3.T, registry *typeRegistry, vrange VersionRange) []ir.Exclusion {
	if spec == nil || spec.Components == nil || spec.Components.Schemas == nil {
		return nil
	}

	w := &walker{
		registry: registry,
		spec:     spec,
		inFlight: make(map[string]struct{}),
		vrange:   vrange,
	}

	// Walk all response schemas to build the full type registry.
	for i := range ops {
		ref := ops[i].ResponseRef
		if ref == "" {
			continue
		}
		schemaRef, ok := spec.Components.Schemas[ref]
		if !ok {
			// Inline response schema (defined in components/responses, not
			// components/schemas). Use the resolved SchemaRef directly.
			if ops[i].ResponseSchemaRef != nil {
				w.walkSchema(ops[i].ResponseSchemaRef, ref, ops[i].Group, true)
			}
			continue
		}
		w.walkSchema(schemaRef, ref, ops[i].Group, true)
	}

	// Populate each operation's RespFields and SiblingTypes.
	// First, identify types used by multiple operations and promote them to
	// shared so they are emitted once in types_gen.go instead of duplicated.
	groupPkgs := make(map[string]string, len(ops))
	for _, op := range ops {
		prefix := groupPrefix(op.Group)
		if coreGroups[prefix] {
			groupPkgs[op.Group] = opensearchAPIPkgName
		} else {
			groupPkgs[op.Group] = prefix
		}
	}
	promoteMultiUseTypes(ops, registry, groupPkgs)

	// Promote types transitively referenced by shared types to shared.
	registry.promoteSharedDeps()

	// Track which types have already been assigned to an operation's SiblingTypes
	// to avoid emitting the same type in multiple files within the same package.
	claimed := make(map[string]bool)

	for i := range ops {
		ref := ops[i].ResponseRef
		if ref == "" {
			continue
		}
		respType, ok := registry.lookup(ref)

		if ok && respType.IsUnion {
			// Response is a union type (oneOf/anyOf at the response level).
			// Use RespShapeRaw so the Resp stores raw JSON and round-trips
			// perfectly; users access the decoded union via sibling types.
			classifyRespUnionAsRaw(&ops[i], registry, claimed)
			continue
		}

		if !ok {
			classifyUnregisteredResp(&ops[i], spec, registry, claimed)
			continue
		}
		ops[i].RespFields = respType.Fields

		// Collect non-shared sibling types reachable from this response.
		for _, st := range registry.reachableFrom(ref) {
			if !claimed[st.SchemaRef] {
				ops[i].SiblingTypes = append(ops[i].SiblingTypes, st)
				claimed[st.SchemaRef] = true
			}
		}
	}
	return w.excludedFields
}

// classifyUnregisteredResp handles operations whose Resp struct is not in the
// registry: classifies the response shape (map/array/raw) and gathers the
// sibling types that belong with it.
func classifyUnregisteredResp(op *apiOperation, spec *openapi3.T, registry *typeRegistry, claimed map[string]bool) {
	classifyRespShape(op, spec, registry)

	for _, st := range registry.forOperation(op.Group) {
		if !st.IsResp && !st.IsShared && !claimed[st.SchemaRef] {
			op.SiblingTypes = append(op.SiblingTypes, st)
			claimed[st.SchemaRef] = true
		}
	}

	if op.RespElemType == nil || op.RespElemType.IsShared {
		return
	}
	et := op.RespElemType
	if !claimed[et.SchemaRef] {
		op.SiblingTypes = append(op.SiblingTypes, et)
		claimed[et.SchemaRef] = true
	}
	for _, dep := range registry.reachableFrom(et.SchemaRef) {
		if !claimed[dep.SchemaRef] {
			op.SiblingTypes = append(op.SiblingTypes, dep)
			claimed[dep.SchemaRef] = true
		}
	}
}

// populateRequestBodyTypes walks request body schemas for all operations,
// registering types in the shared registry. It then populates each operation's
// ReqBodyFields, ReqBodySiblings, and HasTypedBody. Returns request-body
// field exclusions produced by the version-range filter so the caller can
// emit breadcrumb comments.
func populateRequestBodyTypes(ops []apiOperation, spec *openapi3.T, registry *typeRegistry, vrange VersionRange) []ir.Exclusion {
	if spec == nil || spec.Components == nil {
		return nil
	}

	w := &walker{
		registry: registry,
		spec:     spec,
		inFlight: make(map[string]struct{}),
		vrange:   vrange,
	}

	// Walk all request body schemas to register types.
	for i := range ops {
		ref := ops[i].RequestRef
		if ref == "" {
			continue
		}
		if schemaRef, ok := spec.Components.Schemas[ref]; ok {
			w.walkSchema(schemaRef, ref, ops[i].Group, false)
			continue
		}
		if ops[i].RequestSchemaRef != nil {
			w.walkSchema(ops[i].RequestSchemaRef, ref, ops[i].Group, false)
		}
	}

	// Build a set of schema refs already claimed by response siblings so we
	// don't emit the same type twice in the same file.
	respClaimed := make(map[string]bool)
	for _, op := range ops {
		for _, st := range op.SiblingTypes {
			respClaimed[st.SchemaRef] = true
		}
	}

	// Track types claimed across request body operations to avoid emitting
	// the same type in multiple files within the same package.
	claimed := make(map[string]bool)

	for i := range ops {
		ref := ops[i].RequestRef
		if ref == "" {
			continue
		}
		bodyType, ok := registry.lookup(ref)
		if !ok {
			continue
		}

		// Bare objects (no fields, not a union) stay as io.Reader.
		if len(bodyType.Fields) == 0 && !bodyType.IsUnion {
			ops[i].RequestRef = ""
			continue
		}

		ops[i].ReqBodyFields = bodyType.Fields
		ops[i].HasTypedBody = true
		ops[i].ReqBodyTypeName = bodyType.Name
		ops[i].ReqBodyIsShared = bodyType.IsShared

		// Include the body type itself when it's local and unclaimed.
		if !bodyType.IsShared && !respClaimed[bodyType.SchemaRef] && !claimed[bodyType.SchemaRef] {
			ops[i].ReqBodySiblings = append(ops[i].ReqBodySiblings, bodyType)
			claimed[bodyType.SchemaRef] = true
		}

		for _, st := range registry.reachableFrom(ref) {
			if !st.IsShared && !respClaimed[st.SchemaRef] && !claimed[st.SchemaRef] {
				ops[i].ReqBodySiblings = append(ops[i].ReqBodySiblings, st)
				claimed[st.SchemaRef] = true
			}
		}
	}
	return w.excludedFields
}

// classifyRespShape determines the response body shape for operations whose
// response schema didn't produce a registered struct type. It inspects the
// raw schema to detect map, array, or empty patterns and resolves the element
// type from the registry.
func classifyRespShape(op *apiOperation, spec *openapi3.T, registry *typeRegistry) {
	schema := resolveResponseSchema(op, spec)
	if schema == nil {
		op.RespShape = ir.RespShapeRaw
		return
	}

	// Array: type=array with items.
	if schema.Type != nil && schema.Type.Is("array") {
		op.RespShape = ir.RespShapeArray
		if schema.Items != nil {
			op.RespElemType = resolveElemType(schema.Items, registry)
		}
		return
	}

	// Map: type=object (or untyped) with additionalProperties and no named properties.
	if schema.AdditionalProperties.Schema != nil && len(schema.Properties) == 0 {
		op.RespShape = ir.RespShapeMap
		op.RespElemType = resolveElemType(schema.AdditionalProperties.Schema, registry)
		return
	}

	// Empty schema or bare type=object with no properties.
	op.RespShape = ir.RespShapeRaw
}

// classifyRespUnionAsRaw handles response schemas that resolved to a union type.
// The Resp struct stores raw JSON (RespShapeRaw) for perfect round-trip fidelity,
// and the union type is included as a sibling so callers can decode on demand.
func classifyRespUnionAsRaw(op *apiOperation, registry *typeRegistry, claimed map[string]bool) {
	op.RespShape = ir.RespShapeRaw
	for _, st := range registry.forOperation(op.Group) {
		if !st.IsResp && !st.IsShared && !claimed[st.SchemaRef] {
			op.SiblingTypes = append(op.SiblingTypes, st)
			claimed[st.SchemaRef] = true
		}
	}
	// Pull in types reachable from the response union (branch types may
	// belong to a different schema group).
	for _, dep := range registry.reachableFrom(op.ResponseRef) {
		if !dep.IsShared && !claimed[dep.SchemaRef] {
			op.SiblingTypes = append(op.SiblingTypes, dep)
			claimed[dep.SchemaRef] = true
		}
	}
}

// resolveResponseSchema finds the underlying *openapi3.Schema for an operation's
// 200 response body. Returns nil if the schema can't be resolved.
func resolveResponseSchema(op *apiOperation, spec *openapi3.T) *openapi3.Schema {
	if op.ResponseSchemaRef != nil {
		if op.ResponseSchemaRef.Value != nil {
			return op.ResponseSchemaRef.Value
		}
		if op.ResponseSchemaRef.Ref != "" {
			key := refToSchemaKey(op.ResponseSchemaRef.Ref)
			if s, ok := spec.Components.Schemas[key]; ok && s.Value != nil {
				return s.Value
			}
		}
	}
	if spec.Components != nil && spec.Components.Schemas != nil {
		if s, ok := spec.Components.Schemas[op.ResponseRef]; ok && s.Value != nil {
			return s.Value
		}
	}
	return nil
}

// resolveElemType looks up the element type (for map values or array items) in
// the registry by following the $ref. Returns nil if the type isn't registered.
func resolveElemType(ref *openapi3.SchemaRef, registry *typeRegistry) *goType {
	if ref == nil {
		return nil
	}
	if ref.Ref != "" {
		key := refToSchemaKey(ref.Ref)
		if t, ok := registry.lookup(key); ok {
			return t
		}
		// Try by derived name.
		name := schemaTypeName(key, false)
		if t, ok := registry.lookupByName(name); ok {
			return t
		}
	}
	return nil
}

type typeUsage struct {
	groups map[string]struct{}
	pkgs   map[string]struct{}
	pkg    string
}

// promoteMultiUseTypes marks non-Resp types as shared when they are
// referenced by more than one operation group across different packages.
// This ensures types like SearchHitsMetadata (from _core.search) that are
// also used by scroll get emitted to types_gen.go rather than duplicated.
// Types shared within the same package don't need promotion since all files
// in a Go package can access each other's declarations.
func promoteMultiUseTypes(ops []apiOperation, registry *typeRegistry, groupPkgs map[string]string) {
	uses := make(map[string]*typeUsage)

	for _, op := range ops {
		ref := op.ResponseRef
		if ref == "" {
			continue
		}
		respType, ok := registry.lookup(ref)
		if !ok {
			// No registered Resp struct (e.g. array-typed responses).
			// Still collect types associated with this group.
			for _, st := range registry.forOperation(op.Group) {
				collectTypeRefs(op.Group, st.SchemaRef, registry, uses, groupPkgs)
			}
			continue
		}
		// Collect from the response type's direct fields.
		for _, f := range respType.Fields {
			typeName := unwrapTypeName(f.GoType)
			if child, ok := registry.lookupByName(typeName); ok {
				collectTypeRefs(op.Group, child.SchemaRef, registry, uses, groupPkgs)
			}
		}
		// Also collect from union branches of the response type.
		for _, b := range respType.Branches {
			typeName := unwrapTypeName(b.GoType)
			if child, ok := registry.lookupByName(typeName); ok {
				collectTypeRefs(op.Group, child.SchemaRef, registry, uses, groupPkgs)
			}
		}
	}

	for ref, u := range uses {
		if len(u.pkgs) <= 1 {
			continue
		}
		t, ok := registry.lookup(ref)
		if !ok || t.IsResp || t.IsShared {
			continue
		}
		t.IsShared = true
		t.Pkg = u.pkg
	}
}

// collectTypeRefs recursively finds all type refs used by an operation's
// response schema and records which operation groups use each type.
func collectTypeRefs(group, ref string, registry *typeRegistry, uses map[string]*typeUsage, groupPkgs map[string]string) {
	t, ok := registry.lookup(ref)
	if !ok || t.IsResp {
		return
	}
	u, exists := uses[ref]
	if !exists {
		u = &typeUsage{groups: make(map[string]struct{}), pkgs: make(map[string]struct{}), pkg: t.Pkg}
		uses[ref] = u
	}
	if _, seen := u.groups[group]; seen {
		return
	}
	u.groups[group] = struct{}{}
	if pkg, ok := groupPkgs[group]; ok {
		u.pkgs[pkg] = struct{}{}
	}

	for _, f := range t.Fields {
		typeName := unwrapTypeName(f.GoType)
		if child, ok := registry.lookupByName(typeName); ok {
			collectTypeRefs(group, child.SchemaRef, registry, uses, groupPkgs)
		}
	}
	for _, b := range t.Branches {
		typeName := unwrapTypeName(b.GoType)
		if child, ok := registry.lookupByName(typeName); ok {
			collectTypeRefs(group, child.SchemaRef, registry, uses, groupPkgs)
		}
	}
}

// unwrapTypeName strips pointer, slice, and map wrappers to get the base type name.
func unwrapTypeName(goType string) string {
	for {
		prev := goType
		for strings.HasPrefix(goType, "*") {
			goType = goType[1:]
		}
		for strings.HasPrefix(goType, "[]") {
			goType = goType[2:]
		}
		for strings.HasPrefix(goType, "map[") {
			if idx := strings.Index(goType, "]"); idx >= 0 {
				goType = goType[idx+1:]
			} else {
				break
			}
		}
		if goType == prev {
			break
		}
	}
	return goType
}
