package httphandlers

import (
	"net/http"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/labstack/echo/v4"
)

// ExitHandler terminates a VICE analysis by routing through the appropriate operator.
// It converts an external ID to an analysis ID and delegates cleanup to the operator.
//
//	@ID				exit
//	@Summary		Terminates a VICE analysis
//	@Description	Terminates the VICE analysis deployment and cleans up
//	@Description	resources associated with it. Does not save outputs first. Uses
//	@Description	the external-id label to find all of the objects in the configured
//	@Description	namespace associated with the job. Deletes the following objects:
//	@Description	ingresses, services, deployments, and configmaps.
//	@Param			id	path	string	true	"The external ID of the VICE analysis"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/{id}/exit [post]
func (h *HTTPHandlers) ExitHandler(c echo.Context) error {
	ctx := c.Request().Context()
	externalID := c.Param("id")

	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	log.Infof("routing exit for analysis %s (external ID %s) to operator", analysisID, externalID)

	client := h.operatorClientForAnalysis(ctx, analysisID)
	if client == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "no operator found for analysis")
	}

	return client.Exit(ctx, analysisID)
}

// AdminExitHandler terminates a VICE analysis using the analysis ID directly,
// without requiring external ID conversion. Intended for administrative operations.
//
//	@ID				admin-exit
//	@Summary		Terminates a VICE analysis
//	@Description	Terminates the VICE analysis based on the analysisID and
//	@Description	and should not require any user information to be provided. Otherwise, the
//	@Description	documentation for VICEExit applies here as well.
//	@Param			analysis-id	path	string	true	"The analysis ID of the VICE analysis"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/analyses/{analysis-id}/exit [post]
func (h *HTTPHandlers) AdminExitHandler(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")

	log.Infof("routing admin exit for analysis %s to operator", analysisID)

	client := h.operatorClientForAnalysis(ctx, analysisID)
	if client == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "no operator found for analysis")
	}

	return client.Exit(ctx, analysisID)
}

// SaveAndExitHandler routes a save-and-exit request to the appropriate operator,
// which saves output files before terminating the analysis.
//
//	@ID				save-and-exit
//	@Summary		Save files and terminate VICE analysis
//	@Description	Handles requests to save the output files in iRODS and then exit.
//	@Description	The exit portion will only occur if the save operation succeeds. The operation is
//	@Description	performed inside of a goroutine so that the caller isn't waiting for hours/days for
//	@Description	output file transfers to complete.
//	@Param			id	path	string	true	"external ID of the analysis"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/{id}/save-and-exit [post]
func (h *HTTPHandlers) SaveAndExitHandler(c echo.Context) error {
	ctx := c.Request().Context()
	externalID := c.Param("id")

	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	log.Infof("routing save-and-exit for analysis %s (external ID %s) to operator", analysisID, externalID)

	client := h.operatorClientForAnalysis(ctx, analysisID)
	if client == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "no operator found for analysis")
	}

	return client.SaveAndExit(ctx, analysisID)
}

// AdminSaveAndExitHandler routes an admin save-and-exit request using the analysis ID directly,
// without requiring external ID conversion. Intended for administrative operations.
//
//	@ID				admin-save-and-exit
//	@Summary		Admin endpoint to trigger output file transfer and analysis exit
//	@Description	Handles requests to save the output files in iRODS and
//	@Description	then exit. This version of the call operates based on the analysis ID and does
//	@Description	not require user information to be required by the caller. Otherwise, the docs
//	@Description	for the VICESaveAndExit function apply here as well.
//	@Param			analysis-id	path	string	true	"Analysis ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/analyses/{analysis-id}/save-and-exit [post]
func (h *HTTPHandlers) AdminSaveAndExitHandler(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")

	log.Infof("routing admin save-and-exit for analysis %s to operator", analysisID)

	client := h.operatorClientForAnalysis(ctx, analysisID)
	if client == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "no operator found for analysis")
	}

	return client.SaveAndExit(ctx, analysisID)
}

// TerminateAllResponse contains the results of terminating all running analyses.
type TerminateAllResponse struct {
	VICE        []string `json:"vice"`
	Batch       []string `json:"batch"`
	FailedVICE  []string `json:"failed_vice"`
	FailedBatch []string `json:"failed_batch"`
}

// TerminateAllAnalysesHandler terminates all running analyses (both VICE and batch).
// It queries the database for running analyses, routes each through its appropriate handler,
// and returns a summary of succeeded and failed terminations.
//
//	@ID				terminate-all-analyses
//	@Summary		Terminates all analyses
//	@Description	Terminates all analyses marked as running in the DE database.
//	@Description	Does not check for analyses in the cluster but not marked as running in the database.
//	@Description	Terminates both VICE and batch analyses.
//	@Success		200	{object}	TerminateAllResponse
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/terminate-all [post]
func (h *HTTPHandlers) TerminateAllAnalysesHandler(c echo.Context) error {
	var (
		terminatedVICE  []string
		failedVICE      []string
		terminatedBatch []string
		failedBatch     []string
	)

	ctx := c.Request().Context()

	// Get the list of running analyses.
	interactiveIDs, err := h.apps.ListExternalIDs(ctx, constants.Running, constants.Interactive)
	if err != nil {
		return err
	}

	// We'll always attempt to kill off the batch analyses.
	batchIDs, err := h.apps.ListExternalIDs(ctx, constants.Running, constants.Executable)
	if err != nil {
		return err
	}

	for _, id := range interactiveIDs {
		log.Infof("stopping VICE analysis %s", id)

		analysisID, lookupErr := h.apps.GetAnalysisIDByExternalID(ctx, id)
		if lookupErr != nil {
			log.Errorf("failed to look up analysis ID for external ID %s: %v", id, lookupErr)
			failedVICE = append(failedVICE, id)
			continue
		}

		client := h.operatorClientForAnalysis(ctx, analysisID)
		if client == nil {
			log.Errorf("no operator found for analysis %s (external ID %s)", analysisID, id)
			failedVICE = append(failedVICE, id)
			continue
		}

		if err = client.Exit(ctx, analysisID); err != nil {
			log.Error(err)
			failedVICE = append(failedVICE, id)
			continue
		}
		terminatedVICE = append(terminatedVICE, id)
	}

	for _, id := range batchIDs {
		log.Infof("stopping batch analysis %s", id)
		if err = h.batchadapter.StopWorkflow(ctx, id); err != nil {
			log.Error(err)
			failedBatch = append(failedBatch, id)
			continue
		}
		terminatedBatch = append(terminatedBatch, id)
		log.Debugf("done stopping batch analysis %s", id)
	}

	retval := TerminateAllResponse{
		VICE:        terminatedVICE,
		FailedVICE:  failedVICE,
		Batch:       terminatedBatch,
		FailedBatch: failedBatch,
	}

	return c.JSON(http.StatusOK, retval)
}
