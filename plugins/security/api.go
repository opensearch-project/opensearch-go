// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"context"
	"fmt"

	"github.com/opensearch-project/opensearch-go/v3"
)

// Config represents the client configuration
type Config struct {
	Client opensearch.Config
}

// Client represents the security Client summarizing all API calls
type Client struct {
	Client         *opensearch.Client
	Account        accountClient
	ActionGroups   actiongroupsClient
	Audit          auditClient
	InternalUsers  internalusersClient
	NodesDN        nodesdnClient
	Roles          rolesClient
	RolesMapping   rolesmappingClient
	SecurityConfig securityconfigClient
	SSL            sslClient
	Tenants        tenantsClient
}

// clientInit inits the Client with all sub clients
func clientInit(rootClient *opensearch.Client) *Client {
	client := &Client{
		Client: rootClient,
	}
	client.Account = accountClient{apiClient: client}
	client.ActionGroups = actiongroupsClient{apiClient: client}
	client.Audit = auditClient{apiClient: client}
	client.InternalUsers = internalusersClient{apiClient: client}
	client.NodesDN = nodesdnClient{apiClient: client}
	client.Roles = rolesClient{apiClient: client}
	client.RolesMapping = rolesmappingClient{apiClient: client}
	client.SecurityConfig = securityconfigClient{apiClient: client}
	client.SSL = sslClient{apiClient: client}
	client.Tenants = tenantsClient{apiClient: client}
	return client
}

// NewClient returns a security client
func NewClient(config Config) (*Client, error) {
	rootClient, err := opensearch.NewClient(config.Client)
	if err != nil {
		return nil, err
	}

	return clientInit(rootClient), nil
}

// do calls the opensearch.Client.Do() and checks the response for errors
func (c *Client) do(ctx context.Context, req opensearch.Request, dataPointer any) (*opensearch.Response, error) {
	resp, err := c.Client.Do(ctx, req, dataPointer)
	if err != nil {
		return nil, err
	}

	if resp.IsError() {
		if dataPointer != nil {
			return resp, opensearch.ParseError(resp)
		} else {
			return resp, fmt.Errorf("status: %s", resp.Status())
		}
	}

	return resp, nil
}
