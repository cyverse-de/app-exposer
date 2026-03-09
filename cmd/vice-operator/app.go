package main

import (
	"net/http"

	"github.com/cyverse-de/app-exposer/operator"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// App wraps the Echo router and registers operator routes.
type App struct {
	router *echo.Echo
}

// NewApp creates a new App with all operator routes registered.
func NewApp(op *operator.Operator) *App {
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, "Hello from vice-operator.")
	})

	e.GET("/capacity", op.HandleCapacity)
	e.POST("/analyses", op.HandleLaunch)
	e.GET("/listing", op.HandleListing)

	analyses := e.Group("/analyses/:analysis-id")
	analyses.DELETE("", op.HandleExit)
	analyses.POST("/save-and-exit", op.HandleSaveAndExit)
	analyses.GET("/status", op.HandleStatus)
	analyses.GET("/url-ready", op.HandleURLReady)
	analyses.POST("/download-input-files", op.HandleDownloadInputFiles)
	analyses.POST("/save-output-files", op.HandleSaveOutputFiles)
	analyses.GET("/pods", op.HandlePods)
	analyses.GET("/logs", op.HandleLogs)

	return &App{router: e}
}

// Start begins listening on the given address.
func (a *App) Start(addr string) error {
	return a.router.Start(addr)
}
