package httphandlers

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/incluster"
	"github.com/cyverse-de/app-exposer/permissions"
	"github.com/cyverse-de/model/v10"
	"github.com/labstack/echo/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
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

	found, err := h.incluster.IsAnalysisInCluster(ctx, job.InvocationID)
	if err != nil {
		return err
	}

	// If the deployment for this invocation ID is already in the cluster, there's nothing to do.
	if found {
		return c.NoContent(http.StatusOK)
	}

	// Scheduler is required for multi-cluster routing. Fail fast before doing
	// any expensive work.
	if h.scheduler == nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "no scheduler configured for VICE launches")
	}

	if status, err := h.incluster.ValidateJob(ctx, job); err != nil {
		if validationErr, ok := err.(common.ErrorResponse); ok {
			return validationErr
		}
		return echo.NewHTTPError(status, err.Error())
	}

	// Create the excludes file ConfigMap for the job.
	if err = h.incluster.UpsertExcludesConfigMap(ctx, job); err != nil {
		return err
	}

	// Create the input path list config map
	if err = h.incluster.UpsertInputPathListConfigMap(ctx, job); err != nil {
		return err
	}

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
		log.Infof("debug: job %s before launch: status=%s, app_id=%s, operator_name=%v",
			debugInfo.ID, debugInfo.Status, debugInfo.AppID, debugInfo.OperatorName)
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

// URLReadyResponse indicates whether a VICE analysis is accessible and provides its URL.
type URLReadyResponse struct {
	Ready     bool   `json:"ready"`
	AccessURL string `json:"access_url,omitempty"`
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
//	@Success		200		{object}	URLReadyResponse
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
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("user %s not found", fixedUser))
		}
		return err
	}

	host := c.Param("host")

	// GetIDFromHost queries existing ingresses, so a successful return implies the ingress exists.
	id, err := h.incluster.GetIDFromHost(ctx, host)
	if err != nil {
		return err
	}

	listoptions := metav1.ListOptions{
		LabelSelector: labels.Set{"external-id": id}.AsSelector().String(),
	}

	// Check service existence.
	svclist, err := h.clientset.CoreV1().Services(h.incluster.ViceNamespace).List(ctx, listoptions)
	if err != nil {
		return err
	}
	serviceExists := len(svclist.Items) > 0

	// Check whether any deployment has ready replicas.
	deplist, err := h.clientset.AppsV1().Deployments(h.incluster.ViceNamespace).List(ctx, listoptions)
	if err != nil {
		return err
	}
	var podReady bool
	for _, dep := range deplist.Items {
		if dep.Status.ReadyReplicas > 0 {
			podReady = true
			break
		}
	}

	// Ingress existence is implied by GetIDFromHost succeeding above.
	resourcesReady := serviceExists && podReady

	analysisID, err := h.apps.GetAnalysisIDByExternalID(ctx, id)
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

	data := URLReadyResponse{Ready: false}

	// Only proceed with vice-proxy and public URL checks if k8s resources are ready.
	if resourcesReady {
		accessURL, err := h.incluster.GetAccessURL(ctx, id)
		if err != nil {
			log.Debugf("vice-proxy not reachable for %s: %v", id, err)
			return c.JSON(http.StatusOK, data)
		}

		if err := h.incluster.CheckAccessURL(ctx, accessURL); err != nil {
			log.Debugf("access URL not live for %s: %v", id, err)
			return c.JSON(http.StatusOK, data)
		}

		data.Ready = true
		data.AccessURL = accessURL
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
//	@Success		200		{object}	URLReadyResponse
//	@Failure		400		{object}	common.ErrorResponse
//	@Failure		404		{object}	common.ErrorResponse
//	@Failure		500		{object}	common.ErrorResponse
//	@Router			/vice/admin/{host}/url-ready [get]
func (h *HTTPHandlers) AdminURLReadyHandler(c echo.Context) error {
	ctx := c.Request().Context()
	host := c.Param("host")

	// GetIDFromHost queries existing ingresses, so a successful return implies the ingress exists.
	id, err := h.incluster.GetIDFromHost(ctx, host)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}

	listoptions := metav1.ListOptions{
		LabelSelector: labels.Set{"external-id": id}.AsSelector().String(),
	}

	// Check service existence.
	svclist, err := h.clientset.CoreV1().Services(h.incluster.ViceNamespace).List(ctx, listoptions)
	if err != nil {
		return err
	}
	serviceExists := len(svclist.Items) > 0

	// Check whether any deployment has ready replicas.
	deplist, err := h.clientset.AppsV1().Deployments(h.incluster.ViceNamespace).List(ctx, listoptions)
	if err != nil {
		return err
	}
	var podReady bool
	for _, dep := range deplist.Items {
		if dep.Status.ReadyReplicas > 0 {
			podReady = true
			break
		}
	}

	// Ingress existence is implied by GetIDFromHost succeeding above.
	resourcesReady := serviceExists && podReady
	data := URLReadyResponse{Ready: false}

	// Only proceed with vice-proxy and public URL checks if k8s resources are ready.
	if resourcesReady {
		accessURL, err := h.incluster.GetAccessURL(ctx, id)
		if err != nil {
			log.Debugf("vice-proxy not reachable for %s: %v", id, err)
			return c.JSON(http.StatusOK, data)
		}

		if err := h.incluster.CheckAccessURL(ctx, accessURL); err != nil {
			log.Debugf("access URL not live for %s: %v", id, err)
			return c.JSON(http.StatusOK, data)
		}

		data.Ready = true
		data.AccessURL = accessURL
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

	found, err := h.incluster.IsAnalysisInCluster(ctx, externalID)
	if err != nil {
		return err
	}
	retval := AnalysisInClusterResponse{
		Found: found,
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
//	@Description	Returns whether a deployment for the analysis with the provided external ID is present in the cluster, regardless of its state
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
	externalID, err := h.incluster.GetExternalIDByAnalysisID(ctx, analysisID)
	if err != nil {
		return err
	}
	found, err := h.incluster.IsAnalysisInCluster(ctx, externalID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, AnalysisInClusterResponse{Found: found})
}
