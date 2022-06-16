module github.com/opensearch-project/opensearch-go/v2/internal/build

go 1.15

replace github.com/opensearch-project/opensearch-go/v2 => ../../

require (
	github.com/alecthomas/chroma v0.8.2
	github.com/opensearch-project/opensearch-go/v2 v2.0.1
	github.com/spf13/cobra v1.1.3
	golang.org/x/crypto v0.0.0-20210322153248-0c34fe9e7dc2
	golang.org/x/tools v0.1.0
	gopkg.in/yaml.v2 v2.4.0
)
