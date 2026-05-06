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
	"github.com/google/uuid"
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

// AddOperator creates a new operator via POST /vice/admin/operators. The
// returned summary includes the server-assigned UUID, so callers can
// PATCH/DELETE the row by id without an extra list call.
func (c *OperatorClient) AddOperator(ctx context.Context, req *operatorclient.OperatorConfig) (*operatorclient.OperatorAdminSummary, error) {
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

	var summary operatorclient.OperatorAdminSummary
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &summary, nil
}

// ListOperators fetches all operators via GET /vice/admin/operators. The
// admin endpoint returns each operator's UUID alongside the public config
// fields so the CLI can resolve a user-supplied name to an id for PATCH.
func (c *OperatorClient) ListOperators(ctx context.Context) ([]operatorclient.OperatorAdminSummary, error) {
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

	var ops []operatorclient.OperatorAdminSummary
	if err := json.NewDecoder(resp.Body).Decode(&ops); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return ops, nil
}

// UpdateOperator partially updates the operator with the given id via
// PATCH /vice/admin/operators/id/{id}. Only fields with non-nil pointers
// in req are sent (json:omitempty handles the omission), matching the
// server's COALESCE-based partial-update semantics. The response carries
// the row's id alongside the updated fields so callers can confirm the
// post-update state without a follow-up list call.
func (c *OperatorClient) UpdateOperator(ctx context.Context, id uuid.UUID, req *operatorclient.UpdateOperatorRequest) (*operatorclient.OperatorAdminSummary, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	u := c.baseURL.JoinPath("vice", "admin", "operators", "id", id.String())
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPatch, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("sending request: %w", err)
	}
	defer common.CloseBody(resp)

	if resp.StatusCode != http.StatusOK {
		return nil, readError(resp)
	}

	var summary operatorclient.OperatorAdminSummary
	if err := json.NewDecoder(resp.Body).Decode(&summary); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}
	return &summary, nil
}

// DeleteOperator removes an operator by id via DELETE
// /vice/admin/operators/id/{id}. The endpoint is idempotent: deleting a
// non-existent UUID returns 200 silently. The CLI typically resolves a
// user-supplied name to an id by calling ListOperators first.
func (c *OperatorClient) DeleteOperator(ctx context.Context, id uuid.UUID) error {
	u := c.baseURL.JoinPath("vice", "admin", "operators", "id", id.String())
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
