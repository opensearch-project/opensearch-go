- [User Guide](#user-guide)
	- [Example](#example)
	- [Amazon OpenSearch Service](#amazon-opensearch-service)
			- [AWS SDK V1](#aws-sdk-v1)
			- [AWS SDK V2](#aws-sdk-v2)

# User Guide

## Example

In the example below, we create a client, an index with non-default settings, insert a document to the index,
search for the document, delete the document and finally delete the index.

```go
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	opensearch "github.com/opensearch-project/opensearch-go/v2"
	opensearchapi "github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

const IndexName = "go-test-index1"

func main() {
	if err := example(); err != nil {
		fmt.Println(fmt.Sprintf("Error: %s", err))
		os.Exit(1)
	}
}

func example() error {

	// Initialize the client with SSL/TLS enabled.
	client, err := opensearch.NewClient(opensearch.Config{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // For testing only. Use certificate for validation.
		},
		Addresses: []string{"https://localhost:9200"},
		Username:  "admin", // For testing only. Don't store credentials in code.
		Password:  "admin",
	})
	if err != nil {
		return err
	}

	// Print OpenSearch version information on console.
	fmt.Println(client.Info())

	// Define index mapping.
	mapping := strings.NewReader(`{
	    "settings": {
	        "index": {
	            "number_of_shards": 4
	        }
	    }
	}`)

	// Create an index with non-default settings.
	createIndex := opensearchapi.IndicesCreateRequest{
		Index: IndexName,
		Body:  mapping,
	}
	ctx := context.Background()
	var opensearchError *opensearchapi.Error
	createIndexResponse, err := createIndex.Do(ctx, client)
	// Load err into opensearchapi.Error to access the fields and tolerate if the index already exists
	if err != nil {
		if errors.As(err, &opensearchError) {
			if opensearchError.Err.Type != "resource_already_exists_exception" {
				return err
			}
		} else {
			return err
		}
	}
	fmt.Println(createIndexResponse)

	// Add a document to the index.
	document := strings.NewReader(`{
	    "title": "Moneyball",
	    "director": "Bennett Miller",
	    "year": "2011"
	}`)

	docId := "1"
	req := opensearchapi.IndexRequest{
		Index:      IndexName,
		DocumentID: docId,
		Body:       document,
	}
	insertResponse, err := req.Do(ctx, client)
	if err != nil {
		return err
	}
	fmt.Println(insertResponse)

	// Search for the document.
	content := strings.NewReader(`{
	    "size": 5,
	    "query": {
	        "multi_match": {
	            "query": "miller",
	            "fields": ["title^2", "director"]
	        }
	    }
	}`)

	search := opensearchapi.SearchRequest{
		Body: content,
	}

	searchResponse, err := search.Do(ctx, client)
	if err != nil {
		return err
	}
	fmt.Println(searchResponse)

	// Delete the document.
	deleteReq := opensearchapi.DeleteRequest{
		Index:      IndexName,
		DocumentID: docId,
	}

	deleteResponse, err := deleteReq.Do(ctx, client)
	if err != nil {
		return err
	}
	fmt.Println("deleting document")
	fmt.Println(deleteResponse)

	// Delete previously created index.
	deleteIndex := opensearchapi.IndicesDeleteRequest{
		Index: []string{IndexName},
	}

	deleteIndexResponse, err := deleteIndex.Do(ctx, client)
	if err != nil {
		return err
	}
	fmt.Println("deleting index", deleteIndexResponse)

	// Try to delete the index again which failes as it does not exist
	// Load err into opensearchapi.Error to access the fields and tolerate if the index is missing
	_, err = deleteIndex.Do(ctx, client)
	if err != nil {
		if errors.As(err, &opensearchError) {
			if opensearchError.Err.Type != "index_not_found_exception" {
				return err
			}
		} else {
			return err
		}
	}
	return nil
}
```

## Amazon OpenSearch Service

Before starting, we strongly recommend reading the full AWS documentation regarding using IAM credentials to sign
requests to OpenSearch APIs.
See [Identity and Access Management in Amazon OpenSearch Service.](https://docs.aws.amazon.com/opensearch-service/latest/developerguide/ac.html)

> Even if you configure a completely open resource-based access policy, all requests to the OpenSearch Service
> configuration API must be signed. If your policies specify IAM users or roles, requests to the OpenSearch APIs also
> must
> be signed using AWS Signature Version 4.
>
See [Managed Domains signing-service requests.](https://docs.aws.amazon.com/opensearch-service/latest/developerguide/ac.html#managedomains-signing-service-requests)

Depending on the version of AWS SDK used, import the v1 or v2 request signer from `signer/aws` or `signer/awsv2`
respectively.
Both signers are equivalent in their functionality, they provide AWS Signature Version 4 (SigV4).

To read more about SigV4
see [Signature Version 4 signing process](https://docs.aws.amazon.com/general/latest/gr/signature-version-4.html)

Here are some Go samples that show how to sign each OpenSearch request and automatically search for AWS credentials from
the ~/.aws folder or environment variables:

#### AWS SDK V1

```go
package main

import (
	"context"
	"io"
	"log"

	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	requestsigner "github.com/opensearch-project/opensearch-go/v2/signer/aws"
)

const endpoint = "" // e.g. https://opensearch-domain.region.com

func main() {
	ctx := context.Background()

	// Create an AWS request Signer and load AWS configuration using default config folder or env vars.
	// See https://docs.aws.amazon.com/opensearch-service/latest/developerguide/request-signing.html#request-signing-go
	signer, err := requestsigner.NewSigner(session.Options{SharedConfigState: session.SharedConfigEnable})
	if err != nil {
		log.Fatal(err) // Do not log.fatal in a production ready app.
	}

	// Create an opensearch client and use the request-signer
	client, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{endpoint},
		Signer:    signer,
	})
	if err != nil {
		log.Fatal("client creation err", err)
	}

	ping := opensearchapi.PingRequest{}

	resp, err := ping.Do(ctx, client)
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()

	log.Println("PING OK")
}
```

#### AWS SDK V2

Use the AWS SDK v2 for Go to authenticate with Amazon OpenSearch service.

```go
package main

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	opensearch "github.com/opensearch-project/opensearch-go/v2"
	opensearchapi "github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	requestsigner "github.com/opensearch-project/opensearch-go/v2/signer/awsv2"
)

const endpoint = "" // e.g. https://opensearch-domain.region.com or Amazon OpenSearch Serverless endpoint

func main() {
	ctx := context.Background()

	awsCfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("<AWS_REGION>"),
		config.WithCredentialsProvider(
			getCredentialProvider("<AWS_ACCESS_KEY>", "<AWS_SECRET_ACCESS_KEY>", "<AWS_SESSION_TOKEN>"),
		),
	)
	if err != nil {
		log.Fatal(err) // Do not log.fatal in a production ready app.
	}

	// Create an AWS request Signer and load AWS configuration using default config folder or env vars.
	signer, err := requestsigner.NewSignerWithService(awsCfg, "es") // "aoss" for Amazon OpenSearch Serverless
	if err != nil {
		log.Fatal(err) // Do not log.fatal in a production ready app.
	}

	// Create an opensearch client and use the request-signer
	client, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{endpoint},
		Signer:    signer,
	})
	if err != nil {
		log.Fatal("client creation err", err)
	}

	indexName = "go-test-index"

	// Define index mapping.
	mapping := strings.NewReader(`{
	 "settings": {
	   "index": {
	        "number_of_shards": 4
	        }
	      }
	 }`)
    
	// Create an index with non-default settings.
	createIndex := opensearchapi.IndicesCreateRequest{
		Index: indexName,
		Body:  mapping,
	}
	createIndexResponse, err := createIndex.Do(ctx, client)
	if err != nil {
		log.Println("Error ", err.Error())
		log.Println("failed to create index ", err)
		log.Fatal("create response body read err", err)
	}
	log.Println(createIndexResponse)

	// Delete previously created index.
	deleteIndex := opensearchapi.IndicesDeleteRequest{
		Index: []string{indexName},
	}

	deleteIndexResponse, err := deleteIndex.Do(ctx, client)
	if err != nil {
		log.Println("failed to delete index ", err)
		log.Fatal("delete index response body read err", err)
	}
	log.Println("deleting index", deleteIndexResponse)
}

func getCredentialProvider(accessKey, secretAccessKey, token string) aws.CredentialsProviderFunc {
	return func(ctx context.Context) (aws.Credentials, error) {
		c := &aws.Credentials{
			AccessKeyID:     accessKey,
			SecretAccessKey: secretAccessKey,
			SessionToken:    token,
		}
		return *c, nil
	}
}

```
