# CHANGELOG

Inspired from [Keep a Changelog](https://keepachangelog.com/en/1.0.0/)

## [Unreleased]

### Dependencies

- Bumps `github.com/aws/aws-sdk-go` from 1.44.263 to 1.47.4
- Bumps `github.com/aws/aws-sdk-go-v2` from 1.18.0 to 1.22.1
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.18.25 to 1.19.1
- Bumps `github.com/stretchr/testify` from 1.8.2 to 1.8.4
- Bumps `golang.org/x/net` from 0.7.0 to 0.17.0
- Bumps `github.com/golangci/golangci-lint-action` from 1.53.3 to 1.54.2

### Added

- Adds `Err()` function to Response for detailed errors ([#246](https://github.com/opensearch-project/opensearch-go/pull/246))
- Adds golangci-lint as code analysis tool ([#313](https://github.com/opensearch-project/opensearch-go/pull/313))
- Adds govulncheck to check for go vulnerablities ([#405](https://github.com/opensearch-project/opensearch-go/pull/405))

### Changed

- Removes the need for double error checking ([#246](https://github.com/opensearch-project/opensearch-go/pull/246))
- Updated and adjusted golangci-lint, solve linting complains for signer ([#352](https://github.com/opensearch-project/opensearch-go/pull/352))
- Solve linting complains for opensearchtransport ([#353](https://github.com/opensearch-project/opensearch-go/pull/353))
- Updated Developer guide to include docker build instructions ([#385](https://github.com/opensearch-project/opensearch-go/pull/385))
- Test against version 2.9.0,2.10.0, run tests in all branches, change intergration tests to wait for OpenSearch to start ([#392](https://github.com/opensearch-project/opensearch-go/pull/392))
- Makefile: use docker golangci-lint, run integration test on `.` folder, change coverage generation ([#392](https://github.com/opensearch-project/opensearch-go/pull/392)) 

### Deprecated

- Deprecate legacy API /_template ([#390](https://github.com/opensearch-project/opensearch-go/pull/390))

### Removed

### Fixed

- Corrects AWSv4 signature on DataStream Stats with no index name specified ([#338](https://github.com/opensearch-project/opensearch-go/pull/338))
- Fixed GetSourceRequest `Source` field and deprecated the `Source` parameter ([#402](https://github.com/opensearch-project/opensearch-go/pull/402))

### Security

[Unreleased]: https://github.com/opensearch-project/opensearch-go/compare/v2.3.0...HEAD