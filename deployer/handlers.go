package deployer

import (
	"net/http"

	"github.com/cyverse-de/app-exposer/vicetypes"
	"github.com/labstack/echo/v4"
)

// Handlers provides HTTP handlers for the deployer API.
type Handlers struct {
	deployer  *Deployer
	namespace string
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(deployer *Deployer, namespace string) *Handlers {
	return &Handlers{
		deployer:  deployer,
		namespace: namespace,
	}
}

// RegisterRoutes registers the deployer API routes with the Echo router.
func (h *Handlers) RegisterRoutes(e *echo.Echo) {
	api := e.Group("/api/v1")

	// Deployment operations
	api.POST("/deployments", h.CreateDeployment)
	api.DELETE("/deployments/:external_id", h.DeleteDeployment)
	api.GET("/deployments/:external_id/status", h.GetStatus)
	api.GET("/deployments/:external_id/url-ready", h.CheckURLReady)
	api.GET("/deployments/:external_id/logs", h.GetLogs)

	// Health check
	api.GET("/health", h.Health)
	e.GET("/health", h.Health) // Also at root for k8s probes
}

// CreateDeployment handles POST /api/v1/deployments
//
//	@Summary		Create a new VICE deployment
//	@Description	Creates all Kubernetes resources for a VICE deployment from the provided spec
//	@Tags			deployments
//	@Accept			json
//	@Produce		json
//	@Param			spec	body		vicetypes.VICEDeploymentSpec	true	"Deployment specification"
//	@Success		201		{object}	vicetypes.DeploymentResponse
//	@Failure		400		{object}	vicetypes.DeploymentResponse
//	@Failure		500		{object}	vicetypes.DeploymentResponse
//	@Router			/api/v1/deployments [post]
func (h *Handlers) CreateDeployment(c echo.Context) error {
	ctx := c.Request().Context()

	var spec vicetypes.VICEDeploymentSpec
	if err := c.Bind(&spec); err != nil {
		return c.JSON(http.StatusBadRequest, vicetypes.DeploymentResponse{
			Status: "error",
			Error:  "invalid request body: " + err.Error(),
		})
	}

	if spec.Metadata.ExternalID == "" {
		return c.JSON(http.StatusBadRequest, vicetypes.DeploymentResponse{
			Status: "error",
			Error:  "external_id is required in metadata",
		})
	}

	resp, err := h.deployer.CreateDeployment(ctx, &spec)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, resp)
	}

	return c.JSON(http.StatusCreated, resp)
}

// DeleteDeployment handles DELETE /api/v1/deployments/:external_id
//
//	@Summary		Delete a VICE deployment
//	@Description	Deletes all Kubernetes resources associated with the deployment
//	@Tags			deployments
//	@Produce		json
//	@Param			external_id	path		string	true	"External ID of the deployment"
//	@Param			namespace	query		string	false	"Kubernetes namespace (defaults to configured namespace)"
//	@Success		200			{object}	vicetypes.DeploymentResponse
//	@Failure		500			{object}	vicetypes.DeploymentResponse
//	@Router			/api/v1/deployments/{external_id} [delete]
func (h *Handlers) DeleteDeployment(c echo.Context) error {
	ctx := c.Request().Context()
	externalID := c.Param("external_id")
	namespace := c.QueryParam("namespace")
	if namespace == "" {
		namespace = h.namespace
	}

	resp, err := h.deployer.DeleteDeployment(ctx, externalID, namespace)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, resp)
	}

	return c.JSON(http.StatusOK, resp)
}

// GetStatus handles GET /api/v1/deployments/:external_id/status
//
//	@Summary		Get deployment status
//	@Description	Returns the current status of all resources in the deployment
//	@Tags			deployments
//	@Produce		json
//	@Param			external_id	path		string	true	"External ID of the deployment"
//	@Param			namespace	query		string	false	"Kubernetes namespace (defaults to configured namespace)"
//	@Success		200			{object}	vicetypes.DeploymentStatus
//	@Failure		500			{object}	map[string]string
//	@Router			/api/v1/deployments/{external_id}/status [get]
func (h *Handlers) GetStatus(c echo.Context) error {
	ctx := c.Request().Context()
	externalID := c.Param("external_id")
	namespace := c.QueryParam("namespace")
	if namespace == "" {
		namespace = h.namespace
	}

	status, err := h.deployer.GetStatus(ctx, externalID, namespace)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, status)
}

// CheckURLReady handles GET /api/v1/deployments/:external_id/url-ready
//
//	@Summary		Check if deployment is ready to serve traffic
//	@Description	Returns whether the deployment's ingress, service, and pods are ready
//	@Tags			deployments
//	@Produce		json
//	@Param			external_id	path		string	true	"External ID of the deployment"
//	@Param			namespace	query		string	false	"Kubernetes namespace (defaults to configured namespace)"
//	@Success		200			{object}	vicetypes.URLReadyResponse
//	@Failure		500			{object}	map[string]string
//	@Router			/api/v1/deployments/{external_id}/url-ready [get]
func (h *Handlers) CheckURLReady(c echo.Context) error {
	ctx := c.Request().Context()
	externalID := c.Param("external_id")
	namespace := c.QueryParam("namespace")
	if namespace == "" {
		namespace = h.namespace
	}

	ready, err := h.deployer.CheckURLReady(ctx, externalID, namespace)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}

	return c.JSON(http.StatusOK, ready)
}

// GetLogs handles GET /api/v1/deployments/:external_id/logs
//
//	@Summary		Get deployment logs
//	@Description	Returns logs from the deployment's pods
//	@Tags			deployments
//	@Produce		json
//	@Param			external_id	path		string	true	"External ID of the deployment"
//	@Param			namespace	query		string	false	"Kubernetes namespace (defaults to configured namespace)"
//	@Param			container	query		string	false	"Container name"
//	@Param			since		query		integer	false	"Return logs from the last N seconds"
//	@Param			tail		query		integer	false	"Return the last N lines"
//	@Param			previous	query		boolean	false	"Return logs from previous container instance"
//	@Success		200			{object}	vicetypes.LogsResponse
//	@Failure		500			{object}	vicetypes.LogsResponse
//	@Router			/api/v1/deployments/{external_id}/logs [get]
func (h *Handlers) GetLogs(c echo.Context) error {
	ctx := c.Request().Context()
	externalID := c.Param("external_id")
	namespace := c.QueryParam("namespace")
	if namespace == "" {
		namespace = h.namespace
	}

	req := &vicetypes.LogsRequest{
		Container: c.QueryParam("container"),
		Previous:  c.QueryParam("previous") == "true",
	}

	// Parse optional integer parameters
	if since := c.QueryParam("since"); since != "" {
		var sinceSeconds int64
		if err := echo.QueryParamsBinder(c).Int64("since", &sinceSeconds).BindError(); err == nil {
			req.SinceSeconds = &sinceSeconds
		}
	}
	if tail := c.QueryParam("tail"); tail != "" {
		var tailLines int64
		if err := echo.QueryParamsBinder(c).Int64("tail", &tailLines).BindError(); err == nil {
			req.TailLines = &tailLines
		}
	}

	logs, err := h.deployer.GetLogs(ctx, externalID, namespace, req)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, vicetypes.LogsResponse{
			Error: err.Error(),
		})
	}

	return c.JSON(http.StatusOK, logs)
}

// Health handles GET /api/v1/health and GET /health
//
//	@Summary		Health check
//	@Description	Returns the health status of the deployer service
//	@Tags			health
//	@Produce		json
//	@Success		200	{object}	vicetypes.HealthResponse
//	@Failure		503	{object}	vicetypes.HealthResponse
//	@Router			/api/v1/health [get]
func (h *Handlers) Health(c echo.Context) error {
	ctx := c.Request().Context()
	health := h.deployer.Health(ctx)

	statusCode := http.StatusOK
	if health.Status != "healthy" {
		statusCode = http.StatusServiceUnavailable
	}

	return c.JSON(statusCode, health)
}
