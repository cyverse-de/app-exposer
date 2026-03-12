package httphandlers

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// TriggerDownloadsHandler triggers input file downloads for an analysis
// by routing through the appropriate operator.
//
//	@ID				trigger-downloads
//	@Summary		Triggers input downloads for an analysis
//	@Description	Triggers input downloads for an analysis by routing through
//	@Description	the operator running it.
//	@Produce		json
//	@Param			id	path	string	true	"External ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/{id}/download-input-files [post]
func (h *HTTPHandlers) TriggerDownloadsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	externalID := c.Param("id")

	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	client := h.operatorClientForAnalysis(ctx, analysisID)
	if client == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "no operator found for analysis")
	}

	log.Infof("routing download-input-files for analysis %s to operator %s", analysisID, client.Name())
	return client.DownloadInputFiles(ctx, analysisID)
}

// TriggerUploadsHandler triggers output file uploads from an analysis
// by routing through the appropriate operator.
//
//	@ID				trigger-uploads
//	@Summary		Triggers output uploads from an analysis
//	@Description	Triggers output uploads from a running analysis by routing
//	@Description	through the operator running it.
//	@Produce		json
//	@Param			id	path	string	true	"External ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/{id}/save-output-files [post]
func (h *HTTPHandlers) TriggerUploadsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	externalID := c.Param("id")

	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	client := h.operatorClientForAnalysis(ctx, analysisID)
	if client == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "no operator found for analysis")
	}

	log.Infof("routing save-output-files for analysis %s to operator %s", analysisID, client.Name())
	return client.SaveOutputFiles(ctx, analysisID)
}

// AdminTriggerDownloadsHandler administratively triggers input file downloads
// by routing through the appropriate operator.
//
//	@ID				admin-trigger-downloads
//	@Summary		Administratively trigger file downloads to an analysis
//	@Description	Handles requests to trigger file downloads without requiring
//	@Description	user information, operating from the analysis UUID.
//	@Produce		json
//	@Param			analysis-id	path	string	true	"Analysis ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/analyses/{analysis-id}/download-input-files [post]
func (h *HTTPHandlers) AdminTriggerDownloadsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")

	client := h.operatorClientForAnalysis(ctx, analysisID)
	if client == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "no operator found for analysis")
	}

	log.Infof("routing admin download-input-files for analysis %s to operator %s", analysisID, client.Name())
	return client.DownloadInputFiles(ctx, analysisID)
}

// AdminTriggerUploadsHandler administratively triggers output file uploads
// by routing through the appropriate operator.
//
//	@ID				admin-trigger-uploads
//	@Summary		Administratively trigger output file uploads from an analysis
//	@Description	Handles requests to trigger file uploads without requiring
//	@Description	user information, operating from the analysis UUID.
//	@Produce		json
//	@Param			analysis-id	path	string	true	"Analysis ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/analyses/{analysis-id}/save-output-files [post]
func (h *HTTPHandlers) AdminTriggerUploadsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")

	client := h.operatorClientForAnalysis(ctx, analysisID)
	if client == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "no operator found for analysis")
	}

	log.Infof("routing admin save-output-files for analysis %s to operator %s", analysisID, client.Name())
	return client.SaveOutputFiles(ctx, analysisID)
}
