package httphandlers

import (
	"net/http"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/labstack/echo/v4"
)

//	@ID				trigger-downloads
//	@Summary		Triggers input downloads for an analysis
//	@Description	Triggers input downloads for an analysis
//	@Produce		json
//	@Param			id	path	string	true	"External ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/{id}/download-input-files [post]
func (h *HTTPHandlers) TriggerDownloadsHandler(c echo.Context) error {
	return h.incluster.DoFileTransfer(c.Request().Context(), c.Param("id"), constants.DownloadBasePath, constants.DownloadKind, true)
}

//	@ID				trigger-uploads
//	@Summary		Triggers output uploads from an analysis
//	@Description	Triggers output uploads from a running analysis.
//	@Produce		json
//	@Param			id	path	string	true	"External ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/{id}/save-output-files [post]
func (h *HTTPHandlers) TriggerUploadsHandler(c echo.Context) error {
	return h.incluster.DoFileTransfer(c.Request().Context(), c.Param("id"), constants.UploadBasePath, constants.UploadKind, true)
}

//	@ID				admin-trigger-downloads
//	@Summary		Administratively trigger file downloads to an analysis
//	@Description	Handles requests to trigger file downloads
//	@Description	without requiring user information in the request and also operates from
//	@Description	the analysis UUID rather than the external ID. For use with tools that
//	@Description	require the caller to have administrative privileges.
//	@Produce		json
//	@Param			analysis-id	path	string	true	"Analysis ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/analyses/{analysis-id}/download-input-files [post]
func (h *HTTPHandlers) AdminTriggerDownloadsHandler(c echo.Context) error {
	var err error
	ctx := c.Request().Context()

	analysisID := c.Param("analysis-id")

	externalID, err := h.incluster.GetExternalIDByAnalysisID(ctx, analysisID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	return h.incluster.DoFileTransfer(ctx, externalID, constants.DownloadBasePath, constants.DownloadKind, true)
}

//	@ID				admin-trigger-uploads
//	@Summary		Administratively trigger output file uploads from an analysis
//	@Description	Handles requests to trigger file uploads without
//	@Description	requiring user information in the request, while also operating from the
//	@Description	analysis UUID rather than the external UUID. For use with tools that
//	@Description	require the caller to have administrative privileges.
//	@Produce		json
//	@Param			analysis-id	path	string	true	"Analysis ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/analyses/{analysis-id}/save-output-files [post]
func (h *HTTPHandlers) AdminTriggerUploadsHandler(c echo.Context) error {
	var err error
	ctx := c.Request().Context()

	analysisID := c.Param("analysis-id")

	externalID, err := h.incluster.GetExternalIDByAnalysisID(ctx, analysisID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	return h.incluster.DoFileTransfer(ctx, externalID, constants.UploadBasePath, constants.UploadKind, true)
}
