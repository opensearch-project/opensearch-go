# CHANGELOG

Inspired from [Keep a Changelog](https://keepachangelog.com/en/1.0.0/)

## [Unreleased]

### Dependencies

- Bumps `github.com/aws/aws-sdk-go-v2` from 1.17.1 to 1.17.6
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.18.10 to 1.18.18
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.18.9 to 1.18.12
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.18.8 to 1.18.10
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.17.10 to 1.18.9
- Bumps `github.com/aws/aws-sdk-go` from 1.44.195 to 1.44.225
- Bumps `github.com/aws/aws-sdk-go` from 1.44.185 to 1.44.205
- Bumps `github.com/aws/aws-sdk-go` from 1.44.180 to 1.44.195
- Bumps `github.com/aws/aws-sdk-go` from 1.44.176 to 1.44.185
- Bumps `github.com/aws/aws-sdk-go` from 1.44.132 to 1.44.180
- Bumps `github.com/stretchr/testify` from 1.8.1 to 1.8.2

### Added

- Github workflow for changelog verification ([#172](https://github.com/opensearch-project/opensearch-go/pull/172))
- Add Go Documentation link for the client ([#182](https://github.com/opensearch-project/opensearch-go/pull/182))
- Add implementation of Data Streams API ([#257](https://github.com/opensearch-project/opensearch-go/pull/257)
- Support for Amazon OpenSearch Serverless ([#216](https://github.com/opensearch-project/opensearch-go/pull/216), [#259](https://github.com/opensearch-project/opensearch-go/pull/259))
- Add Err() function to Response for detailed errors ([#246](https://github.com/opensearch-project/opensearch-go/pull/246))

### Dependencies

- Bumps `github.com/stretchr/testify` from 1.8.0 to 1.8.1
- Bumps `github.com/aws/aws-sdk-go` from 1.44.45 to 1.44.132

### Changed

- Workflow improvements ([#242](https://github.com/opensearch-project/opensearch-go/pull/242))
- Opensearchapi check the response for errors ([#246](https://github.com/opensearch-project/opensearch-go/pull/246))

### Deprecated

### Removed

- Remove info call before performing every request ([#219](https://github.com/opensearch-project/opensearch-go/pull/219))

### Fixed

- Renamed the sequence number struct tag to if_seq_no to fix optimistic concurrency control ([#166](https://github.com/opensearch-project/opensearch-go/pull/166))
- Fix `RetryOnConflict` on bulk indexer ([#215](https://github.com/opensearch-project/opensearch-go/pull/215))
- Correct curl logging to emit the correct URL destination ([#101](https://github.com/opensearch-project/opensearch-go/pull/101))

### Security

[Unreleased]: https://github.com/opensearch-project/opensearch-go/compare/2.1...HEAD
