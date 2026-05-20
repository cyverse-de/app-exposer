package main

import (
	"net/http"
	"slices"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/cyverse-de/app-exposer/operator"
	_ "github.com/cyverse-de/app-exposer/operatordocs"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	echoSwagger "github.com/swaggo/echo-swagger"
)

// App wraps the Echo router and registers operator routes.
type App struct {
	router *echo.Echo
}

//	@title			vice-operator
//	@version		1.0
//	@description	The vice-operator API for managing VICE analyses on remote clusters.
//	@BasePath		/

// AppConfig holds the dependencies for NewApp, grouped into a struct rather
// than a long positional parameter list.
type AppConfig struct {
	Operator          *operator.Operator
	Verifier          *oidc.IDTokenVerifier
	ExpectedClientID  string
	SwaggerCfg        *SwaggerAuthConfig
	AdminRole         string
	AdminEntitlements []string
	ViceUsersCfg      *ViceUsersAuthConfig
}

// NewApp creates a new App with all operator routes registered.
// When cfg.Verifier is non-nil, all API routes require a valid Keycloak JWT
// Bearer token (or a valid session cookie set by the Swagger login flow). The
// token must additionally carry cfg.AdminRole in realm_access.roles or at least
// one value in cfg.AdminEntitlements in its entitlement claim — the API is
// admin-only. cfg.SwaggerCfg controls the Swagger UI login gate; when disabled,
// docs are served without authentication. When cfg.ViceUsersCfg is non-nil, the
// unauthenticated OAuth callback relay is registered.
func NewApp(cfg AppConfig) *App {
	op := cfg.Operator
	verifier := cfg.Verifier
	expectedClientID := cfg.ExpectedClientID
	swaggerCfg := cfg.SwaggerCfg
	adminRole := cfg.AdminRole
	adminEntitlements := cfg.AdminEntitlements
	viceUsersCfg := cfg.ViceUsersCfg

	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	// Landing page; unauthenticated so it also serves as the health check. Its
	// API-docs link goes through the existing Swagger auth gate.
	e.GET("/", op.HandleLandingPage)

	// Swagger UI docs — gated by a session-based login flow when configured,
	// otherwise served openly. These routes are outside the Bearer auth group
	// so the login page and OAuth callback are always reachable.
	if verifier != nil && swaggerCfg.Enabled() {
		e.GET("/docs/login", handleLogin(swaggerCfg))
		e.GET("/docs/callback", handleCallback(swaggerCfg, verifier))
		e.GET("/docs/logout", handleLogout())

		docs := e.Group("/docs")
		docs.Use(swaggerSessionMiddleware(verifier, swaggerCfg))
		// InstanceName must match the --instanceName passed to swag init for
		// operatordocs (see Justfile); a mismatch causes echo-swagger to 500
		// on doc.json without logging.
		docs.GET("/*", echoSwagger.EchoWrapHandler(echoSwagger.InstanceName("operator")))
	} else {
		e.GET("/docs/*", echoSwagger.EchoWrapHandler(echoSwagger.InstanceName("operator")))
	}

	// vice-users OAuth callback relay — unauthenticated and outside both the
	// Bearer auth group and the /docs login gate, since Keycloak redirects the
	// end user's browser here directly. Registered only when a state HMAC
	// secret is configured.
	if viceUsersCfg != nil {
		e.GET(viceUsersCallbackPath, handleViceUsersCallback(viceUsersCfg))
	}

	// All API routes go through an optional auth group.
	api := e.Group("")
	if verifier != nil {
		api.Use(bearerAuthMiddleware(verifier, expectedClientID, swaggerCfg, adminRole, adminEntitlements))
	}
	api.GET("/capacity", op.HandleCapacity)
	api.POST("/analyses", op.HandleLaunch)
	api.GET("/analyses", op.HandleListing)

	analyses := api.Group("/analyses/:analysis-id")
	analyses.DELETE("", op.HandleExit)
	analyses.POST("/save-and-exit", op.HandleSaveAndExit)
	analyses.GET("/status", op.HandleStatus)
	analyses.GET("/url-ready", op.HandleURLReady)
	analyses.POST("/download-input-files", op.HandleDownloadInputFiles)
	analyses.POST("/save-output-files", op.HandleSaveOutputFiles)
	analyses.GET("/pods", op.HandlePods)
	analyses.GET("/logs", op.HandleLogs)
	analyses.POST("/swap-route", op.HandleSwapRoute)
	analyses.GET("/permissions", op.HandleGetPermissions)
	analyses.PUT("/permissions", op.HandleUpdatePermissions)
	analyses.GET("/active-sessions", op.HandleGetActiveSessions)
	analyses.POST("/logout-user", op.HandleLogoutUser)

	// Admin operations.
	api.POST("/regenerate-network-policies", op.HandleRegenerateNetworkPolicies)

	// Image cache routes.
	api.PUT("/image-cache", op.HandleCacheImages)
	api.POST("/image-cache/refresh", op.HandleRefreshCachedImages)
	api.DELETE("/image-cache", op.HandleRemoveCachedImages)
	api.GET("/image-cache", op.HandleListCachedImages)
	api.GET("/image-cache/:id", op.HandleGetCachedImage)
	api.DELETE("/image-cache/:id", op.HandleDeleteCachedImage)

	return &App{router: e}
}

// Start begins listening on the given address.
func (a *App) Start(addr string) error {
	return a.router.Start(addr)
}

// LoadingApp wraps the Echo router for the loading page server.
type LoadingApp struct {
	router *echo.Echo
}

// NewLoadingApp creates a new loading page server with routes for serving
// loading pages on analysis subdomains.
func NewLoadingApp(op *operator.Operator) *LoadingApp {
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	e.GET("/", op.HandleLoadingPage)
	e.GET("/loading/status", op.HandleLoadingStatus)

	return &LoadingApp{router: e}
}

// Start begins listening on the given address.
func (a *LoadingApp) Start(addr string) error {
	return a.router.Start(addr)
}

// stripBearer extracts the token from an `Authorization: Bearer <token>`
// header value. RFC 7235 specifies that auth schemes are case-insensitive,
// so "Bearer", "bearer", "BEARER" all match. Returns ("", false) when the
// header is empty or doesn't start with the Bearer scheme.
func stripBearer(auth string) (string, bool) {
	const prefix = "Bearer "
	if len(auth) < len(prefix) {
		return "", false
	}
	if !strings.EqualFold(auth[:len(prefix)], prefix) {
		return "", false
	}
	return auth[len(prefix):], true
}

// bearerAuthMiddleware returns Echo middleware that validates JWT Bearer tokens
// from Keycloak. It accepts tokens from two sources:
//  1. Authorization: Bearer <token> header (machine-to-machine).
//  2. Session cookie set by the Swagger UI login flow.
//
// Keycloak client credentials tokens use the "azp" (authorized party) claim for
// the client ID rather than "aud", so the standard audience check is skipped and
// azp is verified manually against the expected API client ID and (optionally)
// the Swagger UI client ID.
//
// After azp validation, the token must additionally carry adminRole in
// realm_access.roles[] or at least one value in adminEntitlements in its
// entitlement claim. This restricts the API to service accounts that hold the
// role and to admin users whose group memberships surface as entitlements.
func bearerAuthMiddleware(verifier *oidc.IDTokenVerifier, expectedClientID string, swaggerCfg *SwaggerAuthConfig, adminRole string, adminEntitlements []string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			var rawToken string

			// Prefer the Authorization header; fall back to the session cookie.
			if token, ok := stripBearer(c.Request().Header.Get("Authorization")); ok {
				rawToken = token
			} else if swaggerCfg.Enabled() {
				extracted, err := extractTokenFromCookie(c, swaggerCfg.CookieSecret)
				if err != nil {
					return echo.NewHTTPError(http.StatusUnauthorized, "missing or malformed Authorization header")
				}
				rawToken = extracted
			} else {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing or malformed Authorization header")
			}

			token, err := verifier.Verify(c.Request().Context(), rawToken)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid token: "+err.Error())
			}

			// Pull every claim we need in a single Claims() call. Re-parsing
			// the token a second time for the role/entitlement check would
			// double the JSON-decode cost on every request.
			var claims struct {
				AZP         string `json:"azp"`
				RealmAccess struct {
					Roles []string `json:"roles"`
				} `json:"realm_access"`
				Entitlement []string `json:"entitlement"`
			}
			if err := token.Claims(&claims); err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "failed to parse token claims: "+err.Error())
			}

			// Accept the API client ID or the Swagger UI client ID.
			if claims.AZP != expectedClientID && (!swaggerCfg.Enabled() || claims.AZP != swaggerCfg.ClientID) {
				return echo.NewHTTPError(http.StatusForbidden, "unauthorized client")
			}

			// Admin gate: realm role OR entitlement intersection. The role
			// path is how service accounts get in; the entitlement path is
			// how human admins get in via group membership.
			if !allowedByAdminGate(claims.RealmAccess.Roles, claims.Entitlement, adminRole, adminEntitlements) {
				return echo.NewHTTPError(http.StatusForbidden, "insufficient privileges")
			}

			return next(c)
		}
	}
}

// allowedByAdminGate reports whether a token with the given realm roles and
// entitlement values should be admitted by the API's admin gate. A token
// passes if it carries adminRole in roles, or if any of its entitlements
// appears in adminEntitlements.
func allowedByAdminGate(roles, entitlements []string, adminRole string, adminEntitlements []string) bool {
	return slices.Contains(roles, adminRole) || anyMatch(entitlements, adminEntitlements)
}

// anyMatch reports whether any element of a is also present in b. Used for
// the entitlement-intersection check; both inputs are typically very small
// (≤10 entries) so a nested loop beats building a set.
func anyMatch(a, b []string) bool {
	for _, v := range a {
		if slices.Contains(b, v) {
			return true
		}
	}
	return false
}
