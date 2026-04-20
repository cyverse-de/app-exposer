// Package main implements the vice-operator-tool CLI for managing VICE
// operators via the app-exposer HTTP API.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/operatorclient"
)

// OperatorClient talks to the app-exposer operator admin API using
// operatorclient.OperatorConfig as the canonical on-the-wire shape.
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
func (c *OperatorClient) AddOperator(ctx context.Context, req *operatorclient.OperatorConfig) (*operatorclient.OperatorConfig, error) {
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
	defer common.CloseBody(resp)

	if resp.StatusCode != http.StatusCreated {
		return nil, readError(resp)
	}

	var summary operatorclient.OperatorConfig
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &summary, nil
}

// ListOperators fetches all operators via GET /vice/admin/operators.
func (c *OperatorClient) ListOperators(ctx context.Context) ([]operatorclient.OperatorConfig, error) {
	u := c.baseURL.JoinPath("vice", "admin", "operators")
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer common.CloseBody(resp)

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var ops []operatorclient.OperatorConfig
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
	defer common.CloseBody(resp)

	if resp.StatusCode != http.StatusOK {
		return readError(resp)
	}
	return nil
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
