package httphandlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/incluster"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/app-exposer/permissions"
	"github.com/cyverse-de/model/v10"
	"github.com/labstack/echo/v4"
)

// LaunchAppHandler orchestrates the launch of a VICE analysis: validates the job,
// builds K8s resources, selects an operator with capacity, and records the
// operator assignment. Idempotent — returns 200 if the analysis is already running.
//
//	@ID				launch-app
//	@Summary		Launch a VICE analysis
//	@Description	The HTTP handler that orchestrates the launching of a VICE analysis inside
//	@Description	the k8s cluster. This gets passed to the router to be associated with a route. The Job
//	@Description	is passed in as the body of the request.
//	@Accept			json
//	@Param			request						body	AnalysisLaunch	true	"The request body containing the analysis details"
//	@Param			disable-resource-tracking	query	boolean			false	"Bypass resource tracking"	default(false)
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/launch [post]
func (h *HTTPHandlers) LaunchAppHandler(c echo.Context) error {
	ctx := c.Request().Context()

	job := &model.Job{}
	if err := c.Bind(job); err != nil {
		return err
	}

	// Check if already running on any operator for idempotency.
	if client := h.operatorClientForAnalysis(ctx, job.ID); client != nil {
		log.Infof("analysis %s already running on operator %s, returning success", job.ID, client.Name())
		return c.NoContent(http.StatusOK)
	}

	if status, err := h.incluster.ValidateJob(ctx, job); err != nil {
		if validationErr, ok := err.(common.ErrorResponse); ok {
			return validationErr
		}
		return echo.NewHTTPError(status, err.Error())
	}

	// Pre-build the deployment locally just to calculate millicores reservation
	// and validate resource requirements.
	deployment, err := h.incluster.GetDeployment(ctx, job)
	if err != nil {
		return err
	}

	millicores, err := incluster.GetMillicoresFromDeployment(deployment)
	if err != nil {
		return err
	}

	if err = h.apps.SetMillicoresReserved(job, millicores); err != nil {
		return err
	}

	// Log the current database state for this job ID before launching.
	debugInfo, debugErr := h.apps.GetJobDebugInfo(ctx, job.ID)
	if debugErr != nil {
		log.Errorf("debug: failed to query job %s: %v", job.ID, debugErr)
	} else if debugInfo == nil {
		log.Warnf("debug: no jobs row found for ID %s before launch", job.ID)
	} else {
		log.Infof("debug: job %s before launch: status=%s, app_id=%s, operator_id=%v",
			debugInfo.ID, debugInfo.Status, debugInfo.AppID, debugInfo.OperatorID)
	}

	// Build a bundle and route to an operator. Uses job.ID directly because
	// job_steps rows don't exist yet at launch time.
	bundle, err := h.incluster.BuildAnalysisBundle(ctx, job, job.ID)
	if err != nil {
		return err
	}

	operatorName, err := h.scheduler.LaunchAnalysis(ctx, bundle)
	if err != nil {
		return err
	}

	// Record which operator is running this analysis. This is best-effort:
	// the analysis is already running, so a failure here is non-fatal.
	if err := h.apps.SetOperatorName(ctx, job.ID, operatorName); err != nil {
		log.Errorf("failed to set operator name for analysis %s: %v", job.ID, err)
	}

	return c.NoContent(http.StatusOK)
}

// DryRunBundleHandler builds the AnalysisBundle for a job without launching it.
// Useful for debugging, testing, and inspecting what would be sent to a
// vice-operator without any side effects.
//
//	@ID				dry-run-bundle
//	@Summary		Build an AnalysisBundle without launching
//	@Description	Accepts the same model.Job body as /vice/launch but returns the
//	@Description	assembled AnalysisBundle JSON instead of dispatching it to an operator.
//	@Description	No side effects (no ConfigMaps, no scheduling, no DB writes).
//	@Accept			json
//	@Produce		json
//	@Param			request		body		AnalysisLaunch	true	"The job to build a bundle for"
//	@Param			validate	query		boolean			false	"Run validation checks on the job"	default(false)
//	@Success		200			{object}	operatorclient.AnalysisBundle
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Router			/vice/dry-run [post]
func (h *HTTPHandlers) DryRunBundleHandler(c echo.Context) error {
	ctx := c.Request().Context()

	job := &model.Job{}
	if err := c.Bind(job); err != nil {
		return err
	}

	// Opt-in validation via query parameter.
	if c.QueryParam("validate") == "true" {
		if status, err := h.incluster.ValidateJob(ctx, job); err != nil {
			if validationErr, ok := err.(common.ErrorResponse); ok {
				return validationErr
			}
			return echo.NewHTTPError(status, err.Error())
		}
	}

	bundle, err := h.incluster.BuildAnalysisBundle(ctx, job, job.ID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, bundle)
}

// checkURLReady verifies K8s resource readiness by delegating to the
// operator running the analysis. It returns a URLReadyResponse indicating
// if the deployment, service, and routing are fully live.
func (h *HTTPHandlers) checkURLReady(ctx context.Context, analysisID string) (operatorclient.URLReadyResponse, error) {
	client := h.operatorClientForAnalysis(ctx, analysisID)
	if client == nil {
		return operatorclient.URLReadyResponse{Ready: false}, nil
	}

	raw, err := client.URLReady(ctx, analysisID)
	if err != nil {
		return operatorclient.URLReadyResponse{Ready: false}, fmt.Errorf("operator %s url-ready check failed: %w", client.Name(), err)
	}

	var resp operatorclient.URLReadyResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return operatorclient.URLReadyResponse{Ready: false}, fmt.Errorf("failed to decode url-ready response from operator %s: %w", client.Name(), err)
	}

	return resp, nil
}

// URLReadyHandler checks whether the VICE analysis for the given subdomain is
// accessible, verifying user permissions before performing the check.
//
//	@ID				url-ready
//	@Summary		Check if a VICE app is ready for users to access it.
//	@Description	Returns whether or not a VICE app is ready
//	@Description	for users to access it. This version will check the user's permissions
//	@Description	and return an error if they aren't allowed to access the running app.
//	@Produce		json
//	@Param			user	query		string	true	"A user's username"
//	@Param			host	path		string	true	"The subdomain of the analysis. AKA the ingress name"
//	@Success		200		{object}	operatorclient.URLReadyResponse
//	@Failure		400		{object}	common.ErrorResponse
//	@Failure		403		{object}	common.ErrorResponse
//	@Failure		404		{object}	common.ErrorResponse
//	@Failure		500		{object}	common.ErrorResponse
//	@Router			/vice/{host}/url-ready [get]
func (h *HTTPHandlers) URLReadyHandler(c echo.Context) error {
	ctx := c.Request().Context()

	user := c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user query parameter must be set")
	}

	// Since some usernames don't come through the labelling process unscathed, we have to use
	// the user ID.
	fixedUser := common.FixUsername(user, h.incluster.UserSuffix)
	_, err := h.apps.GetUserID(ctx, fixedUser)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("user %s not found", fixedUser))
		}
		return err
	}

	host := c.Param("host")

	params := url.Values{}
	params.Set("subdomain", host)

	// Search all operators for the analysis with this subdomain.
	listing, _, err := h.aggregateListing(ctx, params)
	if err != nil {
		return err
	}
	if len(listing.Deployments) == 0 {
		return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("no deployment found for subdomain %s", host))
	}

	externalID := listing.Deployments[0].ExternalID
	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
	if err != nil {
		return err
	}

	// Make sure the user has permissions to look up info about this analysis.
	p := &permissions.Permissions{
		BaseURL: h.incluster.PermissionsURL,
	}

	allowed, err := p.IsAllowed(ctx, user, analysisID)
	if err != nil {
		return err
	}

	if !allowed {
		return echo.NewHTTPError(http.StatusForbidden, fmt.Sprintf("user %s cannot access analysis %s", user, analysisID))
	}

	data, err := h.checkURLReady(ctx, analysisID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, data)
}

// AdminURLReadyHandler checks K8s resource readiness for the given subdomain
// without user permission checks.
//
//	@ID				admin-url-ready
//	@Summary		Checks the status of a running VICE app in K8s
//	@Description	Handles requests to check the status of a running VICE app in K8s.
//	@Description	This will return an overall status and status for the individual containers in
//	@Description	the app's pod. Uses the state of the readiness checks in K8s, along with the
//	@Description	existence of the various resources created for the app.
//	@Produce		json
//	@Param			host	path		string	true	"The subdomain of the analysis"
//	@Success		200		{object}	operatorclient.URLReadyResponse
//	@Failure		400		{object}	common.ErrorResponse
//	@Failure		404		{object}	common.ErrorResponse
//	@Failure		500		{object}	common.ErrorResponse
//	@Router			/vice/admin/{host}/url-ready [get]
func (h *HTTPHandlers) AdminURLReadyHandler(c echo.Context) error {
	ctx := c.Request().Context()
	host := c.Param("host")

	params := url.Values{}
	params.Set("subdomain", host)

	// Search all operators for the analysis with this subdomain.
	listing, _, err := h.aggregateListing(ctx, params)
	if err != nil {
		return err
	}
	if len(listing.Deployments) == 0 {
		return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("no deployment found for subdomain %s", host))
	}

	externalID := listing.Deployments[0].ExternalID
	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
	if err != nil {
		return err
	}

	data, err := h.checkURLReady(ctx, analysisID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, data)
}

// AnalysisInClusterResponse is the response body for the in-cluster check endpoints.
type AnalysisInClusterResponse struct {
	Found bool `json:"found"`
}

// AdminAnalysisInClusterByExternalID returns whether the provided external ID is
// associated with any Deployments in the cluster, regardless of state. Does
// not check for any other resource types. Does not check if the requesting user
// has access to the analysis.
//
//	@ID				admin-analysis-in-cluster-by-external-id
//	@Summary		Returns whether a deployment for an analysis is in the cluster
//	@Description	Returns whether a deployment for the analysis with the provided external ID is present in the cluster, regardless of its state
//	@Produces		json
//	@Param			external-id	path		string	true	"external id"
//	@Success		200			{object}	AnalysisInClusterResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Router			/vice/admin/is-deployed/external-id/{external-id} [get]
func (h *HTTPHandlers) AdminAnalysisInClusterByExternalID(c echo.Context) error {
	ctx := c.Request().Context()
	externalID := c.Param("external-id")
	if externalID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "external-id is not set")
	}

	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, externalID)
	if err != nil {
		return err
	}

	client := h.searchOperatorsForAnalysis(ctx, analysisID)
	retval := AnalysisInClusterResponse{
		Found: client != nil,
	}
	return c.JSON(http.StatusOK, retval)
}

// AdminAnalysisInClusterByID returns whether the provided analysis ID is
// associated with any Deployments in the cluster, regardless of state. Does
// not check for any other resource types. Does not check if the requesting user
// has access to the analysis.
//
//	@ID				admin-analysis-in-cluster-by-id
//	@Summary		Returns whether a deployment for an analysis is in the cluster
//	@Description	Returns whether a deployment for the analysis with the provided analysis ID is present in the cluster, regardless of its state
//	@Produces		json
//	@Param			analysis-id	path		string	true	"analysis id"
//	@Success		200			{object}	AnalysisInClusterResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Router			/vice/admin/is-deployed/analysis-id/{analysis-id} [get]
func (h *HTTPHandlers) AdminAnalysisInClusterByID(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is not set")
	}

	client := h.searchOperatorsForAnalysis(ctx, analysisID)
	return c.JSON(http.StatusOK, AnalysisInClusterResponse{Found: client != nil})
}
