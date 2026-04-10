// Package main implements the vice-operator-tool CLI for managing VICE
// operators via the app-exposer HTTP API.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// OperatorSummary mirrors db.OperatorSummary from the app-exposer API.
type OperatorSummary struct {
	Name          string `json:"name"`
	URL           string `json:"url"`
	TLSSkipVerify bool   `json:"tls_skip_verify"`
}

// AddOperatorRequest is the JSON body for creating a new operator.
type AddOperatorRequest struct {
	Name                  string `json:"name"`
	URL                   string `json:"url"`
	TLSSkipVerify         bool   `json:"tls_skip_verify"`
	AuthUser              string `json:"auth_user"`
	AuthPasswordEncrypted string `json:"auth_password_encrypted"`
}

// OperatorClient talks to the app-exposer operator admin API.
type OperatorClient struct {
	baseURL *url.URL
	http    *http.Client
}

// NewOperatorClient creates a client targeting the given app-exposer base URL
// using the provided HTTP client.
func NewOperatorClient(baseURL *url.URL, httpClient *http.Client) *OperatorClient {
	return &OperatorClient{
		baseURL: baseURL,
		http:    httpClient,
	}
}

// AddOperator creates a new operator via POST /vice/admin/operators.
func (c *OperatorClient) AddOperator(ctx context.Context, req *AddOperatorRequest) (*OperatorSummary, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	u := c.baseURL.JoinPath("vice", "admin", "operators")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusCreated {
		return nil, readError(resp)
	}

	var summary OperatorSummary
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &summary, nil
}

// ListOperators fetches all operators via GET /vice/admin/operators.
func (c *OperatorClient) ListOperators(ctx context.Context) ([]OperatorSummary, error) {
	u := c.baseURL.JoinPath("vice", "admin", "operators")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var ops []OperatorSummary
	if err := json.NewDecoder(resp.Body).Decode(&ops); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return ops, nil
}

// DeleteOperator removes an operator by name via DELETE /vice/admin/operators/name/{name}.
func (c *OperatorClient) DeleteOperator(ctx context.Context, name string) error {
	u := c.baseURL.JoinPath("vice", "admin", "operators", "name", name)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return fmt.Errorf("building request: %w", err)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}
	return nil
}

// aesKeySize is the byte length for AES-256 keys.
const aesKeySize = 32

// GenerateKey produces a base64-encoded AES-256 key suitable for use as
// the encryption.key configuration setting.
func GenerateKey() (string, error) {
	key := make([]byte, aesKeySize)
	if _, err := rand.Read(key); err != nil {
		return "", fmt.Errorf("generating random bytes: %w", err)
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

// maxErrBody caps the amount of data read from error response bodies to
// prevent unbounded memory allocation from misbehaving servers.
const maxErrBody = 4096

// readError reads an HTTP error response body and returns it as an error.
func readError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrBody)) //nolint:errcheck // best-effort read
	if len(body) > 0 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(body))
	}
	return fmt.Errorf("HTTP %d", resp.StatusCode)
}
