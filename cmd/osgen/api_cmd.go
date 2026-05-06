// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package main

import (
	"bytes"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/google/renameio/v2"
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
	fs.Parse(os.Args[1:])

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

	return generateAPI(*specPath, filter, *outDir, *pluginsDir, *pkg)
}

// generateAPI extracts operations from the spec and writes Req/Params/Resp
// files. This is the testable core of the "api" subcommand.
func generateAPI(specPath string, filter map[string]bool, outDir, pluginsDir, corePkg string) error {
	ops, spec, err := extractOperations(specPath, filter)
	if err != nil {
		return err
	}

	// Walk response schemas to populate typed response fields.
	registry := newTypeRegistry(corePkg)
	populateResponseTypes(ops, spec, registry)

	// Track every file we write so we can remove stale ones afterward.
	written := make(map[string]struct{})

	var wrote int
	for _, op := range ops {
		routePkg, dir := routeOperation(op.Group, outDir, pluginsDir)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %q: %w", dir, err)
		}

		basename := operationFilename(op.Group)

		apiFile, err := filepath.Abs(filepath.Join(dir, basename+genFileSuffix))
		if err != nil {
			return fmt.Errorf("resolving path: %w", err)
		}
		filePkg := routePkg
		if filePkg == opensearchAPIPkgName {
			filePkg = corePkg
		}
		apiSrc, err := renderAPIFile(op, filePkg, registry)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render %q: %v\n", op.Group, err)
			continue
		}
		if changed, werr := writeIfChanged(apiFile, []byte(apiSrc)); werr != nil {
			return werr
		} else if changed {
			fmt.Fprintf(os.Stderr, "  %q -> %s\n", op.Group, repoRelPath(apiFile))
			wrote++
		}
		written[apiFile] = struct{}{}

		// Generate params test (white-box, same package).
		paramsTestFile, err := filepath.Abs(filepath.Join(dir, basename+"_internal"+genTestFileSuffix))
		if err != nil {
			return fmt.Errorf("resolving test path: %w", err)
		}
		paramsSrc, err := renderParamsTest(op, filePkg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render params test %q: %v\n", op.Group, err)
		} else if paramsSrc != "" {
			if changed, werr := writeIfChanged(paramsTestFile, []byte(paramsSrc)); werr != nil {
				return werr
			} else if changed {
				wrote++
			}
			written[paramsTestFile] = struct{}{}
		}

		// Generate GetRequest test (black-box, external test package).
		reqTestFile, err := filepath.Abs(filepath.Join(dir, basename+genTestFileSuffix))
		if err != nil {
			return fmt.Errorf("resolving req test path: %w", err)
		}
		fileImport := importPathForPkg(op.Group, corePkg)
		reqSrc, err := renderReqTest(op, filePkg, fileImport)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render req test %q: %v\n", op.Group, err)
		} else if reqSrc != "" {
			if changed, werr := writeIfChanged(reqTestFile, []byte(reqSrc)); werr != nil {
				return werr
			} else if changed {
				wrote++
			}
			written[reqTestFile] = struct{}{}
		}

		// Generate integration test (black-box, external test package).
		integTestFile, err := filepath.Abs(filepath.Join(dir, basename+"_integ"+genTestFileSuffix))
		if err != nil {
			return fmt.Errorf("resolving integ test path: %w", err)
		}
		integSrc, err := renderIntegTest(op, filePkg, fileImport, corePkg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render integ test %q: %v\n", op.Group, err)
		} else if integSrc != "" {
			if changed, werr := writeIfChanged(integTestFile, []byte(integSrc)); werr != nil {
				return werr
			} else if changed {
				wrote++
			}
			written[integTestFile] = struct{}{}
		}
	}

	// Generate compat.go for each plugin package.
	type pluginInfo struct {
		dir         string
		hasDuration bool
		ops         []apiOperation
	}
	pluginPkgs := make(map[string]*pluginInfo)
	for _, op := range ops {
		pkg, dir := routeOperation(op.Group, outDir, pluginsDir)
		if pkg == opensearchAPIPkgName || pkg == corePkg {
			continue
		}
		pi, ok := pluginPkgs[pkg]
		if !ok {
			pi = &pluginInfo{dir: dir}
			pluginPkgs[pkg] = pi
		}
		pi.ops = append(pi.ops, op)
		if !pi.hasDuration {
			for _, p := range op.QueryParams {
				if p.IsDuration {
					pi.hasDuration = true
					break
				}
			}
		}
	}
	for pkg, pi := range pluginPkgs {
		compatFile, err := filepath.Abs(filepath.Join(pi.dir, "compat"+genFileSuffix))
		if err != nil {
			return fmt.Errorf("resolving path: %w", err)
		}
		src, err := renderCompatFile(pkg, pi.hasDuration)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render compat %q: %v\n", pkg, err)
			continue
		}
		if changed, werr := writeIfChanged(compatFile, []byte(src)); werr != nil {
			return fmt.Errorf("writing %q: %w", compatFile, werr)
		} else if changed {
			fmt.Fprintf(os.Stderr, "  compat -> %s\n", repoRelPath(compatFile))
			wrote++
		}
		written[compatFile] = struct{}{}

		// Generate client_gen.go for each plugin package.
		clientFile, err := filepath.Abs(filepath.Join(pi.dir, "client"+genFileSuffix))
		if err != nil {
			return fmt.Errorf("resolving plugin client path: %w", err)
		}
		clientSrc, err := renderPluginClientFile(pkg, pi.ops)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render plugin client %q: %v\n", pkg, err)
		} else {
			if changed, werr := writeIfChanged(clientFile, []byte(clientSrc)); werr != nil {
				return fmt.Errorf("writing %q: %w", clientFile, werr)
			} else if changed {
				fmt.Fprintf(os.Stderr, "  plugin client -> %s\n", repoRelPath(clientFile))
				wrote++
			}
			written[clientFile] = struct{}{}
		}

		// Generate internal/test/helpers_gen.go for each plugin package.
		testDir := filepath.Join(pi.dir, "internal", "test")
		if err := os.MkdirAll(testDir, 0o755); err != nil {
			return fmt.Errorf("creating %q: %w", testDir, err)
		}
		testHelperFile, err := filepath.Abs(filepath.Join(testDir, "helpers"+genFileSuffix))
		if err != nil {
			return fmt.Errorf("resolving plugin test helper path: %w", err)
		}
		pluginImport := importPathForPkg(pi.ops[0].Group, corePkg)
		coreImport := modulePath + "/" + corePkg
		testHelperSrc, err := renderPluginTestHelper(pkg, pluginImport, coreImport)
		if err != nil {
			fmt.Fprintf(os.Stderr, "render plugin test helper %q: %v\n", pkg, err)
		} else {
			if changed, werr := writeIfChanged(testHelperFile, []byte(testHelperSrc)); werr != nil {
				return fmt.Errorf("writing %q: %w", testHelperFile, werr)
			} else if changed {
				fmt.Fprintf(os.Stderr, "  plugin test helper -> %s\n", repoRelPath(testHelperFile))
				wrote++
			}
			written[testHelperFile] = struct{}{}
		}
	}

	// Generate types_gen.go for shared types in the core package.
	sharedTypes := registry.shared()
	if len(sharedTypes) > 0 {
		// Partition into struct types and union types.
		var structTypes, unionTypes []*goType
		for _, t := range sharedTypes {
			if t.IsUnion {
				unionTypes = append(unionTypes, t)
			} else {
				structTypes = append(structTypes, t)
			}
		}

		if len(structTypes) > 0 {
			typesFile, err := filepath.Abs(filepath.Join(outDir, "types"+genFileSuffix))
			if err != nil {
				return fmt.Errorf("resolving types path: %w", err)
			}
			src, err := renderSharedTypesFile(structTypes, corePkg)
			if err != nil {
				return fmt.Errorf("render shared types: %w", err)
			}
			if src != "" {
				if changed, werr := writeIfChanged(typesFile, []byte(src)); werr != nil {
					return fmt.Errorf("writing %q: %w", typesFile, werr)
				} else if changed {
					fmt.Fprintf(os.Stderr, "  shared types -> %s\n", repoRelPath(typesFile))
					wrote++
				}
				written[typesFile] = struct{}{}
			}
		}

		if len(unionTypes) > 0 {
			unionsFile, err := filepath.Abs(filepath.Join(outDir, "unions"+genFileSuffix))
			if err != nil {
				return fmt.Errorf("resolving unions path: %w", err)
			}
			src, err := renderUnionTypesFile(unionTypes, corePkg)
			if err != nil {
				return fmt.Errorf("render union types: %w", err)
			}
			if src != "" {
				if changed, werr := writeIfChanged(unionsFile, []byte(src)); werr != nil {
					return fmt.Errorf("writing %q: %w", unionsFile, werr)
				} else if changed {
					fmt.Fprintf(os.Stderr, "  union types -> %s\n", repoRelPath(unionsFile))
					wrote++
				}
				written[unionsFile] = struct{}{}
			}
		}
	}

	// Generate clients_gen.go for Client struct and sub-client types.
	clientsFile, err := filepath.Abs(filepath.Join(outDir, "clients"+genFileSuffix))
	if err != nil {
		return fmt.Errorf("resolving clients path: %w", err)
	}
	clientsSrc, err := renderClientsFile(corePkg)
	if err != nil {
		return fmt.Errorf("render clients: %w", err)
	}
	if changed, werr := writeIfChanged(clientsFile, []byte(clientsSrc)); werr != nil {
		return fmt.Errorf("writing %q: %w", clientsFile, werr)
	} else if changed {
		fmt.Fprintf(os.Stderr, "  clients -> %s\n", repoRelPath(clientsFile))
		wrote++
	}
	written[clientsFile] = struct{}{}

	// Generate dispatch_gen_test.go for compile-time signature assertions.
	coreOps := make([]apiOperation, 0, len(ops))
	for _, op := range ops {
		if len(op.DispatchRoutes) > 0 {
			coreOps = append(coreOps, op)
		}
	}
	if len(coreOps) > 0 {
		dispatchTestFile, err := filepath.Abs(filepath.Join(outDir, "dispatch"+genTestFileSuffix))
		if err != nil {
			return fmt.Errorf("resolving dispatch test path: %w", err)
		}
		coreImport := modulePath + "/" + corePkg
		dispatchSrc, err := renderDispatchTest(coreOps, corePkg, coreImport)
		if err != nil {
			return fmt.Errorf("render dispatch test: %w", err)
		}
		if dispatchSrc != "" {
			if changed, werr := writeIfChanged(dispatchTestFile, []byte(dispatchSrc)); werr != nil {
				return fmt.Errorf("writing %q: %w", dispatchTestFile, werr)
			} else if changed {
				fmt.Fprintf(os.Stderr, "  dispatch test -> %s\n", repoRelPath(dispatchTestFile))
				wrote++
			}
			written[dispatchTestFile] = struct{}{}
		}
	}

	// Remove stale _gen.go files not produced by this run.
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

	fmt.Fprintf(os.Stderr, "generated %d operations (%d files written, %d stale removed)\n", len(ops), wrote, removed)
	return nil
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

var (
	gitTopOnce  sync.Once
	gitTopValue string
	gitTopErr   error
)

// repoRoot returns the absolute path of the git working tree root,
// cached for the lifetime of the process. Tests may override
// repoRootFunc to bypass the git check.
var repoRootFunc = repoRootGit

func repoRoot() (string, error) {
	return repoRootFunc()
}

func repoRootGit() (string, error) {
	gitTopOnce.Do(func() {
		out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
		if err != nil {
			gitTopErr = fmt.Errorf("not inside a git repository: %w", err)
			return
		}
		gitTopValue = strings.TrimSpace(string(out))
	})
	return gitTopValue, gitTopErr
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
	v, _ := strconv.ParseBool(os.Getenv(envSkipGitCheck))
	return v
}

// writeIfChanged compares data against the existing file at path. If the
// content differs (or the file does not exist), it atomically writes data
// using renameio and returns true. If the content is identical, it returns
// false without touching the file.
func writeIfChanged(path string, data []byte) (bool, error) {
	existing, err := os.ReadFile(path)
	if err == nil && bytes.Equal(existing, data) {
		return false, nil
	}
	if err := renameio.WriteFile(path, data, 0o644); err != nil {
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
func populateResponseTypes(ops []apiOperation, spec *openapi3.T, registry *typeRegistry) {
	if spec == nil || spec.Components == nil || spec.Components.Schemas == nil {
		return
	}

	w := &walker{
		registry: registry,
		spec:     spec,
		inFlight: make(map[string]struct{}),
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
		if !ok {
			// No registered Resp struct (e.g. array-typed responses).
			// Collect sibling types that belong to this operation's group.
			for _, st := range registry.forOperation(ops[i].Group) {
				if !st.IsResp && !st.IsShared && !claimed[st.SchemaRef] {
					ops[i].SiblingTypes = append(ops[i].SiblingTypes, st)
					claimed[st.SchemaRef] = true
				}
			}
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
