# CHANGELOG

Inspired from [Keep a Changelog](https://keepachangelog.com/en/1.0.0/)

## [Unreleased]

### Dependencies

- Bumps `github.com/aws/aws-sdk-go` from 1.44.45 to 1.44.241
- Bumps `github.com/aws/aws-sdk-go-v2` from 1.17.1 to 1.17.6
- Bumps `github.com/aws/aws-sdk-go-v2/config` from 1.18.8 to 1.18.21
- Bumps `github.com/stretchr/testify` from 1.8.0 to 1.8.2

### Added

- Adds support for Amazon OpenSearch Serverless ([#216](https://github.com/opensearch-project/opensearch-go/pull/216), [#259](https://github.com/opensearch-project/opensearch-go/pull/259))
- Adds Github workflow for changelog verification ([#172](https://github.com/opensearch-project/opensearch-go/pull/172))
- Adds Go Documentation link for the client ([#182](https://github.com/opensearch-project/opensearch-go/pull/182))
- Adds implementation of Data Streams API ([#257](https://github.com/opensearch-project/opensearch-go/pull/257)
- Adds `Err()` function to Response for detailed errors ([#246](https://github.com/opensearch-project/opensearch-go/pull/246))
- Adds Point In Time API ([#253](https://github.com/opensearch-project/opensearch-go/pull/253))
- Adds InfoResp type ([#253](https://github.com/opensearch-project/opensearch-go/pull/253))
- Adds markdown linter ([#261](https://github.com/opensearch-project/opensearch-go/pull/261))
- Adds testcases to check upsert functionality ([#207](https://github.com/opensearch-project/opensearch-go/issues/207))
- Adds @Jakob3xD to co-maintainers ([#270](https://github.com/opensearch-project/opensearch-go/pull/270))

### Changed

- Uses `[]string` instead of `string` in `SnapshotDeleteRequest` ([#237](https://github.com/opensearch-project/opensearch-go/pull/237))
- Removes the need for double error checking ([#246](https://github.com/opensearch-project/opensearch-go/pull/246))
- Updates workflows to reduce CI time, consolidate OpenSearch versions, update compatibility matrix ([#242](https://github.com/opensearch-project/opensearch-go/pull/242))
- Moved @svencowart to emeritus maintainers ([#270](https://github.com/opensearch-project/opensearch-go/pull/270))
- Read, close and replace the http Reponse Body ([#300](https://github.com/opensearch-project/opensearch-go/pull/300))

### Deprecated

### Removed

- Removes info call before performing every request ([#219](https://github.com/opensearch-project/opensearch-go/pull/219))

### Fixed

- Renames the sequence number struct tag to `if_seq_no` to fix optimistic concurrency control ([#166](https://github.com/opensearch-project/opensearch-go/pull/166))
- Fixes `RetryOnConflict` on bulk indexer ([#215](https://github.com/opensearch-project/opensearch-go/pull/215))
- Corrects curl logging to emit the correct URL destination ([#101](https://github.com/opensearch-project/opensearch-go/pull/101))
- Corrects handling of errors without an error response body ([#286](https://github.com/opensearch-project/opensearch-go/pull/286))

### Security

[Unreleased]: https://github.com/opensearch-project/opensearch-go/compare/2.1...HEAD
