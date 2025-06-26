package httphandlers

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"
)

type AnalysisStatus struct {
	Status string `json:"status"`
}

// AnalysisStatusByExternalID returns the status of an analysis identified by its external ID.
//
//	@ID				analysis-status-by-external-id
//	@Summary		Returns the status of an analysis
//	@Description	Returns the status of an analysis identified by its external ID.
//	@Param			external-id	path		string	true	"external ID"
//	@Success		200			{object}	AnalysisStatus
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		404			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Router			/info/analysis/status/by/external-id/{external-id} [get]
func (h *HTTPHandlers) AnalysisStatusByExternalID(c echo.Context) error {
	ctx := c.Request().Context()

	externalID := c.Param("external-id")
	if externalID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "external-id parameter is empty")
	}

	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("analysis ID for external ID %s not found", externalID))
		}
		return err
	}

	status, err := h.apps.GetAnalysisStatus(ctx, analysisID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, AnalysisStatus{
		Status: status,
	})
}

// AnalysisStatusByExternalID returns the status of an analysis identified by its analysis ID.
//
//	@ID				analysis-status-by-analysis-id
//	@Summary		Returns the status of an analysis
//	@Description	Returns the status of an analysis identified by its analysis ID.
//	@Param			analysis-id	path		string	true	"analysis ID"
//	@Success		200			{object}	AnalysisStatus
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		404			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Router			/info/analysis/status/by/analysis-id/{analysis-id} [get]
func (h *HTTPHandlers) AnalysisStatusByAnalysisID(c echo.Context) error {
	ctx := c.Request().Context()

	analysisID := c.Param("analysisID")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id parameter is empty")
	}

	status, err := h.apps.GetAnalysisStatus(ctx, analysisID)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("no status found for analysis ID %s", analysisID))
		}
		return err
	}

	return c.JSON(http.StatusOK, AnalysisStatus{
		Status: status,
	})
}
