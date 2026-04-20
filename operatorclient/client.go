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
	"time"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/oauth2"
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

// NewClient creates a new operator Client from an OperatorConfig. When ts is
// non-nil, an oauth2.Transport is inserted into the transport chain so that
// every request carries a Bearer token (automatically refreshed). When
// cfg.TLSSkipVerify is true, TLS certificate verification is skipped — use
// only for development/testing with self-signed certs.
func NewClient(cfg OperatorConfig, ts oauth2.TokenSource) (*Client, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing operator URL %q: %w", cfg.URL, err)
	}

	transport := http.RoundTripper(http.DefaultTransport)
	if cfg.TLSSkipVerify {
		// Clone DefaultTransport to preserve connection pooling, timeouts, and
		// proxy settings — only override the TLS verification.
		dt := http.DefaultTransport.(*http.Transport).Clone()
		dt.TLSClientConfig.InsecureSkipVerify = true //nolint:gosec // intentional for dev/testing
		transport = dt
		log.Warnf("operator %q: TLS certificate verification disabled (tls_skip_verify)", cfg.Name)
	}

	// When a token source is provided, wrap the transport so every outgoing
	// request carries an Authorization: Bearer header. Token refresh is
	// handled automatically by oauth2.Transport.
	if ts != nil {
		transport = &oauth2.Transport{Source: ts, Base: transport}
	}

	return &Client{
		name:    cfg.Name,
		baseURL: u,
		http: &http.Client{
			Transport: otelhttp.NewTransport(transport),
			Timeout:   30 * time.Second,
		},
	}, nil
}

// doRequest builds and executes an HTTP request. Auth is handled by the
// transport chain (oauth2.Transport when configured). The caller is
// responsible for closing the response body.
func (c *Client) doRequest(ctx context.Context, method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

// checkStatus returns an error if the response status code is outside the
// 2xx range. The error includes the response body for debugging.
func checkStatus(resp *http.Response, label string) error {
	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort error body read
	return fmt.Errorf("%s returned %d: %s", label, resp.StatusCode, string(body))
}

// Name returns the operator's configured name.
func (c *Client) Name() string {
	return c.name
}

// Capacity queries the operator for its current cluster capacity.
func (c *Client) Capacity(ctx context.Context) (*CapacityResponse, error) {
	reqURL := c.baseURL.JoinPath("capacity").String()
	log.Infof("operator %s: GET %s", c.name, reqURL)

	resp, err := c.doRequest(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("querying capacity: %w", err)
	}
	defer common.CloseBody(resp)

	if err := checkStatus(resp, "capacity"); err != nil {
		log.Errorf("operator %s: %v", c.name, err)
		return nil, err
	}

	var cap CapacityResponse
	if err := json.NewDecoder(resp.Body).Decode(&cap); err != nil {
		return nil, fmt.Errorf("decoding capacity response: %w", err)
	}

	log.Infof("operator %s: capacity (running=%d, max=%d, available=%d)",
		c.name, cap.RunningAnalyses, cap.MaxAnalyses, cap.AvailableSlots)

	return &cap, nil
}

// Launch sends an AnalysisBundle to the operator for deployment.
// Returns ErrCapacityExhausted on 409 Conflict.
func (c *Client) Launch(ctx context.Context, bundle *AnalysisBundle) error {
	body, err := json.Marshal(bundle)
	if err != nil {
		return fmt.Errorf("marshalling bundle: %w", err)
	}

	reqURL := c.baseURL.JoinPath("analyses").String()
	log.Infof("operator %s: POST %s (analysis %s)", c.name, reqURL, bundle.AnalysisID)

	resp, err := c.doRequest(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("launch request for analysis %s: %w", bundle.AnalysisID, err)
	}
	defer common.CloseBody(resp)

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
func (c *Client) analysisURL(analysisID AnalysisID, subpath string) string {
	return c.baseURL.JoinPath("analyses", string(analysisID), subpath).String()
}

// Exit tells the operator to delete all resources for an analysis.
func (c *Client) Exit(ctx context.Context, analysisID AnalysisID) error {
	u := c.baseURL.JoinPath("analyses", string(analysisID)).String()
	log.Infof("operator %s: DELETE %s (analysis %s)", c.name, u, analysisID)

	resp, err := c.doRequest(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return fmt.Errorf("exit request for analysis %s: %w", analysisID, err)
	}
	defer common.CloseBody(resp)

	if err := checkStatus(resp, "exit"); err != nil {
		log.Errorf("operator %s: %v", c.name, err)
		return err
	}

	log.Infof("operator %s: exit succeeded for analysis %s", c.name, analysisID)
	return nil
}

// SaveAndExit tells the operator to save output files and then exit.
func (c *Client) SaveAndExit(ctx context.Context, analysisID AnalysisID) error {
	return c.postAnalysisAction(ctx, analysisID, "save-and-exit")
}

// DownloadInputFiles tells the operator to trigger input file downloads.
func (c *Client) DownloadInputFiles(ctx context.Context, analysisID AnalysisID) error {
	return c.postAnalysisAction(ctx, analysisID, "download-input-files")
}

// SaveOutputFiles tells the operator to trigger output file uploads.
func (c *Client) SaveOutputFiles(ctx context.Context, analysisID AnalysisID) error {
	return c.postAnalysisAction(ctx, analysisID, "save-output-files")
}

// postAnalysisAction POSTs to an analysis sub-endpoint.
func (c *Client) postAnalysisAction(ctx context.Context, analysisID AnalysisID, action string) error {
	reqURL := c.analysisURL(analysisID, action)

	log.Infof("operator %s: POST %s (analysis %s, action %s)", c.name, reqURL, analysisID, action)

	resp, err := c.doRequest(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return fmt.Errorf("%s request for analysis %s: %w", action, analysisID, err)
	}
	defer common.CloseBody(resp)

	if err := checkStatus(resp, action); err != nil {
		log.Errorf("operator %s: %v", c.name, err)
		return err
	}

	log.Infof("operator %s: %s succeeded for analysis %s", c.name, action, analysisID)
	return nil
}

// UpdatePermissions pushes a new allowed-users list to the operator's
// permissions ConfigMap for the given analysis.
func (c *Client) UpdatePermissions(ctx context.Context, analysisID AnalysisID, users []string) error {
	reqURL := c.analysisURL(analysisID, "permissions")

	log.Infof("operator %s: PUT %s (analysis %s, %d users)", c.name, reqURL, analysisID, len(users))

	body, err := json.Marshal(UpdatePermissionsRequest{AllowedUsers: users})
	if err != nil {
		return fmt.Errorf("marshalling permissions request: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPut, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("permissions request for analysis %s: %w", analysisID, err)
	}
	defer common.CloseBody(resp)

	if err := checkStatus(resp, "permissions"); err != nil {
		log.Errorf("operator %s: %v", c.name, err)
		return err
	}

	log.Infof("operator %s: permissions updated for analysis %s", c.name, analysisID)
	return nil
}

// ActiveSessions returns the list of currently active user sessions for an analysis.
func (c *Client) ActiveSessions(ctx context.Context, analysisID AnalysisID) (*ActiveSessionsResponse, error) {
	raw, err := c.getAnalysisJSON(ctx, analysisID, "active-sessions")
	if err != nil {
		return nil, err
	}

	var resp ActiveSessionsResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decoding active-sessions response: %w", err)
	}
	return &resp, nil
}

// LogoutUser invalidates all sessions for the given username in an analysis.
func (c *Client) LogoutUser(ctx context.Context, analysisID AnalysisID, username string) error {
	reqURL := c.analysisURL(analysisID, "logout-user")

	log.Infof("operator %s: POST %s (analysis %s, logout-user %s)", c.name, reqURL, analysisID, username)

	body, err := json.Marshal(LogoutUserRequest{Username: username})
	if err != nil {
		return fmt.Errorf("marshalling logout-user request: %w", err)
	}

	resp, err := c.doRequest(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("logout-user request for analysis %s: %w", analysisID, err)
	}
	defer common.CloseBody(resp)

	if err := checkStatus(resp, "logout-user"); err != nil {
		log.Errorf("operator %s: %v", c.name, err)
		return err
	}

	log.Infof("operator %s: logout-user succeeded for analysis %s (user %s)", c.name, analysisID, username)
	return nil
}

// Listing returns full resource info for all running VICE analyses from
// this operator's cluster.
func (c *Client) Listing(ctx context.Context, params url.Values) (*reporting.ResourceInfo, error) {
	reqURL, err := url.Parse(c.baseURL.JoinPath("analyses").String())
	if err != nil {
		return nil, err
	}
	reqURL.RawQuery = params.Encode()

	log.Debugf("operator %s: GET %s (listing)", c.name, reqURL.String())

	resp, err := c.doRequest(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("listing request: %w", err)
	}
	defer common.CloseBody(resp)

	log.Debugf("after GET %s", reqURL.String())

	if err := checkStatus(resp, "listing"); err != nil {
		return nil, err
	}

	var info reporting.ResourceInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("decoding listing response: %w", err)
	}

	return &info, nil
}

// HasAnalysis checks whether this operator's cluster has the given analysis
// by calling the status endpoint and checking for deployments.
func (c *Client) HasAnalysis(ctx context.Context, analysisID AnalysisID) (bool, error) {
	resp, err := c.Status(ctx, analysisID)
	if err != nil {
		return false, err
	}
	return len(resp.Deployments) > 0, nil
}

// Status queries the resource status for an analysis from the operator.
func (c *Client) Status(ctx context.Context, analysisID AnalysisID) (*StatusResponse, error) {
	raw, err := c.getAnalysisJSON(ctx, analysisID, "status")
	if err != nil {
		return nil, err
	}
	var resp StatusResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decoding status response for analysis %s: %w", analysisID, err)
	}
	return &resp, nil
}

// URLReady checks whether the analysis is ready for user access.
func (c *Client) URLReady(ctx context.Context, analysisID AnalysisID) (*URLReadyResponse, error) {
	raw, err := c.getAnalysisJSON(ctx, analysisID, "url-ready")
	if err != nil {
		return nil, err
	}
	var resp URLReadyResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decoding url-ready response for analysis %s: %w", analysisID, err)
	}
	return &resp, nil
}

// Pods returns pod info for an analysis from the operator.
func (c *Client) Pods(ctx context.Context, analysisID AnalysisID) ([]StatusPod, error) {
	raw, err := c.getAnalysisJSON(ctx, analysisID, "pods")
	if err != nil {
		return nil, err
	}
	var pods []StatusPod
	if err := json.Unmarshal(raw, &pods); err != nil {
		return nil, fmt.Errorf("decoding pods response for analysis %s: %w", analysisID, err)
	}
	return pods, nil
}

// Logs returns container logs for an analysis from the operator. The
// operator returns a single reporting.VICELogEntry (the first pod's
// logs) — see HandleLogs for the reasoning.
func (c *Client) Logs(ctx context.Context, analysisID AnalysisID, params url.Values) (*reporting.VICELogEntry, error) {
	reqURL, err := url.Parse(c.analysisURL(analysisID, "logs"))
	if err != nil {
		return nil, err
	}
	reqURL.RawQuery = params.Encode()

	log.Debugf("operator %s: GET %s (analysis %s, logs)", c.name, reqURL.String(), analysisID)

	resp, err := c.doRequest(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("logs request for analysis %s: %w", analysisID, err)
	}
	defer common.CloseBody(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading logs response for analysis %s: %w", analysisID, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		err := fmt.Errorf("logs returned %d: %s", resp.StatusCode, string(body))
		log.Errorf("operator %s: %v", c.name, err)
		return nil, err
	}

	var entry reporting.VICELogEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, fmt.Errorf("decoding logs response for analysis %s: %w", analysisID, err)
	}
	return &entry, nil
}

// getAnalysisJSON GETs a JSON response from an analysis sub-endpoint.
func (c *Client) getAnalysisJSON(ctx context.Context, analysisID AnalysisID, subpath string) (json.RawMessage, error) {
	reqURL := c.analysisURL(analysisID, subpath)
	log.Debugf("operator %s: GET %s (analysis %s, query %s)", c.name, reqURL, analysisID, subpath)

	resp, err := c.doRequest(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%s request for analysis %s: %w", subpath, analysisID, err)
	}
	defer common.CloseBody(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading %s response for analysis %s: %w", subpath, analysisID, err)
	}

	// Check status after reading body — checkStatus would try to re-read
	// the already-consumed body, producing empty error messages.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		err := fmt.Errorf("%s returned %d: %s", subpath, resp.StatusCode, string(body))
		log.Errorf("operator %s: %v", c.name, err)
		return nil, err
	}

	return json.RawMessage(body), nil
}
