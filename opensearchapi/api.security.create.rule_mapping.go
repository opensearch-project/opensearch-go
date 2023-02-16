package opensearchapi

import (
	"context"
	"io"
	"net/http"
	"strings"
)

func newCreateSecurityRuleMappingFunc(t Transport) CreateSecurityRuleMapping {
	return func(name string, body io.Reader, o ...func(*CreateSecurityRuleMappingRequest)) (*Response, error) {
		var r = CreateSecurityRuleMappingRequest{Name: name, Body: body}
		for _, f := range o {
			f(&r)
		}
		return r.Do(r.ctx, t)
	}
}

// ----- API Definition -------------------------------------------------------

// CreateSecurityRuleMapping creates a role mapping
//
//	To use this API, you must have at least the manage_security cluster privilege.
type CreateSecurityRuleMapping func(name string, body io.Reader, o ...func(*CreateSecurityRuleMappingRequest)) (*Response, error)

// CreateSecurityRuleMappingRequest configures the Create Security Rule Mapping API request.
type CreateSecurityRuleMappingRequest struct {
	Name string

	Body io.Reader

	Pretty     bool
	Human      bool
	ErrorTrace bool
	FilterPath []string

	Header http.Header

	ctx context.Context
}

// Do will execute the request and returns response or error.
func (r CreateSecurityRuleMappingRequest) Do(ctx context.Context, transport Transport) (*Response, error) {
	var (
		method string
		path   strings.Builder
		params map[string]string
	)

	method = http.MethodPost

	path.Grow(len("/_security/role_mapping/") + len(r.Name))
	path.WriteString("/_security/role_mapping/")
	path.WriteString(r.Name)

	params = make(map[string]string)
	if r.Pretty {
		params["pretty"] = "true"
	}

	if r.Human {
		params["human"] = "true"
	}

	if r.ErrorTrace {
		params["error_trace"] = "true"
	}

	if len(r.FilterPath) > 0 {
		params["filter_path"] = strings.Join(r.FilterPath, ",")
	}

	req, err := newRequest(method, path.String(), r.Body)
	if err != nil {
		return nil, err
	}

	if len(params) > 0 {
		q := req.URL.Query()
		for k, v := range params {
			q.Set(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}

	if len(r.Header) > 0 {
		if len(req.Header) == 0 {
			req.Header = r.Header
		} else {
			for k, vv := range r.Header {
				for _, v := range vv {
					req.Header.Add(k, v)
				}
			}
		}
	}

	if ctx != nil {
		req = req.WithContext(ctx)
	}

	res, err := transport.Perform(req)
	if err != nil {
		return nil, err
	}

	response := Response{
		StatusCode: res.StatusCode,
		Body:       res.Body,
		Header:     res.Header,
	}

	return &response, nil
}

// WithContext sets the request context.
func (f CreateSecurityRuleMapping) WithContext(v context.Context) func(*CreateSecurityRuleMappingRequest) {
	return func(r *CreateSecurityRuleMappingRequest) {
		r.ctx = v
	}
}

// WithPretty makes the response body pretty-printed.
func (f CreateSecurityRuleMapping) WithPretty() func(*CreateSecurityRuleMappingRequest) {
	return func(r *CreateSecurityRuleMappingRequest) {
		r.Pretty = true
	}
}

// WithHuman makes statistical values human-readable.
func (f CreateSecurityRuleMapping) WithHuman() func(*CreateSecurityRuleMappingRequest) {
	return func(r *CreateSecurityRuleMappingRequest) {
		r.Human = true
	}
}

// WithErrorTrace includes the stack trace for errors in the response body.
func (f CreateSecurityRuleMapping) WithErrorTrace() func(*CreateSecurityRuleMappingRequest) {
	return func(r *CreateSecurityRuleMappingRequest) {
		r.ErrorTrace = true
	}
}

// WithFilterPath filters the properties of the response body.
func (f CreateSecurityRuleMapping) WithFilterPath(v ...string) func(*CreateSecurityRuleMappingRequest) {
	return func(r *CreateSecurityRuleMappingRequest) {
		r.FilterPath = v
	}
}

// WithHeader adds the headers to the HTTP request.
func (f CreateSecurityRuleMapping) WithHeader(h map[string]string) func(*CreateSecurityRuleMappingRequest) {
	return func(r *CreateSecurityRuleMappingRequest) {
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		for k, v := range h {
			r.Header.Add(k, v)
		}
	}
}

// WithOpaqueID adds the X-Opaque-Id header to the HTTP request.
func (f CreateSecurityRuleMapping) WithOpaqueID(s string) func(*CreateSecurityRuleMappingRequest) {
	return func(r *CreateSecurityRuleMappingRequest) {
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		r.Header.Set("X-Opaque-Id", s)
	}
}
