package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/cyverse-de/go-mod/viceauth"
	"github.com/labstack/echo/v4"
)

// viceUsersCallbackPath is the fixed path of the OAuth callback relay. It is
// combined with --public-url to form OPERATOR_CALLBACK_URL, the single static
// redirect_uri registered in Keycloak for the vice-users client.
const viceUsersCallbackPath = "/vice-users/callback"

// ViceUsersAuthConfig configures the vice-users OAuth callback relay.
type ViceUsersAuthConfig struct {
	// StateCodec verifies the HMAC-signed OAuth state produced by vice-proxy.
	StateCodec *viceauth.Codec

	// BaseDomain is the VICE base domain (e.g. "cyverse.run"). The relay only
	// redirects to single-label subdomains of this domain, so a forged or
	// tampered state cannot turn the callback into an open redirect.
	BaseDomain string
}

// isAllowedHost reports whether host is a single-label subdomain of the VICE
// base domain. host is expected to already have any port stripped (as
// url.URL.Hostname does). This is the open-redirect guard for the relay.
func (cfg *ViceUsersAuthConfig) isAllowedHost(host string) bool {
	suffix := "." + cfg.BaseDomain
	label, ok := strings.CutSuffix(host, suffix)
	if !ok {
		return false
	}
	return label != "" && !strings.Contains(label, ".")
}

// handleViceUsersCallback returns the handler for GET /vice-users/callback.
//
// vice-proxy registers this operator's URL as the single static redirect_uri
// for the Keycloak "vice-users" client, because Keycloak cannot wildcard-match
// per-app VICE subdomains. Keycloak delivers the authorization code here; the
// handler recovers the original app URL from the signed state and relays the
// browser back to it with the code intact. It is intentionally stateless and
// does no token exchange — vice-proxy holds the client secret and redeems the
// code itself.
func handleViceUsersCallback(cfg *ViceUsersAuthConfig) echo.HandlerFunc {
	return func(c echo.Context) error {
		q := c.Request().URL.Query()

		// Surface Keycloak-side errors instead of relaying a useless redirect.
		if errParam := q.Get("error"); errParam != "" {
			return echo.NewHTTPError(http.StatusUnauthorized,
				fmt.Sprintf("Keycloak error: %s — %s", errParam, q.Get("error_description")))
		}

		code := q.Get("code")
		state := q.Get("state")
		if code == "" || state == "" {
			return echo.NewHTTPError(http.StatusBadRequest, "missing code or state")
		}

		// A decode error means a forged or corrupted state — refuse to relay.
		claims, err := cfg.StateCodec.Decode(state)
		if err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid state")
		}

		origin, err := url.Parse(claims.Origin)
		if err != nil || origin.Scheme != "https" || !cfg.isAllowedHost(origin.Hostname()) {
			return echo.NewHTTPError(http.StatusBadRequest, "invalid redirect target")
		}

		// Re-attach code + state so vice-proxy can validate state against its
		// cookie and exchange the code. Preserve any path/query already present.
		oq := origin.Query()
		oq.Set("code", code)
		oq.Set("state", state)
		origin.RawQuery = oq.Encode()

		return c.Redirect(http.StatusTemporaryRedirect, origin.String())
	}
}
