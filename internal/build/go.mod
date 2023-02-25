module github.com/opensearch-project/opensearch-go/v2/internal/build

go 1.15

replace github.com/opensearch-project/opensearch-go/v2 => ../../

require (
	github.com/alecthomas/chroma v0.8.2
	github.com/kr/pretty v0.1.0 // indirect
	github.com/opensearch-project/opensearch-go/v2 v2.2.0
	github.com/spf13/cobra v1.6.1
	golang.org/x/crypto v0.1.0
	golang.org/x/tools v0.6.0
	gopkg.in/check.v1 v1.0.0-20180628173108-788fd7840127 // indirect
	gopkg.in/yaml.v2 v2.4.0
)
