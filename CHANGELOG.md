# CHANGELOG

Inspired from [Keep a Changelog](https://keepachangelog.com/en/1.0.0/)

## [Unreleased]

### Dependencies

- Bumps `github.com/aws/aws-sdk-go` from 1.44.180 to 1.45.12
- Bumps `github.com/aws/aws-sdk-go-v2` from 1.17.4 to 1.21.0
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.18.8 to 1.18.40
- Bumps `github.com/stretchr/testify` from 1.8.1 to 1.8.4

### Added

- Adds implementation of Data Streams API ([#257](https://github.com/opensearch-project/opensearch-go/pull/257))
- Adds `Err()` function to Response for detailed errors ([#246](https://github.com/opensearch-project/opensearch-go/pull/246))
- Adds Point In Time API ([#253](https://github.com/opensearch-project/opensearch-go/pull/253))
- Adds InfoResp type ([#253](https://github.com/opensearch-project/opensearch-go/pull/253))
- Adds markdown linter ([#261](https://github.com/opensearch-project/opensearch-go/pull/261))
- Adds testcases to check upsert functionality ([#269](https://github.com/opensearch-project/opensearch-go/pull/269))
- Adds @Jakob3xD to co-maintainers ([#270](https://github.com/opensearch-project/opensearch-go/pull/270))
- Adds dynamic type to \_source field ([#285](https://github.com/opensearch-project/opensearch-go/pull/285))
- Adds testcases for Document API ([#285](https://github.com/opensearch-project/opensearch-go/pull/285))
- Adds `index_lifecycle` guide ([#287](https://github.com/opensearch-project/opensearch-go/pull/287))
- Adds `bulk` guide ([#292](https://github.com/opensearch-project/opensearch-go/pull/292))
- Adds `search` guide ([#291](https://github.com/opensearch-project/opensearch-go/pull/291))
- Adds `document_lifecycle` guide ([#290](https://github.com/opensearch-project/opensearch-go/pull/290))
- Adds `index_template` guide ([#289](https://github.com/opensearch-project/opensearch-go/pull/289))
- Adds `advanced_index_actions` guide ([#288](https://github.com/opensearch-project/opensearch-go/pull/288))
- Adds testcases to check UpdateByQuery functionality ([#304](https://github.com/opensearch-project/opensearch-go/pull/304))
- Adds additional timeout after cluster start ([#303](https://github.com/opensearch-project/opensearch-go/pull/303))
- Adds docker healthcheck to auto restart the container ([#315](https://github.com/opensearch-project/opensearch-go/pull/315))
- Adds golangci-lint as code analysis tool ([#313](https://github.com/opensearch-project/opensearch-go/pull/313))

### Changed

- Uses `[]string` instead of `string` in `SnapshotDeleteRequest` ([#237](https://github.com/opensearch-project/opensearch-go/pull/237))
- Removes the need for double error checking ([#246](https://github.com/opensearch-project/opensearch-go/pull/246))
- Updates workflows to reduce CI time, consolidate OpenSearch versions, update compatibility matrix ([#242](https://github.com/opensearch-project/opensearch-go/pull/242))
- Moved @svencowart to emeritus maintainers ([#270](https://github.com/opensearch-project/opensearch-go/pull/270))
- Read, close and replace the http Reponse Body ([#300](https://github.com/opensearch-project/opensearch-go/pull/300))
- Updated and adjusted golangci-lint, solve linting complains for signer ([#352](https://github.com/opensearch-project/opensearch-go/pull/352))
- Solve linting complains for opensearchtransport ([#353](https://github.com/opensearch-project/opensearch-go/pull/353))

### Deprecated

### Removed

- Removes info call before performing every request ([#219](https://github.com/opensearch-project/opensearch-go/pull/219))

### Fixed

- Corrects curl logging to emit the correct URL destination ([#101](https://github.com/opensearch-project/opensearch-go/pull/101))
- Corrects handling of errors without an error response body ([#286](https://github.com/opensearch-project/opensearch-go/pull/286))
- Corrects AWSv4 signature on DataStream Stats with no index name specified ([#338](https://github.com/opensearch-project/opensearch-go/pull/338))

### Security

[Unreleased]: https://github.com/opensearch-project/opensearch-go/compare/v2.2.0...HEAD