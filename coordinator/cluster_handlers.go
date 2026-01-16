package coordinator

import (
	"net/http"
	"time"

	"github.com/cyverse-de/app-exposer/vicetypes"
	"github.com/labstack/echo/v4"
)

// ClusterHandlers provides HTTP handlers for cluster management.
type ClusterHandlers struct {
	registry       *ClusterRegistry
	deployerClient *DeployerClient
}

// NewClusterHandlers creates a new ClusterHandlers instance.
func NewClusterHandlers(registry *ClusterRegistry, deployerClient *DeployerClient) *ClusterHandlers {
	return &ClusterHandlers{
		registry:       registry,
		deployerClient: deployerClient,
	}
}

// RegisterRoutes registers the cluster management routes with the Echo router.
func (h *ClusterHandlers) RegisterRoutes(e *echo.Echo) {
	api := e.Group("/api/v1/clusters")

	api.GET("", h.ListClusters)
	api.POST("", h.RegisterCluster)
	api.GET("/:id", h.GetCluster)
	api.PUT("/:id", h.UpdateCluster)
	api.DELETE("/:id", h.DeleteCluster)
	api.POST("/:id/enable", h.EnableCluster)
	api.POST("/:id/disable", h.DisableCluster)
	api.POST("/reload", h.ReloadClusters)
}

// ListClusters handles GET /api/v1/clusters
//
//	@Summary		List all registered clusters
//	@Description	Returns a list of all registered deployer clusters
//	@Tags			clusters
//	@Produce		json
//	@Success		200	{object}	vicetypes.ClusterListResponse
//	@Router			/api/v1/clusters [get]
func (h *ClusterHandlers) ListClusters(c echo.Context) error {
	ctx := c.Request().Context()
	clusters := h.registry.ListClusters()

	responses := make([]vicetypes.ClusterResponse, 0, len(clusters))
	for _, cluster := range clusters {
		resp := h.clusterToResponse(cluster)

		// Check health
		health, err := h.deployerClient.CheckHealth(ctx, cluster.ID)
		if err != nil {
			resp.Status = string(vicetypes.ClusterUnreachable)
		} else if health.Status == "healthy" {
			resp.Status = string(vicetypes.ClusterHealthy)
		} else {
			resp.Status = string(vicetypes.ClusterUnreachable)
		}
		if !cluster.Enabled {
			resp.Status = string(vicetypes.ClusterDisabled)
		}

		now := time.Now()
		resp.LastHealthCheck = &now

		responses = append(responses, resp)
	}

	return c.JSON(http.StatusOK, vicetypes.ClusterListResponse{
		Clusters: responses,
		Total:    len(responses),
	})
}

// RegisterCluster handles POST /api/v1/clusters
//
//	@Summary		Register a new cluster
//	@Description	Registers a new deployer cluster with optional mTLS configuration
//	@Tags			clusters
//	@Accept			json
//	@Produce		json
//	@Param			cluster	body		vicetypes.ClusterRegistrationRequest	true	"Cluster registration"
//	@Success		201		{object}	vicetypes.ClusterResponse
//	@Failure		400		{object}	map[string]string
//	@Failure		500		{object}	map[string]string
//	@Router			/api/v1/clusters [post]
func (h *ClusterHandlers) RegisterCluster(c echo.Context) error {
	ctx := c.Request().Context()

	var req vicetypes.ClusterRegistrationRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid request body: " + err.Error(),
		})
	}

	// Validate required fields
	if req.Name == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "name is required",
		})
	}
	if req.DeployerURL == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "deployer_url is required",
		})
	}

	// Validate mTLS configuration
	if req.MTLSEnabled {
		if req.CACert == "" || req.ClientCert == "" || req.ClientKey == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "ca_cert, client_cert, and client_key are required when mtls_enabled is true",
			})
		}
	}

	cluster, err := h.registry.RegisterCluster(ctx, &req)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}

	resp := h.clusterToResponse(cluster)
	resp.Status = "registered"

	return c.JSON(http.StatusCreated, resp)
}

// GetCluster handles GET /api/v1/clusters/:id
//
//	@Summary		Get cluster details
//	@Description	Returns details for a specific cluster including health status
//	@Tags			clusters
//	@Produce		json
//	@Param			id	path		string	true	"Cluster ID"
//	@Success		200	{object}	vicetypes.ClusterResponse
//	@Failure		404	{object}	map[string]string
//	@Router			/api/v1/clusters/{id} [get]
func (h *ClusterHandlers) GetCluster(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")

	cluster, ok := h.registry.GetCluster(id)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{
			"error": "cluster not found",
		})
	}

	resp := h.clusterToResponse(cluster)

	// Check health
	health, err := h.deployerClient.CheckHealth(ctx, cluster.ID)
	if err != nil {
		resp.Status = string(vicetypes.ClusterUnreachable)
	} else if health.Status == "healthy" {
		resp.Status = string(vicetypes.ClusterHealthy)
	} else {
		resp.Status = string(vicetypes.ClusterUnreachable)
	}
	if !cluster.Enabled {
		resp.Status = string(vicetypes.ClusterDisabled)
	}

	now := time.Now()
	resp.LastHealthCheck = &now

	return c.JSON(http.StatusOK, resp)
}

// UpdateCluster handles PUT /api/v1/clusters/:id
//
//	@Summary		Update cluster configuration
//	@Description	Updates an existing cluster's configuration
//	@Tags			clusters
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string							true	"Cluster ID"
//	@Param			cluster	body		vicetypes.ClusterUpdateRequest	true	"Cluster update"
//	@Success		200		{object}	vicetypes.ClusterResponse
//	@Failure		400		{object}	map[string]string
//	@Failure		404		{object}	map[string]string
//	@Failure		500		{object}	map[string]string
//	@Router			/api/v1/clusters/{id} [put]
func (h *ClusterHandlers) UpdateCluster(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")

	var req vicetypes.ClusterUpdateRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{
			"error": "invalid request body: " + err.Error(),
		})
	}

	cluster, err := h.registry.UpdateCluster(ctx, id, &req)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}

	resp := h.clusterToResponse(cluster)
	resp.Status = "updated"

	return c.JSON(http.StatusOK, resp)
}

// DeleteCluster handles DELETE /api/v1/clusters/:id
//
//	@Summary		Delete a cluster
//	@Description	Removes a cluster from the registry
//	@Tags			clusters
//	@Produce		json
//	@Param			id	path	string	true	"Cluster ID"
//	@Success		204	"No Content"
//	@Failure		404	{object}	map[string]string
//	@Failure		500	{object}	map[string]string
//	@Router			/api/v1/clusters/{id} [delete]
func (h *ClusterHandlers) DeleteCluster(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")

	if err := h.registry.DeleteCluster(ctx, id); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}

	return c.NoContent(http.StatusNoContent)
}

// EnableCluster handles POST /api/v1/clusters/:id/enable
//
//	@Summary		Enable a cluster
//	@Description	Enables a cluster for new deployments
//	@Tags			clusters
//	@Produce		json
//	@Param			id	path		string	true	"Cluster ID"
//	@Success		200	{object}	vicetypes.ClusterResponse
//	@Failure		404	{object}	map[string]string
//	@Failure		500	{object}	map[string]string
//	@Router			/api/v1/clusters/{id}/enable [post]
func (h *ClusterHandlers) EnableCluster(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")

	if err := h.registry.EnableCluster(ctx, id); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}

	cluster, ok := h.registry.GetCluster(id)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{
			"error": "cluster not found",
		})
	}

	resp := h.clusterToResponse(cluster)
	resp.Status = "enabled"

	return c.JSON(http.StatusOK, resp)
}

// DisableCluster handles POST /api/v1/clusters/:id/disable
//
//	@Summary		Disable a cluster
//	@Description	Disables a cluster (no new deployments)
//	@Tags			clusters
//	@Produce		json
//	@Param			id	path		string	true	"Cluster ID"
//	@Success		200	{object}	vicetypes.ClusterResponse
//	@Failure		404	{object}	map[string]string
//	@Failure		500	{object}	map[string]string
//	@Router			/api/v1/clusters/{id}/disable [post]
func (h *ClusterHandlers) DisableCluster(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")

	if err := h.registry.DisableCluster(ctx, id); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}

	cluster, ok := h.registry.GetCluster(id)
	if !ok {
		return c.JSON(http.StatusNotFound, map[string]string{
			"error": "cluster not found",
		})
	}

	resp := h.clusterToResponse(cluster)
	resp.Status = "disabled"

	return c.JSON(http.StatusOK, resp)
}

// ReloadClusters handles POST /api/v1/clusters/reload
//
//	@Summary		Force reload cluster configurations
//	@Description	Forces an immediate reload of cluster configurations from the database
//	@Tags			clusters
//	@Produce		json
//	@Success		200	{object}	map[string]interface{}
//	@Failure		500	{object}	map[string]string
//	@Router			/api/v1/clusters/reload [post]
func (h *ClusterHandlers) ReloadClusters(c echo.Context) error {
	ctx := c.Request().Context()

	if err := h.registry.Reload(ctx); err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
	}

	clusters := h.registry.ListClusters()
	return c.JSON(http.StatusOK, map[string]interface{}{
		"status":        "reloaded",
		"cluster_count": len(clusters),
	})
}

// clusterToResponse converts a ClusterInfo to a ClusterResponse.
func (h *ClusterHandlers) clusterToResponse(cluster *vicetypes.ClusterInfo) vicetypes.ClusterResponse {
	return vicetypes.ClusterResponse{
		ID:          cluster.ID,
		Name:        cluster.Name,
		DeployerURL: cluster.DeployerURL,
		Enabled:     cluster.Enabled,
		Priority:    cluster.Priority,
		MTLSEnabled: cluster.MTLSEnabled,
		CreatedAt:   cluster.CreatedAt,
		UpdatedAt:   cluster.UpdatedAt,
	}
}
