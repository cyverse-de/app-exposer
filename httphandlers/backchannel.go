package httphandlers

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
)

// logoutHTTPClient is used for forwarding logout requests to vice-proxy
// sidecars. It has a short timeout to avoid blocking the caller.
var logoutHTTPClient = &http.Client{Timeout: 5 * time.Second}

// JWKSCache caches JWKS keys from Keycloak
type JWKSCache struct {
	jwksURL     string
	keys        map[string]*rsa.PublicKey
	lastFetched time.Time
	cacheTTL    time.Duration
	mu          sync.RWMutex
}

// NewJWKSCache creates a new JWKS cache
func NewJWKSCache(jwksURL string) *JWKSCache {
	return &JWKSCache{
		jwksURL:  jwksURL,
		keys:     make(map[string]*rsa.PublicKey),
		cacheTTL: 5 * time.Minute,
	}
}

// GetKey retrieves a public key by kid, fetching from Keycloak if needed.
func (c *JWKSCache) GetKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	if time.Since(c.lastFetched) < c.cacheTTL {
		if key, ok := c.keys[kid]; ok {
			c.mu.RUnlock()
			return key, nil
		}
	}
	c.mu.RUnlock()

	// Fetch fresh keys from the JWKS endpoint.
	if err := c.refresh(ctx); err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if key, ok := c.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("key %s not found in JWKS", kid)
}

// refresh fetches the JWKS key set from the configured endpoint and updates
// the cache. Only RSA keys are kept, and the cache is only updated when at
// least one valid key was parsed.
func (c *JWKSCache) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("creating JWKS request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetching JWKS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned HTTP %d", resp.StatusCode)
	}

	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("decoding JWKS response: %w", err)
	}

	newKeys := make(map[string]*rsa.PublicKey)
	for _, key := range jwks.Keys {
		// Only process RSA keys.
		if key.Kty != "RSA" {
			continue
		}

		nBytes, err := base64.RawURLEncoding.DecodeString(key.N)
		if err != nil {
			log.Warnf("failed to decode N for key %s: %v", key.Kid, err)
			continue
		}
		eBytes, err := base64.RawURLEncoding.DecodeString(key.E)
		if err != nil {
			log.Warnf("failed to decode E for key %s: %v", key.Kid, err)
			continue
		}

		n := new(big.Int).SetBytes(nBytes)
		e := int(new(big.Int).SetBytes(eBytes).Int64())
		newKeys[key.Kid] = &rsa.PublicKey{N: n, E: e}
	}

	if len(newKeys) == 0 {
		return fmt.Errorf("JWKS response contained no valid RSA keys")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.keys = newKeys
	c.lastFetched = time.Now()
	return nil
}

// HandleBackChannelLogout handles back-channel logout requests from Keycloak
func (h *HTTPHandlers) HandleBackChannelLogout(c echo.Context) error {
	ctx := c.Request().Context()

	logoutToken := c.FormValue("logout_token")
	if logoutToken == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "logout_token is required")
	}

	// Parse and validate the token
	userID, err := h.validateLogoutToken(ctx, logoutToken)
	if err != nil {
		log.Errorf("invalid logout token: %v", err)
		return echo.NewHTTPError(http.StatusBadRequest, "invalid logout token")
	}

	// Find all VICE deployments for this user.
	deployments, err := h.incluster.GetDeploymentsByUserID(ctx, userID)
	if err != nil {
		log.Errorf("error finding deployments for user %s: %v", userID, err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to process logout")
	}

	log.Infof("forwarding logout to %d deployments for user %s", len(deployments), userID)

	// Forward logout to each vice-proxy and wait for all to complete.
	var wg sync.WaitGroup
	var successCount atomic.Int32
	for _, dep := range deployments {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := h.forwardLogoutToViceProxy(dep.ExternalID, logoutToken); err != nil {
				log.Errorf("failed to forward logout to %s: %v", dep.ExternalID, err)
			} else {
				successCount.Add(1)
			}
		}()
	}
	wg.Wait()

	log.Infof("forwarded logout to %d/%d VICE sessions for user %s",
		successCount.Load(), len(deployments), userID)

	return c.NoContent(http.StatusOK)
}

func (h *HTTPHandlers) validateLogoutToken(ctx context.Context, tokenString string) (string, error) {
	// Parse without validation first to get the kid
	token, _, err := jwt.NewParser().ParseUnverified(tokenString, jwt.MapClaims{})
	if err != nil {
		return "", fmt.Errorf("failed to parse token: %w", err)
	}

	kid, ok := token.Header["kid"].(string)
	if !ok {
		return "", fmt.Errorf("missing kid in token header")
	}

	// Get the public key from cache
	key, err := h.jwksCache.GetKey(ctx, kid)
	if err != nil {
		return "", fmt.Errorf("failed to get signing key: %w", err)
	}

	// Parse and validate the token
	token, err = jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return key, nil
	})
	if err != nil {
		return "", fmt.Errorf("token validation failed: %w", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok || !token.Valid {
		return "", fmt.Errorf("invalid token claims")
	}

	// Extract user ID from sub claim
	sub, ok := claims["sub"].(string)
	if !ok || sub == "" {
		return "", fmt.Errorf("missing sub claim")
	}

	return sub, nil
}

// forwardLogoutToViceProxy sends the logout token to a single vice-proxy
// sidecar and returns an error if the request fails or returns non-200.
func (h *HTTPHandlers) forwardLogoutToViceProxy(externalID, logoutToken string) error {
	viceProxyURL := fmt.Sprintf("http://vice-%s.%s:%d/backchannel-logout",
		externalID,
		h.incluster.ViceNamespace,
		constants.VICEProxyServicePort)

	resp, err := logoutHTTPClient.PostForm(viceProxyURL, url.Values{
		"logout_token": {logoutToken},
	})
	if err != nil {
		return fmt.Errorf("POST to %s: %w", externalID, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("vice-proxy %s returned status %d", externalID, resp.StatusCode)
	}

	log.Infof("successfully forwarded logout to %s", externalID)
	return nil
}
