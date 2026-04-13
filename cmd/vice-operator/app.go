package main

import (
	"net/http"
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

//	@title						vice-operator
//	@version					1.0
//	@description				The vice-operator API for managing VICE analyses on remote clusters.
//	@BasePath					/
//	@securityDefinitions.apikey	BearerAuth
//	@in							header
//	@name						Authorization

// NewApp creates a new App with all operator routes registered.
// When verifier is non-nil, all routes except the health check require a
// valid Keycloak JWT Bearer token with an azp claim matching expectedClientID.
func NewApp(op *operator.Operator, verifier *oidc.IDTokenVerifier, expectedClientID string) *App {
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(middleware.CORS())

	// Health check is always unauthenticated.
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "Hello from vice-operator.")
	})

	// All other routes go through an optional auth group.
	api := e.Group("")
	if verifier != nil {
		api.Use(bearerAuthMiddleware(verifier, expectedClientID))
	}

	// InstanceName must match the --instanceName used in swag init.
	api.GET("/docs/*", echoSwagger.EchoWrapHandler(echoSwagger.InstanceName("operator")))
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

// bearerAuthMiddleware returns Echo middleware that validates JWT Bearer tokens
// from Keycloak. Keycloak client credentials tokens use the "azp" (authorized
// party) claim for the client ID rather than "aud", so the standard audience
// check is skipped and azp is verified manually.
func bearerAuthMiddleware(verifier *oidc.IDTokenVerifier, expectedClientID string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			auth := c.Request().Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				return echo.NewHTTPError(http.StatusUnauthorized, "missing or malformed Authorization header")
			}
			rawToken := strings.TrimPrefix(auth, "Bearer ")

			token, err := verifier.Verify(c.Request().Context(), rawToken)
			if err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "invalid token: "+err.Error())
			}

			// Extract azp (authorized party) from the token claims.
			var claims struct {
				AZP string `json:"azp"`
			}
			if err := token.Claims(&claims); err != nil {
				return echo.NewHTTPError(http.StatusUnauthorized, "failed to parse token claims: "+err.Error())
			}
			if claims.AZP != expectedClientID {
				return echo.NewHTTPError(http.StatusForbidden, "unauthorized client")
			}

			return next(c)
		}
	}
}
