package httphandlers

import (
	"context"
	"net/http"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// ExitHandler terminates the VICE analysis deployment and cleans up
// resources asscociated with it. Does not save outputs first. Uses
// the external-id label to find all of the objects in the configured
// namespace associated with the job. Deletes the following objects:
// ingresses, services, deployments, and configmaps.
func (h *HTTPHandlers) ExitHandler(c echo.Context) error {
	return h.incluster.DoExit(c.Request().Context(), c.Param("id"))
}

// AdminExitHandler terminates the VICE analysis based on the analysisID and
// and should not require any user information to be provided. Otherwise, the
// documentation for VICEExit applies here as well.
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

// SaveAndExitHandler handles requests to save the output files in iRODS and then exit.
// The exit portion will only occur if the save operation succeeds. The operation is
// performed inside of a goroutine so that the caller isn't waiting for hours/days for
// output file transfers to complete.
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

	return nil
}

// AdminSaveAndExitHandler handles requests to save the output files in iRODS and
// then exit. This version of the call operates based on the analysis ID and does
// not require user information to be required by the caller. Otherwise, the docs
// for the VICESaveAndExit function apply here as well.
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
	return nil
}
