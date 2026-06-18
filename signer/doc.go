// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.
//
// Modifications Copyright OpenSearch Contributors. See
// GitHub history for details.

/*
Package signer defines the request-signing interface used by the OpenSearch client.

The Signer interface signs an outgoing http.Request before it is sent, which is required
when connecting to managed services such as Amazon OpenSearch Service that authenticate
requests with AWS Signature Version 4. Pass an implementation to the client through the
opensearch.Config Signer field.

The awsv2 subpackage is the implementation to use. It is built on AWS SDK for Go v2
and constructs a SigV4 signer from an aws.Config via NewSigner (for Amazon OpenSearch
Service) or NewSignerWithService (for Amazon OpenSearch Serverless and other services):

	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
	  log.Fatal(err)
	}

	signer, err := awsv2.NewSigner(cfg)

The constructor ensures credentials are cached so signing reuses resolved credentials
rather than calling Credentials.Retrieve on every request. A raw CredentialsProvider is
wrapped in an aws.CredentialsCache; an already-cached provider (such as one from
config.LoadDefaultConfig) is left as-is. This matters most for STS-backed providers
(assume-role, web identity, IRSA), where a per-request Retrieve is an STS call on the
hot path that under load can exhaust the account's STS rate limits and disrupt any
service that depends on STS, not just this client.
*/
package signer
