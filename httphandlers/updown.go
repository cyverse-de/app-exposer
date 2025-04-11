package httphandlers

import (
	"net/http"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/labstack/echo/v4"
)

// TriggerDownloadsHandler handles requests to trigger file downloads.
func (h *HTTPHandlers) TriggerDownloadsHandler(c echo.Context) error {
	return h.incluster.DoFileTransfer(c.Request().Context(), c.Param("id"), constants.DownloadBasePath, constants.DownloadKind, true)
}

// TriggerUploadsHandler handles requests to trigger file uploads.
func (h *HTTPHandlers) TriggerUploadsHandler(c echo.Context) error {
	return h.incluster.DoFileTransfer(c.Request().Context(), c.Param("id"), constants.UploadBasePath, constants.UploadKind, true)
}

// AdminTriggerDownloadsHandler handles requests to trigger file downloads
// without requiring user information in the request and also operates from
// the analysis UUID rather than the external ID. For use with tools that
// require the caller to have administrative privileges.
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

// AdminTriggerUploadsHandler handles requests to trigger file uploads without
// requiring user information in the request, while also operating from the
// analysis UUID rather than the external UUID. For use with tools that
// require the caller to have administrative privileges.
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
