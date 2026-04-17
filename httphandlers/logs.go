package httphandlers

import (
	"net/http"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/labstack/echo/v4"
)

// LogsHandler handles requests to access the analysis container logs for a pod in a running
// VICE app. Delegate the request to the operator client for the cluster where the
// analysis is running.
//
//	@ID				logs
//	@Summary		Return the logs for a running analysis
//	@Description	Handles requests to access the container logs for a pod in a running
//	@Description	VICE app.
//	@Produce		json
//	@Param			previous	query		bool	false	"Whether to return previously terminated container logs"
//	@Param			since		query		int64	false	"The number of seconds in the past to begin showing logs"
//	@Param			since-time	query		int64	false	"The number of seconds since the epoch to begin showing logs"
//	@Param			tail-lines	query		int64	false	"The number of lines from the end of the log to show"
//	@Param			timestamps	query		bool	false	"Whether to display timestamps at the beginning of each log line"
//	@Param			container	query		string	false	"The name of the container to display logs from"
//	@Success		200			{object}	reporting.VICELogEntry
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		403			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Router			/vice/{analysis-id}/logs [get]
func (h *HTTPHandlers) LogsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param(constants.AnalysisIDLabel)
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
	}

	client, err := h.operatorClientForAnalysis(ctx, analysisID)
	if err != nil {
		log.Errorf("operator routing unavailable for analysis %s: %v", analysisID, err)
		return echo.NewHTTPError(http.StatusServiceUnavailable, "operator routing temporarily unavailable")
	}
	if client == nil {
		return echo.NewHTTPError(http.StatusNotFound, "analysis not found on any operator")
	}

	// Pass all query parameters to the operator.
	logs, err := client.Logs(ctx, analysisID, c.Request().URL.Query())
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, logs)
}
