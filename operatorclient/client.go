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
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var log = common.Log.WithFields(logrus.Fields{"package": "operatorclient"})

// ErrCapacityExhausted is returned when an operator responds with 409 Conflict,
// meaning it has no available slots for new analyses.
var ErrCapacityExhausted = errors.New("operator at capacity")

// Client communicates with a single vice-operator instance via HTTP.
type Client struct {
	name     string
	baseURL  *url.URL
	http     *http.Client
	username string
	password string
}

// NewClient creates a new operator Client from an OperatorConfig.
func NewClient(cfg OperatorConfig) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing operator URL %q: %w", cfg.URL, err)
	}
	return &Client{
		name:     cfg.Name,
		baseURL:  u,
		http:     &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)},
		username: cfg.Username,
		password: cfg.Password,
	}, nil
}

// setAuth adds basic auth credentials to the request when configured.
func (c *Client) setAuth(req *http.Request) {
	if c.username != "" {
		req.SetBasicAuth(c.username, c.password)
	}
}

// Name returns the operator's configured name.
func (c *Client) Name() string {
	return c.name
}

// Capacity queries the operator for its current cluster capacity.
func (c *Client) Capacity(ctx context.Context) (*CapacityResponse, error) {
	reqURL := c.baseURL.JoinPath("capacity")

	log.Infof("operator %s: GET %s", c.name, reqURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating capacity request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		log.Errorf("operator %s: capacity request failed: %v", c.name, err)
		return nil, fmt.Errorf("querying capacity: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort error body read
		log.Errorf("operator %s: capacity returned %d: %s", c.name, resp.StatusCode, string(body))
		return nil, fmt.Errorf("capacity returned %d: %s", resp.StatusCode, string(body))
	}

	var cap CapacityResponse
	if err := json.NewDecoder(resp.Body).Decode(&cap); err != nil {
		return nil, fmt.Errorf("decoding capacity response: %w", err)
	}

	log.Infof("operator %s: capacity response %d (running=%d, max=%d, available=%d)",
		c.name, resp.StatusCode, cap.RunningAnalyses, cap.MaxAnalyses, cap.AvailableSlots)

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

	log.Infof("operator %s: POST %s (analysis %s)", c.name, reqURL, bundle.AnalysisID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating launch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		log.Errorf("operator %s: launch request failed for analysis %s: %v", c.name, bundle.AnalysisID, err)
		return fmt.Errorf("sending launch request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusConflict {
		log.Infof("operator %s: launch returned 409 Conflict for analysis %s", c.name, bundle.AnalysisID)
		return ErrCapacityExhausted
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort error body read
		log.Errorf("operator %s: launch returned %d for analysis %s: %s", c.name, resp.StatusCode, bundle.AnalysisID, string(respBody))
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

	log.Infof("operator %s: DELETE %s (analysis %s)", c.name, u, analysisID)

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u.String(), nil)
	if err != nil {
		return err
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		log.Errorf("operator %s: exit request failed for analysis %s: %v", c.name, analysisID, err)
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort error body read
		log.Errorf("operator %s: exit returned %d for analysis %s: %s", c.name, resp.StatusCode, analysisID, string(body))
		return fmt.Errorf("exit returned %d: %s", resp.StatusCode, string(body))
	}

	log.Infof("operator %s: exit succeeded for analysis %s", c.name, analysisID)
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

	log.Infof("operator %s: POST %s (analysis %s, action %s)", c.name, reqURL, analysisID, action)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return err
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		log.Errorf("operator %s: %s request failed for analysis %s: %v", c.name, action, analysisID, err)
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort error body read
		log.Errorf("operator %s: %s returned %d for analysis %s: %s", c.name, action, resp.StatusCode, analysisID, string(body))
		return fmt.Errorf("%s returned %d: %s", action, resp.StatusCode, string(body))
	}

	log.Infof("operator %s: %s succeeded for analysis %s", c.name, action, analysisID)
	return nil
}

// Listing returns full resource info for all running VICE analyses from
// this operator's cluster.
func (c *Client) Listing(ctx context.Context) (*reporting.ResourceInfo, error) {
	reqURL := c.baseURL.JoinPath("analyses")

	log.Debugf("operator %s: GET %s (listing)", c.name, reqURL)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating listing request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("querying listing: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort error body read
		return nil, fmt.Errorf("listing returned %d: %s", resp.StatusCode, string(body))
	}

	var info reporting.ResourceInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding listing response: %w", err)
	}

	return &info, nil
}

// HasAnalysis checks whether this operator's cluster has the given analysis
// by calling the status endpoint and checking for deployments.
func (c *Client) HasAnalysis(ctx context.Context, analysisID string) (bool, error) {
	raw, err := c.getAnalysisJSON(ctx, analysisID, "status")
	if err != nil {
		return false, err
	}

	// Decode just enough to check for deployments.
	var status struct {
		Deployments []json.RawMessage `json:"deployments"`
	}
	if err := json.Unmarshal(raw, &status); err != nil {
		return false, fmt.Errorf("decoding status response: %w", err)
	}

	return len(status.Deployments) > 0, nil
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

	log.Debugf("operator %s: GET %s (analysis %s, query %s)", c.name, reqURL, analysisID, subpath)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		log.Errorf("operator %s: %s request failed for analysis %s: %v", c.name, subpath, analysisID, err)
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		log.Errorf("operator %s: %s returned %d for analysis %s: %s", c.name, subpath, resp.StatusCode, analysisID, string(body))
		return nil, fmt.Errorf("%s returned %d: %s", subpath, resp.StatusCode, string(body))
	}
	return json.RawMessage(body), nil
}
