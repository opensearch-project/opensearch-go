# CHANGELOG

Inspired from [Keep a Changelog](https://keepachangelog.com/en/1.0.0/)

## [3.0.0]
### Dependencies

- Bumps `github.com/aws/aws-sdk-go` from 1.44.263 to 1.48.13
- Bumps `github.com/aws/aws-sdk-go-v2` from 1.18.0 to 1.23.5
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.18.25 to 1.25.11
- Bumps `github.com/stretchr/testify` from 1.8.2 to 1.8.4
- Bumps `golang.org/x/net` from 0.7.0 to 0.17.0
- Bumps `github.com/golangci/golangci-lint-action` from 1.53.3 to 1.54.2

### Added

- Adds `Err()` function to Response for detailed errors ([#246](https://github.com/opensearch-project/opensearch-go/pull/246))
- Adds golangci-lint as code analysis tool ([#313](https://github.com/opensearch-project/opensearch-go/pull/313))
- Adds govulncheck to check for go vulnerablities ([#405](https://github.com/opensearch-project/opensearch-go/pull/405))
- Adds opensearchapi with new client and function structure ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- Adds integration tests for all opensearchapi functions ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- Adds guide on making raw JSON REST requests ([#399](https://github.com/opensearch-project/opensearch-go/pull/399))

### Changed

- Removes the need for double error checking ([#246](https://github.com/opensearch-project/opensearch-go/pull/246))
- Updated and adjusted golangci-lint, solve linting complains for signer ([#352](https://github.com/opensearch-project/opensearch-go/pull/352))
- Solve linting complains for opensearchtransport ([#353](https://github.com/opensearch-project/opensearch-go/pull/353))
- Updated Developer guide to include docker build instructions ([#385](https://github.com/opensearch-project/opensearch-go/pull/385))
- Test against version 2.9.0,2.10.0, run tests in all branches, change intergration tests to wait for OpenSearch to start ([#392](https://github.com/opensearch-project/opensearch-go/pull/392))
- Makefile: use docker golangci-lint, run integration test on `.` folder, change coverage generation ([#392](https://github.com/opensearch-project/opensearch-go/pull/392)) 
- golangci-lint: update rules and fail when issues are found ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- go: update to golang version 1.20 ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- guids: updated to work for the new opensearchapi ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- Test adjusted to new opensearchapi functions and structs ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- Change codecov to comment code coverage to each PR ([#410](https://github.com/opensearch-project/opensearch-go/pull/410))
- Change module version from v2 to v3 ([#444](https://github.com/opensearch-project/opensearch-go/pull/444))

### Deprecated

- Deprecate legacy API /_template ([#390](https://github.com/opensearch-project/opensearch-go/pull/390))

### Removed

- Removes all old opensearchapi functions ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))
- Removes /internal/build code and folders ([#421](https://github.com/opensearch-project/opensearch-go/pull/421))

### Fixed

- Corrects AWSv4 signature on DataStream Stats with no index name specified ([#338](https://github.com/opensearch-project/opensearch-go/pull/338))
- Fixed GetSourceRequest `Source` field and deprecated the `Source` parameter ([#402](https://github.com/opensearch-project/opensearch-go/pull/402))
- Corrects developer guide summary with golang version 1.20 ([#434](https://github.com/opensearch-project/opensearch-go/pull/434))

### Security

## [2.3.0]
### Dependencies

- Bumps `github.com/aws/aws-sdk-go` from 1.44.180 to 1.44.263
- Bumps `github.com/aws/aws-sdk-go-v2` from 1.17.4 to 1.18.0
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.18.8 to 1.18.25
- Bumps `github.com/stretchr/testify` from 1.8.1 to 1.8.2

### Added

- Adds implementation of Data Streams API ([#257](https://github.com/opensearch-project/opensearch-go/pull/257))
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

### Changed

- Uses `[]string` instead of `string` in `SnapshotDeleteRequest` ([#237](https://github.com/opensearch-project/opensearch-go/pull/237))
- Updates workflows to reduce CI time, consolidate OpenSearch versions, update compatibility matrix ([#242](https://github.com/opensearch-project/opensearch-go/pull/242))
- Moved @svencowart to emeritus maintainers ([#270](https://github.com/opensearch-project/opensearch-go/pull/270))
- Read, close and replace the http Reponse Body ([#300](https://github.com/opensearch-project/opensearch-go/pull/300))

### Deprecated
### Removed
### Fixed

- Corrects curl logging to emit the correct URL destination ([#101](https://github.com/opensearch-project/opensearch-go/pull/101))

### Security

## [2.2.0]
### Dependencies
- Bumps `github.com/aws/aws-sdk-go-v2` from 1.17.1 to 1.17.3
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.17.10 to 1.18.8
- Bumps `github.com/aws/aws-sdk-go` from 1.44.176 to 1.44.180
- Bumps `github.com/aws/aws-sdk-go` from 1.44.132 to 1.44.180

### Added
- Github workflow for changelog verification ([#172](https://github.com/opensearch-project/opensearch-go/pull/172))
- Add Go Documentation link for the client ([#182](https://github.com/opensearch-project/opensearch-go/pull/182))
- Support for Amazon OpenSearch Serverless ([#216](https://github.com/opensearch-project/opensearch-go/pull/216))

### Dependencies
- Bumps `github.com/stretchr/testify` from 1.8.0 to 1.8.1
- Bumps `github.com/aws/aws-sdk-go` from 1.44.45 to 1.44.132

### Changed
### Deprecated
### Removed
- Remove info call before performing every request ([#219](https://github.com/opensearch-project/opensearch-go/pull/219))

### Fixed
 - Renamed the sequence number struct tag to if_seq_no to fix optimistic concurrency control ([#166](https://github.com/opensearch-project/opensearch-go/pull/166))
 - Fix `RetryOnConflict` on bulk indexer ([#215](https://github.com/opensearch-project/opensearch-go/pull/215))

### Security

[Unreleased]: https://github.com/opensearch-project/opensearch-go/compare/v3.0.0...HEAD
[3.0.0]: https://github.com/opensearch-project/opensearch-go/compare/v2.3.0...v3.0.0
[2.3.0]: https://github.com/opensearch-project/opensearch-go/compare/v2.2.0...v2.3.0
[2.2.0]: https://github.com/opensearch-project/opensearch-go/compare/v2.1.0...v2.2.0
[2.1.0]: https://github.com/opensearch-project/opensearch-go/compare/v2.0.1...v2.1.0
[2.0.1]: https://github.com/opensearch-project/opensearch-go/compare/v2.0.0...v2.0.1
[2.0.0]: https://github.com/opensearch-project/opensearch-go/compare/v1.1.0...v2.0.0
[1.0.0]: https://github.com/opensearch-project/opensearch-go/compare/v1.0.0...v1.1.0
