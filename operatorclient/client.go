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

// UpdatePermissions pushes a new allowed-users list to the operator's
// permissions ConfigMap for the given analysis.
func (c *Client) UpdatePermissions(ctx context.Context, analysisID string, users []string) error {
	reqURL := c.analysisURL(analysisID, "permissions")

	log.Infof("operator %s: PUT %s (analysis %s, %d users)", c.name, reqURL, analysisID, len(users))

	body, err := json.Marshal(map[string][]string{"allowedUsers": users})
	if err != nil {
		return fmt.Errorf("marshalling permissions request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating permissions request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		log.Errorf("operator %s: permissions request failed for analysis %s: %v", c.name, analysisID, err)
		return fmt.Errorf("sending permissions request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort error body read
		log.Errorf("operator %s: permissions returned %d for analysis %s: %s", c.name, resp.StatusCode, analysisID, string(respBody))
		return fmt.Errorf("permissions returned %d: %s", resp.StatusCode, string(respBody))
	}

	log.Infof("operator %s: permissions updated for analysis %s", c.name, analysisID)
	return nil
}

// BackChannelLogout forwards a Keycloak back-channel logout token to the
// operator, which relays it to the vice-proxy sidecar for the given analysis.
func (c *Client) BackChannelLogout(ctx context.Context, analysisID, logoutToken string) error {
	reqURL := c.analysisURL(analysisID, "backchannel-logout")

	log.Infof("operator %s: POST %s (analysis %s, backchannel-logout)", c.name, reqURL, analysisID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL,
		bytes.NewReader([]byte(url.Values{"logout_token": {logoutToken}}.Encode())))
	if err != nil {
		return fmt.Errorf("creating backchannel-logout request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		log.Errorf("operator %s: backchannel-logout request failed for analysis %s: %v", c.name, analysisID, err)
		return fmt.Errorf("sending backchannel-logout request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort error body read
		log.Errorf("operator %s: backchannel-logout returned %d for analysis %s: %s", c.name, resp.StatusCode, analysisID, string(respBody))
		return fmt.Errorf("backchannel-logout returned %d: %s", resp.StatusCode, string(respBody))
	}

	log.Infof("operator %s: backchannel-logout forwarded for analysis %s", c.name, analysisID)
	return nil
}

// Logout forwards a logout request to the operator, which relays it to the
// vice-proxy sidecar. Returns the Keycloak logout redirect URL.
func (c *Client) Logout(ctx context.Context, analysisID string) (*LogoutResponse, error) {
	reqURL := c.analysisURL(analysisID, "logout")

	log.Infof("operator %s: POST %s (analysis %s, logout)", c.name, reqURL, analysisID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("creating logout request: %w", err)
	}
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		log.Errorf("operator %s: logout request failed for analysis %s: %v", c.name, analysisID, err)
		return nil, fmt.Errorf("sending logout request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort error body read
		log.Errorf("operator %s: logout returned %d for analysis %s: %s", c.name, resp.StatusCode, analysisID, string(respBody))
		return nil, fmt.Errorf("logout returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result LogoutResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding logout response: %w", err)
	}

	log.Infof("operator %s: logout forwarded for analysis %s", c.name, analysisID)
	return &result, nil
}

// ActiveSessions returns the list of currently active user sessions for an analysis.
func (c *Client) ActiveSessions(ctx context.Context, analysisID string) (*ActiveSessionsResponse, error) {
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
func (c *Client) LogoutUser(ctx context.Context, analysisID, username string) error {
	reqURL := c.analysisURL(analysisID, "logout-user")

	log.Infof("operator %s: POST %s (analysis %s, logout-user %s)", c.name, reqURL, analysisID, username)

	body, err := json.Marshal(LogoutUserRequest{Username: username})
	if err != nil {
		return fmt.Errorf("marshalling logout-user request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("creating logout-user request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		log.Errorf("operator %s: logout-user request failed for analysis %s: %v", c.name, analysisID, err)
		return fmt.Errorf("sending logout-user request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		respBody, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort error body read
		log.Errorf("operator %s: logout-user returned %d for analysis %s: %s", c.name, resp.StatusCode, analysisID, string(respBody))
		return fmt.Errorf("logout-user returned %d: %s", resp.StatusCode, string(respBody))
	}

	log.Infof("operator %s: logout-user succeeded for analysis %s (user %s)", c.name, analysisID, username)
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
