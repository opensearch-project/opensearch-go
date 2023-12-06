# CHANGELOG

Inspired from [Keep a Changelog](https://keepachangelog.com/en/1.0.0/)

## [Unreleased]

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

[Unreleased]: https://github.com/opensearch-project/opensearch-go/compare/v2.3.0...HEAD