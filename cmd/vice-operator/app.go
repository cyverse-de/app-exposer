package main

import (
	"crypto/subtle"
	"net/http"

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
//	@host						localhost:60001
//	@BasePath					/
//	@securityDefinitions.basic	BasicAuth

// NewApp creates a new App with all operator routes registered.
// When basicAuth is true, all routes except the health check require
// basic auth with the given username and password.
func NewApp(op *operator.Operator, basicAuth bool, username, password string) *App {
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
	if basicAuth {
		api.Use(middleware.BasicAuth(func(u, p string, c echo.Context) (bool, error) {
			// Constant-time comparison to prevent timing attacks.
			uMatch := subtle.ConstantTimeCompare([]byte(u), []byte(username)) == 1
			pMatch := subtle.ConstantTimeCompare([]byte(p), []byte(password)) == 1
			return uMatch && pMatch, nil
		}))
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

	// Image cache routes.
	api.PUT("/image-cache", op.HandleCacheImages)
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
