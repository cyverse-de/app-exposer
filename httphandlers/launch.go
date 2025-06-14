package httphandlers

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/incluster"
	"github.com/cyverse-de/app-exposer/permissions"
	"github.com/cyverse-de/model/v7"
	"github.com/labstack/echo/v4"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

//	@ID				launch-app
//	@Summary		Launch a VICE analysis
//	@Description	The HTTP handler that orchestrates the launching of a VICE analysis inside
//	@Description	the k8s cluster. This gets passed to the router to be associated with a route. The Job
//	@Description	is passed in as the body of the request.
//	@Accept			json
//	@Param			request	body	AnalysisLaunch	true	"The request body containing the analysis details"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/vice/launch [post]
func (h *HTTPHandlers) LaunchAppHandler(c echo.Context) error {
	var (
		job *model.Job
		err error
	)

	ctx := c.Request().Context()

	job = &model.Job{}

	if err = c.Bind(job); err != nil {
		return err
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

	// Create the deployment for the job.
	if err = h.incluster.UpsertDeployment(ctx, deployment, job); err != nil {
		return err
	}

	return nil
}

type URLReadyResponse struct {
	Ready bool `json:"ready"`
}

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
	var (
		ingressExists bool
		serviceExists bool
		podReady      bool
	)

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

	// Use the name of the ingress to retrieve the externalID
	id, err := h.incluster.GetIDFromHost(ctx, host)
	if err != nil {
		return err
	}

	// If getIDFromHost returns without an error, then the ingress exists
	// since the ingresses are looked at for the host.
	ingressExists = true

	set := labels.Set(map[string]string{
		"external-id": id,
	})

	listoptions := metav1.ListOptions{
		LabelSelector: set.AsSelector().String(),
	}

	// check the service existence
	svcclient := h.clientset.CoreV1().Services(h.incluster.ViceNamespace)
	svclist, err := svcclient.List(ctx, listoptions)
	if err != nil {
		return err
	}
	if len(svclist.Items) > 0 {
		serviceExists = true
	}

	// Check pod status through the deployment
	depclient := h.clientset.AppsV1().Deployments(h.incluster.ViceNamespace)
	deplist, err := depclient.List(ctx, listoptions)
	if err != nil {
		return err
	}
	for _, dep := range deplist.Items {
		if dep.Status.ReadyReplicas > 0 {
			podReady = true
		}
	}

	data := URLReadyResponse{
		Ready: ingressExists && serviceExists && podReady,
	}

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

	return c.JSON(http.StatusOK, data)
}

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
	var (
		ingressExists bool
		serviceExists bool
		podReady      bool
	)

	ctx := c.Request().Context()
	host := c.Param("host")

	// Use the name of the ingress to retrieve the externalID
	id, err := h.incluster.GetIDFromHost(ctx, host)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, err.Error())
	}

	// If getIDFromHost returns without an error, then the ingress exists
	// since the ingresses are looked at for the host.
	ingressExists = true

	set := labels.Set(map[string]string{
		"external-id": id,
	})

	listoptions := metav1.ListOptions{
		LabelSelector: set.AsSelector().String(),
	}

	// check the service existence
	svcclient := h.clientset.CoreV1().Services(h.incluster.ViceNamespace)
	svclist, err := svcclient.List(ctx, listoptions)
	if err != nil {
		return err
	}
	if len(svclist.Items) > 0 {
		serviceExists = true
	}

	// Check pod status through the deployment
	depclient := h.clientset.AppsV1().Deployments(h.incluster.ViceNamespace)
	deplist, err := depclient.List(ctx, listoptions)
	if err != nil {
		return err
	}
	for _, dep := range deplist.Items {
		if dep.Status.ReadyReplicas > 0 {
			podReady = true
		}
	}

	data := URLReadyResponse{
		Ready: ingressExists && serviceExists && podReady,
	}

	return c.JSON(http.StatusOK, data)
}
