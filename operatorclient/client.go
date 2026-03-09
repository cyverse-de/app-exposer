package operatorclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var log = common.Log.WithFields(logrus.Fields{"package": "operatorclient"})

// ErrCapacityExhausted is returned when an operator responds with 409 Conflict,
// meaning it has no available slots for new analyses.
var ErrCapacityExhausted = errors.New("operator at capacity")

// Client communicates with a single vice-operator instance via HTTP.
type Client struct {
	name    string
	baseURL *url.URL
	http    *http.Client
}

// NewClient creates a new operator Client from an OperatorConfig.
func NewClient(cfg OperatorConfig) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing operator URL %q: %w", cfg.URL, err)
	}
	return &Client{
		name:    cfg.Name,
		baseURL: u,
		http:    &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
	}, nil
}

// Name returns the operator's configured name.
func (c *Client) Name() string {
	return c.name
}

// Capacity queries the operator for its current cluster capacity.
func (c *Client) Capacity(ctx context.Context) (*CapacityResponse, error) {
	reqURL := c.baseURL.JoinPath("capacity")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating capacity request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying capacity: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("capacity returned %d: %s", resp.StatusCode, string(body))
	}

	var cap CapacityResponse
	if err := json.NewDecoder(resp.Body).Decode(&cap); err != nil {
		return nil, fmt.Errorf("decoding capacity response: %w", err)
	}
	return &cap, nil
}

// Launch sends an AnalysisBundle to the operator for deployment.
// Returns ErrCapacityExhausted on 409 Conflict.
func (c *Client) Launch(ctx context.Context, bundle *AnalysisBundle) error {
	body, err := json.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("marshalling bundle: %w", err)
	}

	reqURL := c.baseURL.JoinPath("analyses")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating launch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("sending launch request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusConflict {
		return ErrCapacityExhausted
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("launch returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// analysisURL builds a URL for an analysis-scoped endpoint.
func (c *Client) analysisURL(analysisID, subpath string) string {
	return c.baseURL.JoinPath("analyses", analysisID, subpath).String()
}

// Exit tells the operator to delete all resources for an analysis.
func (c *Client) Exit(ctx context.Context, analysisID string) error {
	u := c.baseURL.JoinPath("analyses", analysisID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("exit returned %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// SaveAndExit tells the operator to save output files and then exit.
func (c *Client) SaveAndExit(ctx context.Context, analysisID string) error {
	return c.postAnalysisAction(ctx, analysisID, "save-and-exit")
}

// DownloadInputFiles tells the operator to trigger input file downloads.
func (c *Client) DownloadInputFiles(ctx context.Context, analysisID string) error {
	return c.postAnalysisAction(ctx, analysisID, "download-input-files")
}

// SaveOutputFiles tells the operator to trigger output file uploads.
func (c *Client) SaveOutputFiles(ctx context.Context, analysisID string) error {
	return c.postAnalysisAction(ctx, analysisID, "save-output-files")
}

// postAnalysisAction POSTs to an analysis sub-endpoint.
func (c *Client) postAnalysisAction(ctx context.Context, analysisID, action string) error {
	reqURL := c.analysisURL(analysisID, action)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s returned %d: %s", action, resp.StatusCode, string(body))
	}
	return nil
}

// Status queries the resource status for an analysis from the operator.
func (c *Client) Status(ctx context.Context, analysisID string) (json.RawMessage, error) {
	return c.getAnalysisJSON(ctx, analysisID, "status")
}

// URLReady checks whether the analysis is ready for user access.
func (c *Client) URLReady(ctx context.Context, analysisID string) (json.RawMessage, error) {
	return c.getAnalysisJSON(ctx, analysisID, "url-ready")
}

// Pods returns pod info for an analysis from the operator.
func (c *Client) Pods(ctx context.Context, analysisID string) (json.RawMessage, error) {
	return c.getAnalysisJSON(ctx, analysisID, "pods")
}

// Logs returns container logs for an analysis from the operator.
func (c *Client) Logs(ctx context.Context, analysisID string) (json.RawMessage, error) {
	return c.getAnalysisJSON(ctx, analysisID, "logs")
}

// getAnalysisJSON GETs a JSON response from an analysis sub-endpoint.
func (c *Client) getAnalysisJSON(ctx context.Context, analysisID, subpath string) (json.RawMessage, error) {
	reqURL := c.analysisURL(analysisID, subpath)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("%s returned %d: %s", subpath, resp.StatusCode, string(body))
	}
	return json.RawMessage(body), nil
}
