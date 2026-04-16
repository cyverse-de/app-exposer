package httphandlers

import (
	"context"
	"net/http"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/labstack/echo/v4"
)

// ExitHandler terminates a VICE analysis by routing through the appropriate operator.
// It converts an external ID to an analysis ID and delegates cleanup to the operator.
//
//	@ID				exit
//	@Summary		Terminates a VICE analysis
//	@Description	Terminates the VICE analysis by routing through the operator
//	@Description	running it. Converts the external ID to an analysis ID and
//	@Description	delegates all resource cleanup to the operator.
//	@Param			id	path	string	true	"The external ID of the VICE analysis"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/{id}/exit [post]
func (h *HTTPHandlers) ExitHandler(c echo.Context) error {
	return h.routeOperatorAction(c, func(ctx context.Context, client *operatorclient.Client, analysisID string) error {
		log.Infof("routing exit for analysis %s to operator %s", analysisID, client.Name())
		return client.Exit(ctx, analysisID)
	})
}

// AdminExitHandler terminates a VICE analysis using the analysis ID directly,
// without requiring external ID conversion. Intended for administrative operations.
//
//	@ID				admin-exit
//	@Summary		Terminates a VICE analysis
//	@Description	Terminates the VICE analysis based on the analysisID and
//	@Description	should not require any user information to be provided. Otherwise,
//	@Description	the documentation for ExitHandler applies here as well.
//	@Param			analysis-id	path	string	true	"The analysis ID of the VICE analysis"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/analyses/{analysis-id}/exit [post]
func (h *HTTPHandlers) AdminExitHandler(c echo.Context) error {
	return h.routeAdminOperatorAction(c, func(ctx context.Context, client *operatorclient.Client, analysisID string) error {
		log.Infof("routing admin exit for analysis %s to operator %s", analysisID, client.Name())
		return client.Exit(ctx, analysisID)
	})
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
	return h.routeOperatorAction(c, func(ctx context.Context, client *operatorclient.Client, analysisID string) error {
		log.Infof("routing save-and-exit for analysis %s to operator %s", analysisID, client.Name())
		return client.SaveAndExit(ctx, analysisID)
	})
}

// AdminSaveAndExitHandler routes an admin save-and-exit request using the analysis ID directly,
// without requiring external ID conversion. Intended for administrative operations.
//
//	@ID				admin-save-and-exit
//	@Summary		Admin endpoint to trigger output file transfer and analysis exit
//	@Description	Handles requests to save the output files in iRODS and
//	@Description	then exit. This version of the call operates based on the analysis ID and does
//	@Description	not require user information to be required by the caller. Otherwise, the docs
//	@Description	for SaveAndExitHandler apply here as well.
//	@Param			analysis-id	path	string	true	"Analysis ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/admin/analyses/{analysis-id}/save-and-exit [post]
func (h *HTTPHandlers) AdminSaveAndExitHandler(c echo.Context) error {
	return h.routeAdminOperatorAction(c, func(ctx context.Context, client *operatorclient.Client, analysisID string) error {
		log.Infof("routing admin save-and-exit for analysis %s to operator %s", analysisID, client.Name())
		return client.SaveAndExit(ctx, analysisID)
	})
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
//	@Description	Terminates both VICE and batch analyses. Returns 207
//	@Description	Multi-Status when any termination failed; the failed_vice
//	@Description	and failed_batch fields list the external IDs that failed.
//	@Success		200	{object}	TerminateAllResponse	"All terminated successfully"
//	@Success		207	{object}	TerminateAllResponse	"Partial success"
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

		client, routeErr := h.operatorClientForAnalysis(ctx, analysisID)
		if routeErr != nil {
			log.Errorf("operator routing unavailable for analysis %s (external ID %s): %v", analysisID, id, routeErr)
			failedVICE = append(failedVICE, id)
			continue
		}
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

	// 207 Multi-Status when any termination failed so automation can
	// distinguish complete success from partial success without parsing
	// the body. Matches the pattern used in HandleRegenerateNetworkPolicies
	// and the image-cache bulk handlers.
	status := http.StatusOK
	if len(failedVICE) > 0 || len(failedBatch) > 0 {
		status = http.StatusMultiStatus
	}
	return c.JSON(status, retval)
}
