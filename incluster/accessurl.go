package incluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cyverse-de/app-exposer/constants"
)

// noRedirectClient is used by CheckAccessURL to verify the public URL is
// reachable without chasing Keycloak login redirects.
var noRedirectClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// frontendURLResponse is the JSON structure returned by vice-proxy's
// /frontend-url endpoint.
type frontendURLResponse struct {
	URL string `json:"url"`
}

// GetAccessURL contacts the vice-proxy sidecar for the given external-id
// through its in-cluster Service and returns the full frontend URL. Returns
// an empty string and error if vice-proxy is unreachable.
func (i *Incluster) GetAccessURL(ctx context.Context, externalID constants.ExternalID) (string, error) {
	svcName := fmt.Sprintf("vice-%s", externalID)
	endpoint := fmt.Sprintf(
		"http://%s.%s.svc.cluster.local:%d/frontend-url",
		svcName,
		i.ViceNamespace,
		constants.VICEProxyServicePort,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("building request to vice-proxy at %s: %w", endpoint, err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("contacting vice-proxy at %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("vice-proxy at %s returned status %d", endpoint, resp.StatusCode)
	}

	var result frontendURLResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding vice-proxy response from %s: %w", endpoint, err)
	}

	if result.URL == "" {
		return "", fmt.Errorf("vice-proxy at %s returned empty URL", endpoint)
	}

	return result.URL, nil
}

// CheckAccessURL verifies the full public URL is live and accessible.
// Redirects are not followed since vice-proxy redirects unauthenticated
// requests to Keycloak; a response in [200-399] indicates the URL is up.
func (i *Incluster) CheckAccessURL(ctx context.Context, accessURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, accessURL, nil)
	if err != nil {
		return fmt.Errorf("building request to %s: %w", accessURL, err)
	}

	resp, err := noRedirectClient.Do(req)
	if err != nil {
		return fmt.Errorf("checking access URL %s: %w", accessURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("access URL %s returned status %d", accessURL, resp.StatusCode)
	}

	return nil
}
