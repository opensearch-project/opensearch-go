// SPDX-License-Identifier: Apache-2.0
//
// The OpenSearch Contributors require contributions made to
// this file be licensed under the Apache-2.0 license or a
// compatible open source license.

package opensearch

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// Error vars
var (
	ErrUnexpectedEmptyBody    = errors.New("body is unexpectedly empty")
	ErrReadBody               = errors.New("failed to read body")
	ErrJSONUnmarshalBody      = errors.New("failed to json unmarshal body")
	ErrUnknownOpensearchError = errors.New("opensearch error response could not be parsed as error")
	ErrNonJSONError           = errors.New("error is not in json format")
	ErrUnauthorized           = errors.New(http.StatusText(http.StatusUnauthorized))
)

// Error represents an Opensearch error with only an error field
type Error struct {
	Err string `json:"error"`
}

// Error returns a string
func (e Error) Error() string {
	return fmt.Sprintf("error: %s", e.Err)
}

// StringError represnets an Opensearch error where error is a string
type StringError struct {
	Err    string `json:"error"`
	Status int    `json:"status"`
}

// Error returns a string
func (e StringError) Error() string {
	return fmt.Sprintf("status: %d, error: %s", e.Status, e.Err)
}

// ReasonError represents an Opensearch error with a reason field
type ReasonError struct {
	Reason string `json:"reason"`
	Status string `json:"status"`
}

// Error returns a string
func (e ReasonError) Error() string {
	return fmt.Sprintf("status: %s, reason: %s", e.Status, e.Reason)
}

// MessageError represents an Opensearch error with a message field
type MessageError struct {
	Message string `json:"message"`
	Status  string `json:"status"`
}

// Error returns a string
func (e MessageError) Error() string {
	return fmt.Sprintf("status: %s, message: %s", e.Status, e.Message)
}

// StructError represents an Opensearch error with a detailed error struct
type StructError struct {
	Err    Err `json:"error"`
	Status int `json:"status"`
}

// Err represents the error of an API error response
type Err struct {
	RootCause []RootCause `json:"root_cause"`
	Type      string      `json:"type"`
	Reason    string      `json:"reason"`
	Index     string      `json:"index,omitempty"`
	IndexUUID string      `json:"index_uuid,omitempty"`
}

// RootCause represents the root_cause of an API error response
type RootCause struct {
	Type      string `json:"type"`
	Reason    string `json:"reason"`
	Index     string `json:"index,omitempty"`
	IndexUUID string `json:"index_uuid,omitempty"`
}

// Error returns a string
func (e StructError) Error() string {
	return fmt.Sprintf("status: %d, type: %s, reason: %s, root_cause: %s", e.Status, e.Err.Type, e.Err.Reason, e.Err.RootCause)
}

// UnmarshalJSON is a custom unmarshal function for StructError returning custom errors in special cases
func (e *StructError) UnmarshalJSON(b []byte) error {
	var dummy struct {
		Err    json.RawMessage `json:"error"`
		Status int             `json:"status"`
	}
	if err := json.Unmarshal(b, &dummy); err != nil {
		return err
	}

	var osErr Err
	if err := json.Unmarshal(dummy.Err, &osErr); err != nil {
		return &StringError{Status: dummy.Status, Err: string(dummy.Err)}
	}
	if dummy.Status == 0 || (osErr.Type == "" && osErr.Reason == "") {
		return fmt.Errorf("%w: %s", ErrUnknownOpensearchError, b)
	}

	e.Err = osErr
	e.Status = dummy.Status

	return nil
}

// PraseError tries to parse the opensearch api error into an custom error
func ParseError(resp *Response) error {
	if resp.Body == nil {
		return fmt.Errorf("%w, status: %s", ErrUnexpectedEmptyBody, resp.Status())
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrReadBody, err)
	}

	if resp.StatusCode == http.StatusMethodNotAllowed {
		var apiError StringError
		if err = json.Unmarshal(body, &apiError); err != nil {
			return fmt.Errorf("%w: %w", ErrJSONUnmarshalBody, err)
		}
		return apiError
	}

	// ToDo: Parse 404 errors separate as they are not in one standard format
	// https://github.com/opensearch-project/OpenSearch/issues/9988

	var apiError Error
	if err = json.Unmarshal(body, &apiError); err != nil {
		return fmt.Errorf("%w: %w", ErrJSONUnmarshalBody, err)
	}

	return apiError
}
