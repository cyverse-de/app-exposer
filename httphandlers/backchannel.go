package httphandlers

import (
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/golang-jwt/jwt/v5"
	"github.com/labstack/echo/v4"
)

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

// GetKey retrieves a public key by kid, fetching from Keycloak if needed
func (c *JWKSCache) GetKey(kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	if time.Since(c.lastFetched) < c.cacheTTL {
		if key, ok := c.keys[kid]; ok {
			c.mu.RUnlock()
			return key, nil
		}
	}
	c.mu.RUnlock()

	// Fetch fresh keys
	if err := c.refresh(); err != nil {
		return nil, err
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	if key, ok := c.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("key %s not found in JWKS", kid)
}

func (c *JWKSCache) refresh() error {
	resp, err := http.Get(c.jwksURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, key := range jwks.Keys {
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

		c.keys[key.Kid] = &rsa.PublicKey{N: n, E: e}
	}
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
	userID, err := h.validateLogoutToken(logoutToken)
	if err != nil {
		log.Errorf("invalid logout token: %v", err)
		return echo.NewHTTPError(http.StatusBadRequest, "invalid logout token")
	}

	// Find all VICE deployments for this user
	deployments, err := h.incluster.GetDeploymentsByUserID(ctx, userID)
	if err != nil {
		log.Errorf("error finding deployments for user %s: %v", userID, err)
		// Per OIDC spec, return 200 even on errors
		return c.NoContent(http.StatusOK)
	}

	log.Infof("forwarding logout to %d deployments for user %s", len(deployments), userID)

	// Forward logout to each vice-proxy (best effort, fire and forget)
	for _, dep := range deployments {
		go h.forwardLogoutToViceProxy(dep.ExternalID, logoutToken)
	}

	// Per OIDC Back-Channel Logout spec, return 200 OK
	return c.NoContent(http.StatusOK)
}

func (h *HTTPHandlers) validateLogoutToken(tokenString string) (string, error) {
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
	key, err := h.jwksCache.GetKey(kid)
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

func (h *HTTPHandlers) forwardLogoutToViceProxy(externalID, logoutToken string) {
	// Service naming: vice-{external-id} on port 60000
	viceProxyURL := fmt.Sprintf("http://vice-%s.%s:%d/backchannel-logout",
		externalID,
		h.incluster.ViceNamespace,
		constants.VICEProxyServicePort)

	resp, err := http.PostForm(viceProxyURL, url.Values{
		"logout_token": {logoutToken},
	})
	if err != nil {
		log.Warnf("failed to forward logout to %s: %v", externalID, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Warnf("vice-proxy %s returned status %d for logout", externalID, resp.StatusCode)
	} else {
		log.Infof("successfully forwarded logout to %s", externalID)
	}
}
