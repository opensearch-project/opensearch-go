package opensearchapi

import (
	"context"
	"io"
	"net/http"
	"strings"
)

func newBulkPatchSecurityRoleMappingFunc(t Transport) BulkPatchSecurityRoleMapping {
	return func(body io.Reader, o ...func(*BulkPatchSecurityRoleMappingRequest)) (*Response, error) {
		var r = BulkPatchSecurityRoleMappingRequest{Body: body}
		for _, f := range o {
			f(&r)
		}
		return r.Do(r.ctx, t)
	}
}

// ----- API Definition -------------------------------------------------------

// BulkPatchSecurityRoleMapping Bulk Patch multiple role mappings
//
//	To use this API, you must have at least the manage_security cluster privilege.
//		https://opensearch.org/docs/2.3/security/access-control/api/#BulkPatch-role-mapping
type BulkPatchSecurityRoleMapping func(body io.Reader, o ...func(*BulkPatchSecurityRoleMappingRequest)) (*Response, error)

// BulkPatchSecurityRoleMappingRequest configures the BulkPatch Security Rule Mapping API request.
type BulkPatchSecurityRoleMappingRequest struct {
	Body io.Reader

	Pretty     bool
	Human      bool
	ErrorTrace bool
	FilterPath []string

	Header http.Header

	ctx context.Context
}

// Do will execute the request and returns response or error.
func (r BulkPatchSecurityRoleMappingRequest) Do(ctx context.Context, transport Transport) (*Response, error) {
	var (
		method string
		path   strings.Builder
		params map[string]string
	)

	method = http.MethodPatch

	path.Grow(len("/_plugins/_security/api/rolesmapping"))
	path.WriteString("/_plugins/_security/api/rolesmapping")

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
func (f BulkPatchSecurityRoleMapping) WithContext(v context.Context) func(*BulkPatchSecurityRoleMappingRequest) {
	return func(r *BulkPatchSecurityRoleMappingRequest) {
		r.ctx = v
	}
}

// WithPretty makes the response body pretty-printed.
func (f BulkPatchSecurityRoleMapping) WithPretty() func(*BulkPatchSecurityRoleMappingRequest) {
	return func(r *BulkPatchSecurityRoleMappingRequest) {
		r.Pretty = true
	}
}

// WithHuman makes statistical values human-readable.
func (f BulkPatchSecurityRoleMapping) WithHuman() func(*BulkPatchSecurityRoleMappingRequest) {
	return func(r *BulkPatchSecurityRoleMappingRequest) {
		r.Human = true
	}
}

// WithErrorTrace includes the stack trace for errors in the response body.
func (f BulkPatchSecurityRoleMapping) WithErrorTrace() func(*BulkPatchSecurityRoleMappingRequest) {
	return func(r *BulkPatchSecurityRoleMappingRequest) {
		r.ErrorTrace = true
	}
}

// WithFilterPath filters the properties of the response body.
func (f BulkPatchSecurityRoleMapping) WithFilterPath(v ...string) func(*BulkPatchSecurityRoleMappingRequest) {
	return func(r *BulkPatchSecurityRoleMappingRequest) {
		r.FilterPath = v
	}
}

// WithHeader adds the headers to the HTTP request.
func (f BulkPatchSecurityRoleMapping) WithHeader(h map[string]string) func(*BulkPatchSecurityRoleMappingRequest) {
	return func(r *BulkPatchSecurityRoleMappingRequest) {
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		for k, v := range h {
			r.Header.Add(k, v)
		}
	}
}

// WithOpaqueID adds the X-Opaque-Id header to the HTTP request.
func (f BulkPatchSecurityRoleMapping) WithOpaqueID(s string) func(*BulkPatchSecurityRoleMappingRequest) {
	return func(r *BulkPatchSecurityRoleMappingRequest) {
		if r.Header == nil {
			r.Header = make(http.Header)
		}
		r.Header.Set("X-Opaque-Id", s)
	}
}
