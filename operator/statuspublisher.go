package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"time"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/messaging/v12"
)

// statusPublisherTimeout bounds a single POST to job-status-listener. The
// listener is expected to publish to AMQP and return quickly; anything longer
// suggests an unresponsive downstream and shouldn't stall the informer's event
// goroutine.
const statusPublisherTimeout = 10 * time.Second

// statusHTTPClient is used by StatusPublisher. Exposed as a package var so
// tests can substitute a client whose Transport redirects to an httptest.Server.
var statusHTTPClient = &http.Client{Timeout: statusPublisherTimeout}

// AnalysisStatus is the body shape expected by job-status-listener's
// POST /{external-id}/status endpoint. Matches vice-status-listener's
// AnalysisStatus so the listener doesn't need a new contract.
type AnalysisStatus struct {
	Host    string             `json:"Host"`
	State   messaging.JobState `json:"State"`
	Message string             `json:"Message"`
}

// StatusPublisher posts VICE analysis status updates to job-status-listener.
// One instance is shared across the operator process; safe for concurrent use.
type StatusPublisher struct {
	baseURL  *url.URL
	hostname string
	client   *http.Client
}

// NewStatusPublisher parses listenerURL and returns a publisher that posts
// updates to <listenerURL>/<external-id>/status. The hostname is sent as the
// AnalysisStatus.Host field so the downstream pipeline can identify which
// publisher emitted each update; pass the cluster name (or pod hostname) to
// disambiguate multi-cluster operators.
func NewStatusPublisher(listenerURL, hostname string) (*StatusPublisher, error) {
	u, err := url.Parse(listenerURL)
	if err != nil {
		return nil, fmt.Errorf("parsing status listener URL %q: %w", listenerURL, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("status listener URL %q must include scheme and host", listenerURL)
	}
	return &StatusPublisher{
		baseURL:  u,
		hostname: hostname,
		client:   statusHTTPClient,
	}, nil
}

// Publish posts a single status update for the given external ID. Returns an
// error if the request couldn't be sent or the listener returned a non-2xx/3xx
// response; the caller decides whether to retry or drop the update.
func (p *StatusPublisher) Publish(ctx context.Context, externalID constants.ExternalID, state messaging.JobState, message string) error {
	body, err := json.Marshal(AnalysisStatus{
		Host:    p.hostname,
		State:   state,
		Message: message,
	})
	if err != nil {
		return fmt.Errorf("marshalling status payload: %w", err)
	}

	target := *p.baseURL
	target.Path = path.Join(target.Path, string(externalID), "status")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("building status request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("posting %s status for %s to %s: %w", state, externalID, target.String(), err)
	}
	defer common.CloseBody(resp)

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 1024))
		if readErr != nil {
			return fmt.Errorf("status listener returned %d for %s (%s); body read failed: %w", resp.StatusCode, externalID, state, readErr)
		}
		return fmt.Errorf("status listener returned %d for %s (%s): %s", resp.StatusCode, externalID, state, string(respBody))
	}
	return nil
}
