package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/labstack/echo/v4"
)

const (
	sessionCookieName = "vice-operator-session"
	stateCookieName   = "vice-operator-oauth-state"
	stateMaxAge       = 300 // 5 minutes for the CSRF state cookie.
)

// swaggerAuthHTTPClient is used for OAuth token-endpoint calls. The default
// http.Client has no timeout, which allows an unresponsive identity provider
// to hang the callback goroutine indefinitely.
var swaggerAuthHTTPClient = &http.Client{Timeout: 10 * time.Second}

// SwaggerAuthConfig holds the OAuth2/OIDC settings for the Swagger UI login flow.
type SwaggerAuthConfig struct {
	IssuerURL    string // OIDC issuer (e.g. https://keycloak.example.com/realms/cyverse).
	ClientID     string // OAuth2 client ID (authorization code flow).
	ClientSecret string // OAuth2 client secret.
	CookieSecret []byte // HMAC key for signing session cookies.
}

// Enabled returns true when enough configuration is present for the login flow.
func (c *SwaggerAuthConfig) Enabled() bool {
	return c.ClientID != "" && c.IssuerURL != ""
}

// authURL returns the Keycloak authorization endpoint.
func (c *SwaggerAuthConfig) authURL() string {
	u, err := url.JoinPath(c.IssuerURL, "protocol", "openid-connect", "auth")
	if err != nil {
		log.Errorf("swaggerauth: joiningauth URL from %q: %v", c.IssuerURL, err)
	}
	return u
}

// tokenURL returns the Keycloak token endpoint. Same caveat as authURL.
func (c *SwaggerAuthConfig) tokenURL() string {
	u, err := url.JoinPath(c.IssuerURL, "protocol", "openid-connect", "token")
	if err != nil {
		log.Errorf("swaggerauth: joiningtoken URL from %q: %v", c.IssuerURL, err)
	}
	return u
}

// GenerateCookieSecret creates a random 32-byte key for HMAC signing.
func GenerateCookieSecret() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating cookie secret: %w", err)
	}
	return key, nil
}

// signToken produces a signed cookie value: base64url(token).base64url(hmac).
func signToken(token string, key []byte) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(token))
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + sig
}

// verifyAndExtractToken validates the HMAC signature and returns the raw token.
func verifyAndExtractToken(cookie string, key []byte) (string, error) {
	parts := strings.SplitN(cookie, ".", 2)
	if len(parts) != 2 {
		return "", errors.New("malformed session cookie")
	}
	payload, sig := parts[0], parts[1]

	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(payload))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(expected)) {
		return "", errors.New("invalid session cookie signature")
	}

	token, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return "", fmt.Errorf("decoding session cookie payload: %w", err)
	}
	return string(token), nil
}

// swaggerSessionMiddleware redirects unauthenticated users to the login page.
// Requests to /docs/login, /docs/callback, and /docs/logout bypass this check.
func swaggerSessionMiddleware(verifier *oidc.IDTokenVerifier, cfg *SwaggerAuthConfig) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			path := c.Request().URL.Path
			if path == "/docs/login" || path == "/docs/callback" || path == "/docs/logout" {
				return next(c)
			}

			cookie, err := c.Cookie(sessionCookieName)
			if err != nil || cookie.Value == "" {
				return c.Redirect(http.StatusFound, "/docs/login")
			}

			rawToken, err := verifyAndExtractToken(cookie.Value, cfg.CookieSecret)
			if err != nil {
				log.Debugf("invalid session cookie: %v", err)
				return c.Redirect(http.StatusFound, "/docs/login")
			}

			// Verify the token is still valid (signature + expiry).
			if _, err := verifier.Verify(c.Request().Context(), rawToken); err != nil {
				log.Debugf("session token verification failed: %v", err)
				clearSessionCookie(c)
				return c.Redirect(http.StatusFound, "/docs/login")
			}

			return next(c)
		}
	}
}

// handleLogin serves a simple HTML page with a "Login with Keycloak" button.
func handleLogin(cfg *SwaggerAuthConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Generate a random state parameter for CSRF protection.
		stateBytes := make([]byte, 16)
		if _, err := rand.Read(stateBytes); err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, "failed to generate state")
		}
		state := base64.RawURLEncoding.EncodeToString(stateBytes)

		// Store state in a short-lived cookie.
		c.SetCookie(&http.Cookie{
			Name:     stateCookieName,
			Value:    state,
			Path:     "/docs",
			MaxAge:   stateMaxAge,
			HttpOnly: true,
			Secure:   c.Scheme() == "https",
			SameSite: http.SameSiteLaxMode,
		})

		// Build the Keycloak authorization URL.
		redirectURI := buildRedirectURI(c)
		authParams := url.Values{
			"response_type": {"code"},
			"client_id":     {cfg.ClientID},
			"redirect_uri":  {redirectURI},
			"scope":         {"openid"},
			"state":         {state},
		}
		authLink := cfg.authURL() + "?" + authParams.Encode()

		return c.HTML(http.StatusOK, loginPage(authLink))
	}
}

// handleCallback exchanges the authorization code for tokens and sets a session cookie.
func handleCallback(cfg *SwaggerAuthConfig, verifier *oidc.IDTokenVerifier) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Validate CSRF state.
		stateCookie, err := c.Cookie(stateCookieName)
		if err != nil || stateCookie.Value == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "missing OAuth state cookie")
		}
		if c.QueryParam("state") != stateCookie.Value {
			return echo.NewHTTPError(http.StatusBadRequest, "OAuth state mismatch")
		}
		// Clear the state cookie.
		c.SetCookie(&http.Cookie{
			Name:     stateCookieName,
			Value:    "",
			Path:     "/docs",
			MaxAge:   -1,
			HttpOnly: true,
		})

		// Check for OAuth error response.
		if errParam := c.QueryParam("error"); errParam != "" {
			desc := c.QueryParam("error_description")
			return echo.NewHTTPError(http.StatusUnauthorized, fmt.Sprintf("OAuth error: %s — %s", errParam, desc))
		}

		code := c.QueryParam("code")
		if code == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "missing authorization code")
		}

		// Exchange the code for tokens.
		redirectURI := buildRedirectURI(c)
		tokenResp, err := exchangeCode(c.Request().Context(), cfg, code, redirectURI)
		if err != nil {
			log.Errorf("token exchange failed: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "token exchange failed")
		}

		// Verify the access token so we know it's valid before storing.
		idToken, err := verifier.Verify(c.Request().Context(), tokenResp.AccessToken)
		if err != nil {
			log.Errorf("token verification failed after exchange: %v", err)
			return echo.NewHTTPError(http.StatusInternalServerError, "received invalid token from identity provider")
		}

		// Compute cookie expiry from the token's exp claim.
		maxAge := max(int(time.Until(idToken.Expiry).Seconds()), 0)

		// Set the signed session cookie.
		signed := signToken(tokenResp.AccessToken, cfg.CookieSecret)
		c.SetCookie(&http.Cookie{
			Name:     sessionCookieName,
			Value:    signed,
			Path:     "/",
			MaxAge:   maxAge,
			HttpOnly: true,
			Secure:   c.Scheme() == "https",
			SameSite: http.SameSiteLaxMode,
		})

		return c.Redirect(http.StatusFound, "/docs/index.html")
	}
}

// handleLogout clears the session cookie and redirects to the login page.
func handleLogout() echo.HandlerFunc {
	return func(c echo.Context) error {
		clearSessionCookie(c)
		return c.Redirect(http.StatusFound, "/docs/login")
	}
}

// clearSessionCookie removes the session cookie.
func clearSessionCookie(c echo.Context) {
	c.SetCookie(&http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
	})
}

// buildRedirectURI constructs the OAuth callback URL from the current request.
func buildRedirectURI(c echo.Context) string {
	scheme := c.Scheme()
	return fmt.Sprintf("%s://%s/docs/callback", scheme, c.Request().Host)
}

// tokenResponse holds the fields we need from the Keycloak token endpoint.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
}

// exchangeCode performs the OAuth2 authorization code exchange. The caller
// provides the request context so cancellation composes with the package-level
// client timeout on swaggerAuthHTTPClient.
func exchangeCode(ctx context.Context, cfg *SwaggerAuthConfig, code, redirectURI string) (*tokenResponse, error) {
	data := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
		"client_id":    {cfg.ClientID},
	}
	if cfg.ClientSecret != "" {
		data.Set("client_secret", cfg.ClientSecret)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.tokenURL(), strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("building token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := swaggerAuthHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST to token endpoint: %w", err)
	}
	defer common.CloseBody(resp)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading token response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp tokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("parsing token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return nil, errors.New("token response missing access_token")
	}
	return &tokenResp, nil
}

// extractTokenFromCookie reads and verifies the session cookie, returning the
// raw access token. Used by the bearer auth middleware to accept cookie-based auth.
func extractTokenFromCookie(c echo.Context, key []byte) (string, error) {
	cookie, err := c.Cookie(sessionCookieName)
	if err != nil {
		return "", fmt.Errorf("no session cookie: %w", err)
	}
	return verifyAndExtractToken(cookie.Value, key)
}

// loginPage returns the HTML for the login page.
func loginPage(authLink string) string {
	return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>vice-operator — Login</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
      background: #fafafa;
      display: flex;
      align-items: center;
      justify-content: center;
      min-height: 100vh;
    }
    .card {
      background: #fff;
      border-radius: 8px;
      box-shadow: 0 2px 12px rgba(0,0,0,0.1);
      padding: 3rem;
      text-align: center;
      max-width: 400px;
      width: 90%;
    }
    h1 { font-size: 1.5rem; color: #333; margin-bottom: 0.5rem; }
    p { color: #666; margin-bottom: 2rem; font-size: 0.95rem; }
    .btn {
      display: inline-block;
      padding: 0.75rem 2rem;
      background: #4a90d9;
      color: #fff;
      text-decoration: none;
      border-radius: 4px;
      font-size: 1rem;
      font-weight: 500;
      transition: background 0.2s;
    }
    .btn:hover { background: #357abd; }
  </style>
</head>
<body>
  <div class="card">
    <h1>vice-operator API</h1>
    <p>Log in to access the Swagger documentation.</p>
    <a class="btn" href="` + authLink + `">Login with Keycloak</a>
  </div>
</body>
</html>`
}
