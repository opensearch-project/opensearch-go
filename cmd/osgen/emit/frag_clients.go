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
)

// SubClient describes a sub-client type and its placement in the hierarchy.
type SubClient struct {
	TypeName  string // e.g. "catClient"
	FieldName string // exported field on parent (e.g. "Cat")
	Parent    string // parent client type ("Client" or "indicesClient")
}

// ClientsFragment renders the Client struct, sub-client types, clientInit, Inspect
// alias, and noBody sentinel for clients_gen.go.
type ClientsFragment struct {
	SubClients []SubClient
}

// Imports returns the imports the Clients fragment needs.
func (f *ClientsFragment) Imports() []Import {
	return []Import{
		{Path: "github.com/opensearch-project/opensearch-go/v4"},
		{Path: "github.com/opensearch-project/opensearch-go/v4/internal/apiutil"},
	}
}

// Body renders the Client struct, sub-client types, clientInit, and the
// Inspect alias as Go source.
func (f *ClientsFragment) Body() (string, error) {
	if len(f.SubClients) == 0 {
		return "", nil
	}

	topLevel := f.topLevel()
	initStmts := f.initStatements()

	data := struct {
		TopLevel   []SubClient
		SubClients []SubClient
		InitStmts  []string
		Hierarchy  []SubClient
	}{
		TopLevel:   topLevel,
		SubClients: f.SubClients,
		InitStmts:  initStmts,
		Hierarchy:  f.SubClients,
	}

	var sb strings.Builder
	if err := clientsFragTmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("rendering ClientsFragment: %w", err)
	}
	return sb.String(), nil
}

func (f *ClientsFragment) topLevel() []SubClient {
	var out []SubClient
	for _, sc := range f.SubClients {
		if sc.Parent == "Client" {
			out = append(out, sc)
		}
	}
	return out
}

func (f *ClientsFragment) initStatements() []string {
	var stmts []string
	for _, sc := range f.SubClients {
		if sc.Parent == "Client" {
			stmts = append(stmts, fmt.Sprintf("client.%s = %s{apiClient: client}", sc.FieldName, sc.TypeName))
		}
	}
	for _, sc := range f.SubClients {
		if sc.Parent != "Client" {
			parentField := f.parentFieldName(sc.Parent)
			stmts = append(stmts, fmt.Sprintf("client.%s.%s = %s{apiClient: client}", parentField, sc.FieldName, sc.TypeName))
		}
	}
	return stmts
}

func (f *ClientsFragment) parentFieldName(parentType string) string {
	for _, sc := range f.SubClients {
		if sc.TypeName == parentType {
			return sc.FieldName
		}
	}
	return parentType
}

func nestedFields(hierarchy []SubClient, parent string) []SubClient {
	var result []SubClient
	for _, sc := range hierarchy {
		if sc.Parent == parent && sc.Parent != "Client" {
			result = append(result, sc)
		}
	}
	return result
}

//nolint:gochecknoglobals // const-ish read-only template
var clientsFragTmpl = template.Must(template.New("clients").Funcs(template.FuncMap{
	"nestedFields": nestedFields,
}).Parse(`// Inspect represents the struct returned by Inspect(), its main use is to return the opensearch.Response to the user.
type Inspect = apiutil.Inspect

// noBody is a typed-nil sentinel passed to do() when the caller does not
// expect a response body.
var noBody *opensearch.NoBody //nolint:gochecknoglobals // package-internal sentinel value

// Client represents the opensearchapi Client summarizing all API calls.
type Client struct {
	Client *opensearch.Client
{{- range .TopLevel}}
	{{.FieldName}} {{.TypeName}}
{{- end}}
}

// clientInit initializes a Client with all sub-clients.
func clientInit(rootClient *opensearch.Client) *Client {
	client := &Client{
		Client: rootClient,
	}
{{- range .InitStmts}}
	{{.}}
{{- end}}
	return client
}
{{range .SubClients}}
type {{.TypeName}} struct {
	apiClient *Client
{{- range nestedFields $.Hierarchy .TypeName}}
	{{.FieldName}} {{.TypeName}}
{{- end}}
}
{{end}}`))

// NewClientsFile builds a Target for clients_gen.go.
func NewClientsFile(outDir, pkg string, subClients []SubClient) Target {
	if len(subClients) == 0 {
		return nil
	}
	return &File{
		FilePath:  outDir + "/clients_gen.go",
		Package:   pkg,
		Fragments: []Fragment{&ClientsFragment{SubClients: subClients}},
	}
}
