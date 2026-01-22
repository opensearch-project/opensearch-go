// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package security

import (
	"encoding/json"
	"net/http"

	"github.com/opensearch-project/opensearch-go/v4"
)

// ConfigGetReq represents possible options for the securityconfig get request
type ConfigGetReq struct {
	Header http.Header
}

// GetRequest returns the *http.Request that gets executed by the client
func (r ConfigGetReq) GetRequest() (*http.Request, error) {
	return opensearch.BuildRequest(
		"GET",
		"/_plugins/_security/api/securityconfig",
		nil,
		make(map[string]string),
		r.Header,
	)
}

// ConfigGetResp represents the returned struct of the securityconfig get response
type ConfigGetResp struct {
	Config struct {
		Dynamic ConfigDynamic `json:"dynamic"`
	} `json:"config"`
	response *opensearch.Response
}

// Inspect returns the Inspect type containing the raw *opensearch.Response
func (r ConfigGetResp) Inspect() Inspect {
	return Inspect{Response: r.response}
}

// ConfigDynamic represents the opensearch security config and is sub type of ConfigGetResp and ConfigPutBody
type ConfigDynamic struct {
	FilteredAliasMode            string              `json:"filtered_alias_mode"`
	DisableRestAuth              bool                `json:"disable_rest_auth"`
	DisableIntertransportAuth    bool                `json:"disable_intertransport_auth"`
	RespectRequestIndicesOptions bool                `json:"respect_request_indices_options"`
	Kibana                       ConfigDynamicKibana `json:"kibana"`
	HTTP                         ConfigDynamicHTTP   `json:"http"`
	Authc                        ConfigDynamicAuthc  `json:"authc"`
	Authz                        ConfigDynamicAuthz  `json:"authz"`
	AuthFailureListeners         json.RawMessage     `json:"auth_failure_listeners"`
	DoNotFailOnForbidden         bool                `json:"do_not_fail_on_forbidden"`
	MultiRolespanEnabled         bool                `json:"multi_rolespan_enabled"`
	HostsResolverMode            string              `json:"hosts_resolver_mode"`
	DoNotFailOnForbiddenEmpty    bool                `json:"do_not_fail_on_forbidden_empty"`
	OnBehalfOf                   *struct {
		Enabled bool `json:"enabled"`
	} `json:"on_behalf_of,omitempty"`
}

// ConfigDynamicKibana is a sub type of ConfigDynamic containing security settings for kibana
type ConfigDynamicKibana struct {
	MultitenancyEnabled  bool     `json:"multitenancy_enabled"`
	PrivateTenantEnabled *bool    `json:"private_tenant_enabled,omitempty"`
	DefaultTenant        *string  `json:"default_tenant,omitempty"`
	ServerUsername       string   `json:"server_username"`
	Index                string   `json:"index"`
	SignInOptions        []string `json:"sign_in_options,omitempty"`
}

// ConfigDynamicHTTP is a sub type of ConfigDynamic containing security settings for HTTP
type ConfigDynamicHTTP struct {
	AnonymousAuthEnabled bool `json:"anonymous_auth_enabled"`
	Xff                  struct {
		Enabled         bool   `json:"enabled"`
		InternalProxies string `json:"internalProxies"`
		RemoteIPHeader  string `json:"remoteIpHeader"`
	} `json:"xff"`
}

// ConfigDynamicAuthc is a sub type of ConfigDynamic containing security settings for Authc
type ConfigDynamicAuthc struct {
	JwtAuthDomain struct {
		HTTPEnabled       bool  `json:"http_enabled"`
		TransportEnabled  *bool `json:"transport_enabled,omitempty"`
		Order             int   `json:"order"`
		HTTPAuthenticator struct {
			Challenge bool   `json:"challenge"`
			Type      string `json:"type"`
			Config    struct {
				JwksURI                      string `json:"jwks_uri,omitempty"`
				SigningKey                   string `json:"signing_key"`
				JwtHeader                    string `json:"jwt_header"`
				JwtClockSkewToleranceSeconds int    `json:"jwt_clock_skew_tolerance_seconds"`
			} `json:"config"`
		} `json:"http_authenticator"`
		AuthenticationBackend struct {
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		} `json:"authentication_backend"`
		Description string `json:"description"`
	} `json:"jwt_auth_domain"`
	LDAP struct {
		HTTPEnabled       bool  `json:"http_enabled"`
		TransportEnabled  *bool `json:"transport_enabled,omitempty"`
		Order             int   `json:"order"`
		HTTPAuthenticator struct {
			Challenge bool            `json:"challenge"`
			Type      string          `json:"type"`
			Config    json.RawMessage `json:"config"`
		} `json:"http_authenticator"`
		AuthenticationBackend struct {
			Type   string `json:"type"`
			Config struct {
				EnableSSL           bool     `json:"enable_ssl"`
				EnableStartTLS      bool     `json:"enable_start_tls"`
				EnableSSLClientAuth bool     `json:"enable_ssl_client_auth"`
				VerifyHostnames     bool     `json:"verify_hostnames"`
				Hosts               []string `json:"hosts"`
				Userbase            string   `json:"userbase"`
				Usersearch          string   `json:"usersearch"`
			} `json:"config"`
		} `json:"authentication_backend"`
		Description string `json:"description"`
	} `json:"ldap"`
	BasicInternalAuthDomain struct {
		HTTPEnabled       bool  `json:"http_enabled"`
		TransportEnabled  *bool `json:"transport_enabled,omitempty"`
		Order             int   `json:"order"`
		HTTPAuthenticator struct {
			Challenge bool            `json:"challenge"`
			Type      string          `json:"type"`
			Config    json.RawMessage `json:"config"`
		} `json:"http_authenticator"`
		AuthenticationBackend struct {
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		} `json:"authentication_backend"`
		Description string `json:"description"`
	} `json:"basic_internal_auth_domain"`
	ProxyAuthDomain struct {
		HTTPEnabled       bool  `json:"http_enabled"`
		TransportEnabled  *bool `json:"transport_enabled,omitempty"`
		Order             int   `json:"order"`
		HTTPAuthenticator struct {
			Challenge bool   `json:"challenge"`
			Type      string `json:"type"`
			Config    struct {
				UserHeader  string `json:"user_header"`
				RolesHeader string `json:"roles_header"`
			} `json:"config"`
		} `json:"http_authenticator"`
		AuthenticationBackend struct {
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		} `json:"authentication_backend"`
		Description string `json:"description"`
	} `json:"proxy_auth_domain"`
	ClientcertAuthDomain struct {
		HTTPEnabled       bool  `json:"http_enabled"`
		TransportEnabled  *bool `json:"transport_enabled,omitempty"`
		Order             int   `json:"order"`
		HTTPAuthenticator struct {
			Challenge bool   `json:"challenge"`
			Type      string `json:"type"`
			Config    struct {
				UsernameAttribute string `json:"username_attribute"`
			} `json:"config"`
		} `json:"http_authenticator"`
		AuthenticationBackend struct {
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		} `json:"authentication_backend"`
		Description string `json:"description"`
	} `json:"clientcert_auth_domain"`
	KerberosAuthDomain struct {
		HTTPEnabled       bool  `json:"http_enabled"`
		TransportEnabled  *bool `json:"transport_enabled,omitempty"`
		Order             int   `json:"order"`
		HTTPAuthenticator struct {
			Challenge bool   `json:"challenge"`
			Type      string `json:"type"`
			Config    struct {
				KrbDebug                bool `json:"krb_debug"`
				StripRealmFromPrincipal bool `json:"strip_realm_from_principal"`
			} `json:"config"`
		} `json:"http_authenticator"`
		AuthenticationBackend struct {
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		} `json:"authentication_backend"`
	} `json:"kerberos_auth_domain"`
}

// ConfigDynamicAuthz is a sub type of ConfigDynamic containing security settings for Authz
type ConfigDynamicAuthz struct {
	RolesFromAnotherLdap struct {
		HTTPEnabled          bool  `json:"http_enabled"`
		TransportEnabled     *bool `json:"transport_enabled,omitempty"`
		AuthorizationBackend struct {
			Type   string          `json:"type"`
			Config json.RawMessage `json:"config"`
		} `json:"authorization_backend"`
		Description string `json:"description"`
	} `json:"roles_from_another_ldap"`
	RolesFromMyldap struct {
		HTTPEnabled          bool  `json:"http_enabled"`
		TransportEnabled     *bool `json:"transport_enabled,omitempty"`
		AuthorizationBackend struct {
			Type   string `json:"type"`
			Config struct {
				EnableSsl           bool     `json:"enable_ssl"`
				EnableStartTLS      bool     `json:"enable_start_tls"`
				EnableSslClientAuth bool     `json:"enable_ssl_client_auth"`
				VerifyHostnames     bool     `json:"verify_hostnames"`
				Hosts               []string `json:"hosts"`
				Rolebase            string   `json:"rolebase"`
				Rolesearch          string   `json:"rolesearch"`
				Userrolename        string   `json:"userrolename"`
				Rolename            string   `json:"rolename"`
				ResolveNestedRoles  bool     `json:"resolve_nested_roles"`
				Userbase            string   `json:"userbase"`
				Usersearch          string   `json:"usersearch"`
			} `json:"config"`
		} `json:"authorization_backend"`
		Description string `json:"description"`
	} `json:"roles_from_myldap"`
}
