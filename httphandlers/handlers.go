package httphandlers

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/incluster"
	"github.com/cyverse-de/app-exposer/permissions"
	"github.com/cyverse-de/model/v7"
	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
	"github.com/pkg/errors"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var otelName = "github.com/cyverse-de/app-exposer/handlers"

type HTTPHandlers struct {
	incluster *incluster.Incluster
	apps      *apps.Apps
	clientset kubernetes.Interface
}

func New(incluster *incluster.Incluster, apps *apps.Apps, clientset kubernetes.Interface) *HTTPHandlers {
	return &HTTPHandlers{
		incluster,
		apps,
		clientset,
	}
}

// LaunchAppHandler is the HTTP handler that orchestrates the launching of a VICE analysis inside
// the k8s cluster. This get passed to the router to be associated with a route. The Job
// is passed in as the body of the request.
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

// TriggerDownloadsHandler handles requests to trigger file downloads.
func (h *HTTPHandlers) TriggerDownloadsHandler(c echo.Context) error {
	return h.incluster.DoFileTransfer(c.Request().Context(), c.Param("id"), constants.DownloadBasePath, constants.DownloadKind, true)
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

// TriggerUploadsHandler handles requests to trigger file uploads.
func (h *HTTPHandlers) TriggerUploadsHandler(c echo.Context) error {
	return h.incluster.DoFileTransfer(c.Request().Context(), c.Param("id"), constants.UploadBasePath, constants.UploadKind, true)
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

// URLReadyHandler returns whether or not a VICE app is ready
// for users to access it. This version will check the user's permissions
// and return an error if they aren't allowed to access the running app.
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
	fixedUser := h.incluster.FixUsername(user)
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

	data := map[string]bool{
		"ready": ingressExists && serviceExists && podReady,
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

// AdminURLReadyHandler handles requests to check the status of a running VICE app in K8s.
// This will return an overall status and status for the individual containers in
// the app's pod. Uses the state of the readiness checks in K8s, along with the
// existence of the various resources created for the app.
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

	data := map[string]bool{
		"ready": ingressExists && serviceExists && podReady,
	}

	return c.JSON(http.StatusOK, data)
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

// TimeLimitUpdateHandler handles requests to update the time limit on an already running VICE app.
func (h *HTTPHandlers) TimeLimitUpdateHandler(c echo.Context) error {
	ctx := c.Request().Context()
	log.Info("update time limit called")

	var (
		err  error
		id   string
		user string
	)

	// user is required
	user = c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusForbidden, "user is not set")
	}

	// id is required
	id = c.Param("analysis-id")
	if id == "" {
		idErr := echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
		log.Error(idErr)
		return idErr
	}

	outputMap, err := h.incluster.UpdateTimeLimit(ctx, user, id)
	if err != nil {
		log.Error(err)
		return err
	}

	return c.JSON(http.StatusOK, outputMap)

}

// AdminTimeLimitUpdateHandler is basically the same as VICETimeLimitUpdate
// except that it doesn't require user information in the request.
func (h *HTTPHandlers) AdminTimeLimitUpdateHandler(c echo.Context) error {
	ctx := c.Request().Context()
	var (
		err  error
		id   string
		user string
	)
	// id is required
	id = c.Param("analysis-id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
	}

	user, _, err = h.apps.GetUserByAnalysisID(ctx, id)
	if err != nil {
		return err
	}

	outputMap, err := h.incluster.UpdateTimeLimit(ctx, user, id)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, outputMap)
}

// GetTimeLimitHandler implements the handler for getting the current time limit from the database.
func (h *HTTPHandlers) GetTimeLimitHandler(c echo.Context) error {
	ctx := c.Request().Context()
	log.Info("get time limit called")

	var (
		err        error
		analysisID string
		user       string
		userID     string
	)

	// user is required
	user = c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusForbidden, "user is not set")
	}

	// analysisID is required
	analysisID = c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
	}

	// Could use this to get the username, but we need to not break other services.
	_, userID, err = h.apps.GetUserByAnalysisID(ctx, analysisID)
	if err != nil {
		return err
	}

	outputMap, err := h.incluster.GetTimeLimit(ctx, userID, analysisID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, outputMap)
}

// AdminGetTimeLimitHandler is the same as VICEGetTimeLimit but doesn't require
// any user information in the request.
func (h *HTTPHandlers) AdminGetTimeLimitHandler(c echo.Context) error {
	ctx := c.Request().Context()
	log.Info("get time limit called")

	var (
		err        error
		analysisID string
		userID     string
	)

	// analysisID is required
	analysisID = c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
	}

	// Could use this to get the username, but we need to not break other services.
	_, userID, err = h.apps.GetUserByAnalysisID(ctx, analysisID)
	if err != nil {
		return err
	}

	outputMap, err := h.incluster.GetTimeLimit(ctx, userID, analysisID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, outputMap)
}

// AdminGetExternalIDHandler returns the external ID associated with the analysis ID.
// There is only one external ID for each VICE analysis, unlike non-VICE analyses.
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

	outputMap := map[string]string{
		"externalID": externalID,
	}

	return c.JSON(http.StatusOK, outputMap)
}

// FilterableDeploymentsHandler lists all of the deployments.
func (h *HTTPHandlers) FilterableDeploymentsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	deployments, err := h.incluster.GetFilteredDeployments(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, map[string][]incluster.DeploymentInfo{
		"deployments": deployments,
	})
}

// FilterablePodsHandler returns a listing of the pods in a VICE analysis.
func (h *HTTPHandlers) FilterablePodsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	pods, err := h.incluster.GetFilteredPods(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, map[string][]incluster.PodInfo{
		"pods": pods,
	})
}

// FilterableConfigMapsHandler lists configmaps in use by VICE apps.
func (h *HTTPHandlers) FilterableConfigMapsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	cms, err := h.incluster.GetFilteredConfigMaps(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, map[string][]incluster.ConfigMapInfo{
		"configmaps": cms,
	})
}

// FilterableServicesHandler lists services in use by VICE apps.
func (h *HTTPHandlers) FilterableServicesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	svcs, err := h.incluster.GetFilteredServices(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, map[string][]incluster.ServiceInfo{
		"services": svcs,
	})
}

// FilterableIngressesHandler lists ingresses in use by VICE apps.
func (h *HTTPHandlers) FilterableIngressesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	ingresses, err := h.incluster.GetFilteredIngresses(ctx, filter)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, map[string][]incluster.IngressInfo{
		"ingresses": ingresses,
	})
}

// AdminDescribeAnalysisHandler returns a listing entry for a single analysis
// asssociated with the host/subdomain passed in as 'host' from the URL.
func (h *HTTPHandlers) AdminDescribeAnalysisHandler(c echo.Context) error {
	ctx := c.Request().Context()
	host := c.Param("host")

	filter := map[string]string{
		"subdomain": host,
	}

	listing, err := h.incluster.DoResourceListing(ctx, filter)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, listing)
}

// DescribeAnalysisHandler returns a listing entry for a single analysis associated
// with the host/subdomain passed in as 'host' from the URL.
func (h *HTTPHandlers) DescribeAnalysisHandler(c echo.Context) error {
	ctx := c.Request().Context()

	log.Info("in DescribeAnalysisHandler")
	host := c.Param("host")
	user := c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user query parameter must be set")
	}

	log.Infof("user: %s, user suffix: %s, host: %s", user, h.incluster.UserSuffix, host)

	// Since some usernames don't come through the labelling process unscathed, we have to use
	// the user ID.
	fixedUser := h.incluster.FixUsername(user)
	_, err := h.apps.GetUserID(ctx, fixedUser)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("user %s not found", fixedUser))
		}
		return err
	}

	log.Infof("2 user: %s, user suffix: %s, host: %s", user, h.incluster.UserSuffix, host)

	filter := map[string]string{
		"subdomain": host,
	}

	listing, err := h.incluster.DoResourceListing(ctx, filter)
	if err != nil {
		return err
	}

	// the permissions checks occur after the listing because it's possible for the listing to happen
	// before the subdomain is set in the database, causing an error to get percolated up to the UI.
	// Waiting until the Deployments list contains at least one item should guarantee that the subdomain
	// is set in the database.
	if len(listing.Deployments) > 0 {
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
	}

	return c.JSON(http.StatusOK, listing)
}

// FilterableResourcesHandler returns all of the k8s resources associated with a VICE analysis
// but checks permissions to see if the requesting user has permission to access the resource.
func (h *HTTPHandlers) FilterableResourcesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user query parameter must be set")
	}

	// Since some usernames don't come through the labelling process unscathed, we have to use
	// the user ID.
	user = h.incluster.FixUsername(user)
	userID, err := h.apps.GetUserID(ctx, user)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("user %s not found", user))
		}
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	filter := common.FilterMap(c.Request().URL.Query())
	delete(filter, "user")

	filter["user-id"] = userID

	log.Debugf("user ID is %s", userID)

	listing, err := h.incluster.DoResourceListing(ctx, filter)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, listing)
}

// AdminFilterableResourcesHandler returns all of the k8s resources associated with a VICE analysis.
func (h *HTTPHandlers) AdminFilterableResourcesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	listing, err := h.incluster.DoResourceListing(ctx, filter)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.JSON(http.StatusOK, listing)
}

// ApplyAsyncLabelsHandler is the http handler for triggering the application
// of labels on running VICE analyses.
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

// VICELogEntry contains the data returned for each log request.
type VICELogEntry struct {
	SinceTime string   `json:"since_time"`
	Lines     []string `json:"lines"`
}

// LogsHandler handles requests to access the analysis container logs for a pod in a running
// VICE app. Needs the 'id' and 'pod-name' mux Vars.
//
// Query Parameters:
//
//	previous - Converted to a boolean, should be either true or false. Return previously
//	           terminated container logs.
//	since - Converted to a int64. The number of seconds before the current time at which
//	        to begin showing logs. Yeah, that's a sentence.
//	since-time - Converted to an int64. The number of seconds since the epoch for the time at
//	            which to begin showing logs.
//	tail-lines - Converted to an int64. The number of lines from the end of the log to show.
//	timestamps - Converted to a boolean, should be either true or false. Whether or not to
//	             display timestamps at the beginning of each log line.
//	container - String containing the name of the container to display logs from. Defaults
//	            the value 'analysis', since this is VICE-specific.
func (h *HTTPHandlers) LogsHandler(c echo.Context) error {
	var (
		err        error
		id         string
		since      int64
		sinceTime  int64
		podName    string
		container  string
		previous   bool
		tailLines  int64
		timestamps bool
		user       string
		logOpts    *apiv1.PodLogOptions
	)

	ctx := c.Request().Context()

	// id is required
	id = c.Param("analysis-id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
	}

	// user is required
	user = c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusForbidden, "user is not set")
	}

	externalIDs, err := h.incluster.GetExternalIDs(ctx, user, id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	if len(externalIDs) < 1 {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("no external-ids found for analysis-id %s", id))
	}

	//Just use the first external-id for now.
	externalID := externalIDs[0]

	logOpts = &apiv1.PodLogOptions{}

	// previous is optional
	if c.QueryParam("previous") != "" {
		if previous, err = strconv.ParseBool(c.QueryParam("previous")); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		logOpts.Previous = previous
	}

	// since is optional
	if c.QueryParam("since") != "" {
		if since, err = strconv.ParseInt(c.QueryParam("since"), 10, 64); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		logOpts.SinceSeconds = &since
	}

	if c.QueryParam("since-time") != "" {
		if sinceTime, err = strconv.ParseInt(c.QueryParam("since-time"), 10, 64); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		convertedSinceTime := metav1.Unix(sinceTime, 0)
		logOpts.SinceTime = &convertedSinceTime
	}

	// tail-lines is optional
	if c.QueryParam("tail-lines") != "" {
		if tailLines, err = strconv.ParseInt(c.QueryParam("tail-lines"), 10, 64); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		logOpts.TailLines = &tailLines
	}

	// follow needs to be false for now since upstream services end up using a full thread to process
	// a stream of updates
	logOpts.Follow = false

	// timestamps is optional
	if c.QueryParam("timestamps") != "" {
		if timestamps, err = strconv.ParseBool(c.QueryParam("timestamps")); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		logOpts.Timestamps = timestamps
	}

	// container is optional, but should have a default value of "analysis"
	if c.QueryParam("container") != "" {
		container = c.QueryParam("container")
	} else {
		container = "analysis"
	}

	logOpts.Container = container

	// We're getting a list of pods associated with the first external-id for the analysis,
	// but we're only going to use the first pod for now.
	podList, err := h.incluster.GetPods(ctx, externalID)
	if err != nil {
		return err
	}

	if len(podList) < 1 {
		return fmt.Errorf("no pods found for analysis %s with external ID %s", id, externalID)
	}

	podName = podList[0].Name

	// Finally, actually get the logs and write the response out
	podLogs := h.clientset.CoreV1().Pods(h.incluster.ViceNamespace).GetLogs(podName, logOpts)

	logReadCloser, err := podLogs.Stream(ctx)
	if err != nil {
		return err
	}
	defer logReadCloser.Close()

	bodyBytes, err := io.ReadAll(logReadCloser)
	if err != nil {
		return err
	}

	bodyLines := strings.Split(string(bodyBytes), "\n")
	newSinceTime := fmt.Sprintf("%d", time.Now().Unix())

	return c.JSON(http.StatusOK, &VICELogEntry{
		SinceTime: newSinceTime,
		Lines:     bodyLines,
	})
}

// PodsHandler lists the k8s pods associated with the provided external-id. For now
// just returns pod info in the format `{"pods" : [{}]}`
func (h *HTTPHandlers) PodsHandler(c echo.Context) error {
	ctx := c.Request().Context()

	analysisID := c.Param("analysis-id")
	user := c.QueryParam("user")

	if user == "" {
		return echo.NewHTTPError(http.StatusForbidden, "user not set")
	}

	externalIDs, err := h.incluster.GetExternalIDs(ctx, user, analysisID)
	if err != nil {
		return err
	}

	if len(externalIDs) == 0 {
		return fmt.Errorf("no external-id found for analysis-id %s", analysisID)
	}

	// For now, just use the first external ID
	externalID := externalIDs[0]

	returnedPods, err := h.incluster.GetPods(ctx, externalID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, map[string][]incluster.RetPod{
		"pods": returnedPods,
	})
}

// AsyncDataHandler returns data that is generately asynchronously from the job launch.
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

	return c.JSON(http.StatusOK, map[string]string{
		"analysisID": analysisID,
		"subdomain":  subdomain,
		"ipAddr":     ipAddr,
	})
}
