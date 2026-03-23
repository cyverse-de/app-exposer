package httphandlers

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// UpdatePermissionsRequest is the request body for the permissions update endpoint.
type UpdatePermissionsRequest struct {
	AllowedUsers []string `json:"allowedUsers"`
}

// UpdatePermissionsHandler pushes a new allowed-users list to the operator
// running the given analysis. The full user list is provided (not incremental).
//
//	@ID				update-permissions
//	@Summary		Update VICE analysis permissions
//	@Description	Pushes an updated list of allowed users to the operator's
//	@Description	permissions ConfigMap for the given analysis.
//	@Accept			json
//	@Param			analysis-id	path	string						true	"The analysis ID"
//	@Param			request		body	UpdatePermissionsRequest		true	"The new allowed users list"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		404	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/{analysis-id}/permissions [put]
func (h *HTTPHandlers) UpdatePermissionsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	var req UpdatePermissionsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if len(req.AllowedUsers) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "allowedUsers must not be empty")
	}

	// Find the operator running this analysis.
	client := h.operatorClientForAnalysis(ctx, analysisID)
	if client == nil {
		return echo.NewHTTPError(http.StatusNotFound, "no operator found for analysis "+analysisID)
	}

	// Push the updated permissions to the operator.
	if err := client.UpdatePermissions(ctx, analysisID, req.AllowedUsers); err != nil {
		log.Errorf("failed to update permissions for analysis %s: %v", analysisID, err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.NoContent(http.StatusOK)
}
