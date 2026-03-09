package httphandlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/cyverse-de/app-exposer/adapter"
	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/incluster"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/model/v10"
	"github.com/labstack/echo/v4"
	"k8s.io/client-go/kubernetes"
)

var log = common.Log

type AnalysisLaunch model.Analysis

type HTTPHandlers struct {
	incluster    *incluster.Incluster
	apps         *apps.Apps
	clientset    kubernetes.Interface
	batchadapter *adapter.JEXAdapter
	jwksCache    *JWKSCache
	scheduler    *operatorclient.Scheduler
}

func New(incluster *incluster.Incluster, apps *apps.Apps, clientset kubernetes.Interface, batchadapter *adapter.JEXAdapter, jwksCache *JWKSCache) *HTTPHandlers {
	return &HTTPHandlers{
		incluster:    incluster,
		apps:         apps,
		clientset:    clientset,
		batchadapter: batchadapter,
		jwksCache:    jwksCache,
	}
}

// SetScheduler configures the operator scheduler for multi-cluster routing.
// When set, launches and lifecycle operations are routed to remote operators.
func (h *HTTPHandlers) SetScheduler(s *operatorclient.Scheduler) {
	h.scheduler = s
}

// operatorClientForAnalysis looks up which operator is running an analysis
// and returns the corresponding client. Returns nil if no operator name is
// recorded, the scheduler is not configured, or the named operator is not
// found. Callers must treat a nil return as a fatal condition.
func (h *HTTPHandlers) operatorClientForAnalysis(ctx context.Context, analysisID string) *operatorclient.Client {
	operatorName, err := h.apps.GetOperatorName(ctx, analysisID)
	if err != nil {
		log.Errorf("error looking up operator for analysis %s: %v", analysisID, err)
		return nil
	}
	if operatorName == "" {
		log.Debugf("no operator name set for analysis %s, falling back to local", analysisID)
		return nil
	}
	// Scheduler may not be configured if the service is running without
	// operator support (e.g. single-cluster deployments).
	if h.scheduler == nil {
		log.Errorf("operator name %q set for analysis %s but no scheduler is configured", operatorName, analysisID)
		return nil
	}
	client := h.scheduler.ClientByName(operatorName)
	if client == nil {
		log.Warnf("operator %q not found in scheduler for analysis %s", operatorName, analysisID)
	} else {
		log.Debugf("analysis %s routed to operator %q", analysisID, operatorName)
	}
	return client
}

type ExternalIDResp struct {
	ExternalID string `json:"external_id" example:"bb52aefb-e021-4ece-89e5-fd73ce30643c"`
}

// AdminGetExternalIDHandler returns the external ID associated with the analysis ID.
// There is only one external ID for each VICE analysis, unlike non-VICE analyses.
//
//	@ID				admin-get-external-id
//	@Summary		Returns external ID
//	@Description	Returns the external ID associated with the provided analysis ID.
//	@Description	Only returns the first external ID in multi-step analyses.
//	@Produces		json
//	@Param			analysis-id	path		string	true	"analysis UUID"	minLength(36)	maxLength(36)
//	@Success		200			{object}	ExternalIDResp
//	@Failure		500			{object}	common.ErrorResponse
//	@Failure		400			{object}	common.ErrorResponse	"id parameter is empty"
//	@Router			/vice/admin/analyses/{analysis-id}/external-id [get]
func (h *HTTPHandlers) AdminGetExternalIDHandler(c echo.Context) error {
	var (
		err        error
		analysisID string
		externalID string
	)

	ctx := c.Request().Context()

	// analysisID is required
	analysisID = c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
	}

	externalID, err = h.incluster.GetExternalIDByAnalysisID(ctx, analysisID)
	if err != nil {
		return err
	}

	retval := ExternalIDResp{
		ExternalID: externalID,
	}

	return c.JSON(http.StatusOK, retval)
}

// ApplyAsyncLabelsHandler is the http handler for triggering the application
// of labels on running VICE analyses.
//
//	@ID				apply-async-labels
//	@Summary		Applies labels to running VICE analyses.
//	@Description	Asynchronously applies labels to all running VICE analyses.
//	@Description	The application of the labels may not be complete by the time the response is returned.
//	@Success		200
//	@Failure		500	{object}	common.ErrorResponse
//	@Failure		400	{object}	common.ErrorResponse
//	@Router			/vice/apply-labels [post]
func (h *HTTPHandlers) ApplyAsyncLabelsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	errs := h.incluster.ApplyAsyncLabels(ctx)

	if len(errs) > 0 {
		var errMsg strings.Builder
		for _, err := range errs {
			log.Error(err)
			fmt.Fprintf(&errMsg, "%s\n", err.Error())
		}

		return c.String(http.StatusInternalServerError, errMsg.String())
	}
	return c.NoContent(http.StatusOK)
}

type AsyncData struct {
	AnalysisID string `json:"analysisID"`
	Subdomain  string `json:"subdomain"`
	IPAddr     string `json:"ipAddr"`
}

// AsyncDataHandler returns data that is generated asynchronously from the job launch.
//
//	@ID				async-data
//	@Summary		Returns data that is generated asynchronously from the job launch.
//	@Description	Returns data that is applied to analyses outside of an API call.
//	@Description	The returned data is not returned asynchronously, despite the name of the call.
//	@Param			external-id	query		string	true	"External ID"
//	@Success		200			{object}	AsyncData
//	@Failure		500			{object}	common.ErrorResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Router			/vice/async-data [get]
func (h *HTTPHandlers) AsyncDataHandler(c echo.Context) error {
	ctx := c.Request().Context()
	externalID := c.QueryParam("external-id")
	if externalID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "external-id not set")
	}

	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
	if err != nil {
		log.Error(err)
		return err
	}

	filter := map[string]string{
		"external-id": externalID,
	}

	deployments, err := h.incluster.DeploymentList(ctx, h.incluster.ViceNamespace, filter, []string{})
	if err != nil {
		return err
	}

	if len(deployments.Items) < 1 {
		return echo.NewHTTPError(http.StatusNotFound, "no deployments found.")
	}

	labels := deployments.Items[0].GetLabels()
	userID := labels["user-id"]

	subdomain := incluster.IngressName(userID, externalID)
	ipAddr, err := h.apps.GetUserIP(ctx, userID)
	if err != nil {
		log.Error(err)
		return err
	}

	return c.JSON(http.StatusOK, AsyncData{
		AnalysisID: analysisID,
		Subdomain:  subdomain,
		IPAddr:     ipAddr,
	})
}
