// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

/*
Package awsv2 provides AWS Signature Version 4 request signing for OpenSearch using
AWS SDK for Go v2. It is the recommended signer. Construct one from an aws.Config with
NewSigner (for Amazon OpenSearch Service) or NewSignerWithService, optionally passing
SignerOptions to customize the underlying SigV4 signer.
*/
package awsv2
