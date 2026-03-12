package httphandlers

import (
	"context"
	"net/http"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/labstack/echo/v4"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// @ID				exit
// @Summary		Terminates a VICE analysis
// @Description	Terminates the VICE analysis deployment and cleans up
// @Description	resources asscociated with it. Does not save outputs first. Uses
// @Description	the external-id label to find all of the objects in the configured
// @Description	namespace associated with the job. Deletes the following objects:
// @Description	HTTP routes, services, deployments, and configmaps.
// @Param			id	path	string	true	"The external ID of the VICE analysis"
// @Success		200
// @Failure		400	{object}	common.ErrorResponse
// @Failure		500	{object}	common.ErrorResponse
// @Router			/vice/{id}/exit [post]
func (h *HTTPHandlers) ExitHandler(c echo.Context) error {
	return h.incluster.DoExit(c.Request().Context(), c.Param("id"))
}

// @ID				admin-exit
// @Summary		Terminates a VICE analysis
// @Description	Terminates the VICE analysis based on the analysisID and
// @Description	and should not require any user information to be provided. Otherwise, the
// @Description	documentation for VICEExit applies here as well.
// @Param			analysis-id	path	string	true	"The external ID of the VICE analysis"
// @Success		200
// @Failure		400	{object}	common.ErrorResponse
// @Failure		500	{object}	common.ErrorResponse
// @Router			/vice/admin/analyses/{analysis-id}/exit [post]
func (h *HTTPHandlers) AdminExitHandler(c echo.Context) error {
	var err error
	ctx := c.Request().Context()

	analysisID := c.Param("analysis-id")

	externalID, err := h.incluster.GetExternalIDByAnalysisID(ctx, analysisID)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	return h.incluster.DoExit(ctx, externalID)
}

// @ID				save-and-exit
// @Summary		Save files and terminate VICE analysis
// @Description	Handles requests to save the output files in iRODS and then exit.
// @Description	The exit portion will only occur if the save operation succeeds. The operation is
// @Description	performed inside of a goroutine so that the caller isn't waiting for hours/days for
// @Description	output file transfers to complete.
// @Param			id	path	string	true	"external ID of the analysis"
// @Success		200
// @Failure		400	{object}	common.ErrorResponse
// @Failure		500	{object}	common.ErrorResponse
// @Router			/vice/{id}/save-and-exit [post]
func (h *HTTPHandlers) SaveAndExitHandler(c echo.Context) error {
	log.Info("save and exit called")

	// Since file transfers can take a while, we should do this asynchronously by default.
	go func(ctx context.Context, c echo.Context) {
		var err error
		separatedSpanContext := trace.SpanContextFromContext(ctx)
		outerCtx := trace.ContextWithSpanContext(context.Background(), separatedSpanContext)
		ctx, span := otel.Tracer(otelName).Start(outerCtx, "SaveAndExitHandler goroutine")
		defer span.End()

		externalID := c.Param("id")

		log.Infof("calling doFileTransfer for %s", externalID)

		// Trigger a blocking output file transfer request.
		if err = h.incluster.DoFileTransfer(ctx, externalID, constants.UploadBasePath, constants.UploadKind, false); err != nil {
			log.Error(errors.Wrap(err, "error doing file transfer")) // Log but don't exit. Possible to cancel a job that hasn't started yet
		}

		log.Infof("calling VICEExit for %s", externalID)

		if err = h.incluster.DoExit(ctx, externalID); err != nil {
			log.Error(errors.Wrapf(err, "error triggering analysis exit for %s", externalID))
		}

		log.Infof("after VICEExit for %s", externalID)
	}(c.Request().Context(), c)

	log.Info("leaving save and exit")

	return c.NoContent(http.StatusOK)
}

// @ID				admin-save-and-exit
// @Summary		Admin endpoint to trigger output file transfer and analysis exit
// @Description	Handles requests to save the output files in iRODS and
// @Description	then exit. This version of the call operates based on the analysis ID and does
// @Description	not require user information to be required by the caller. Otherwise, the docs
// @Description	for the VICESaveAndExit function apply here as well.
// @Param			analysis-id	path	string	true	"Analysis ID"
// @Success		200
// @Failure		400	{object}	common.ErrorResponse
// @Failure		500	{object}	common.ErrorResponse
// @Router			/vice/admin/analyses/{}/save-and-exit [post]
func (h *HTTPHandlers) AdminSaveAndExitHandler(c echo.Context) error {
	log.Info("admin save and exit called")

	// Since file transfers can take a while, we should do this asynchronously by default.
	go func(ctx context.Context, c echo.Context) {
		var (
			err        error
			externalID string
		)

		separatedSpanContext := trace.SpanContextFromContext(ctx)
		outerCtx := trace.ContextWithSpanContext(context.Background(), separatedSpanContext)
		ctx, span := otel.Tracer(otelName).Start(outerCtx, "AdminSaveAndExitHandler goroutine")
		defer span.End()

		log.Debug("calling doFileTransfer")

		analysisID := c.Param("analysis-id")

		if externalID, err = h.incluster.GetExternalIDByAnalysisID(ctx, analysisID); err != nil {
			log.Error(err)
			return
		}

		// Trigger a blocking output file transfer request.
		if err = h.incluster.DoFileTransfer(ctx, externalID, constants.UploadBasePath, constants.UploadKind, false); err != nil {
			log.Error(errors.Wrap(err, "error doing file transfer")) // Log but don't exit. Possible to cancel a job that hasn't started yet
		}

		log.Debug("calling VICEExit")

		if err = h.incluster.DoExit(ctx, externalID); err != nil {
			log.Error(err)
		}

		log.Debug("after VICEExit")
	}(c.Request().Context(), c)

	log.Info("admin leaving save and exit")
	return c.NoContent(http.StatusOK)
}

type TerminateAllResponse struct {
	VICE        []string `json:"vice"`
	Batch       []string `json:"batch"`
	FailedVICE  []string `json:"failed_vice"`
	FailedBatch []string `json:"failed_batch"`
}

// @ID				terminate-all-analyses
// @Summary		Terminates all analyses
// @Description	Terminates all analyses marked as running in the DE database.
// @Description	Does not check for analyses in the cluster but not marked as running in the database.
// @Description	Terminates both VICE and batch analyses.
// @Success		200	{object}	TerminateAllResponse
// @Failure		400	{object}	common.ErrorResponse
// @Failure		500	{object}	common.ErrorResponse
// @Router			/vice/admin/terminate-all [post]
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
		if err = h.incluster.DoExit(ctx, id); err != nil {
			log.Error(err)
			failedVICE = append(failedVICE, id)
			continue
		}
		terminatedVICE = append(terminatedVICE, id)
		log.Debugf("done stopping VICE analysis %s", id)
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
