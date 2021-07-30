module github.com/opensearch-project/opensearch-go/_examples/instrumentation/opencensus

go 1.11

replace github.com/opensearch-project/opensearch-go => ../..

require (
	github.com/opensearch-project/opensearch-go v0.0.0
	github.com/fatih/color v1.7.0
	github.com/mattn/go-colorable v0.1.0 // indirect
	github.com/mattn/go-isatty v0.0.4 // indirect
	go.opencensus.io v0.19.0
	golang.org/x/crypto v0.0.0-20190308221718-c2843e01d9a2
)
