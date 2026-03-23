package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"

	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/typed/apis/v1"
)

// logoutHTTPClient is used for forwarding logout requests to vice-proxy
// sidecars. Short timeout avoids blocking the caller if vice-proxy is down.
var logoutHTTPClient = &http.Client{Timeout: 5 * time.Second}

// noRedirectHTTPClient is like logoutHTTPClient but does not follow redirects.
// Used for vice-proxy endpoints where a redirect indicates an auth wall rather
// than a valid response — following it would return an HTML login page instead
// of a useful error.
var noRedirectHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

var log = common.Log.WithFields(logrus.Fields{"package": "operator"})

// Operator holds the state and dependencies for the vice-operator HTTP handlers.
type Operator struct {
	clientset           kubernetes.Interface
	gatewayClient       gatewayclient.GatewayV1Interface
	namespace           string
	gpuVendor           GPUVendor
	capacityCalc        *CapacityCalculator
	imageCache          *ImageCacheManager
	loadingServiceName  string
	loadingServicePort  int32
	loadingTimeoutMs    int64
	baseDomain          string
	clusterConfigSecret string // Name of the Secret holding cluster config for vice-proxy envFrom.
}

// NewOperator creates a new Operator. Panics if required dependencies are nil
// or invalid, since these indicate programmer error in wiring.
func NewOperator(
	clientset kubernetes.Interface,
	gatewayClient gatewayclient.GatewayV1Interface,
	namespace string,
	gpuVendor GPUVendor,
	capacityCalc *CapacityCalculator,
	imageCache *ImageCacheManager,
	loadingServiceName string,
	loadingServicePort int32,
	loadingTimeoutMs int64,
	baseDomain string,
	clusterConfigSecret string,
) *Operator {
	if clientset == nil {
		panic("operator: clientset must not be nil")
	}
	if gatewayClient == nil {
		panic("operator: gatewayClient must not be nil")
	}
	if namespace == "" {
		panic("operator: namespace must not be empty")
	}
	if capacityCalc == nil {
		panic("operator: capacityCalc must not be nil")
	}
	if imageCache == nil {
		panic("operator: imageCache must not be nil")
	}

	return &Operator{
		clientset:           clientset,
		gatewayClient:       gatewayClient,
		namespace:           namespace,
		gpuVendor:           gpuVendor,
		capacityCalc:        capacityCalc,
		imageCache:          imageCache,
		loadingServiceName:  loadingServiceName,
		loadingServicePort:  loadingServicePort,
		loadingTimeoutMs:    loadingTimeoutMs,
		baseDomain:          baseDomain,
		clusterConfigSecret: clusterConfigSecret,
	}
}

// HandleCapacity returns the current cluster capacity.
//
//	@Summary		Get cluster capacity
//	@Description	Returns the current cluster capacity including available slots,
//	@Description	allocatable CPU/memory, and current usage.
//	@Tags			capacity
//	@Produce		json
//	@Success		200	{object}	operatorclient.CapacityResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/capacity [get]
func (o *Operator) HandleCapacity(c echo.Context) error {
	ctx := c.Request().Context()
	cap, err := o.capacityCalc.Calculate(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	return c.JSON(http.StatusOK, cap)
}

// HandleLaunch receives an AnalysisBundle, transforms routing, and applies
// all resources to the local cluster.
//
//	@Summary		Launch a VICE analysis
//	@Description	Receives a pre-built AnalysisBundle, transforms routing for
//	@Description	this cluster, and applies all K8s resources. Returns 409 if at capacity.
//	@Tags			analyses
//	@Accept			json
//	@Produce		json
//	@Param			request	body		operatorclient.AnalysisBundle	true	"The analysis bundle to launch"
//	@Success		201		{object}	map[string]string
//	@Failure		400		{object}	common.ErrorResponse
//	@Failure		409		{object}	common.ErrorResponse
//	@Failure		500		{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses [post]
func (o *Operator) HandleLaunch(c echo.Context) error {
	ctx := c.Request().Context()

	// Check capacity first.
	cap, err := o.capacityCalc.Calculate(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	var bundle operatorclient.AnalysisBundle
	if err := c.Bind(&bundle); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if bundle.AnalysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysisID is required")
	}

	if cap.AvailableSlots <= 0 {
		log.Infof("launch rejected: at capacity (analysis %s)", bundle.AnalysisID)
		return echo.NewHTTPError(http.StatusConflict, "operator at capacity")
	}

	log.Infof("launching analysis %s", bundle.AnalysisID)

	// Transform the HTTPRoute for the local cluster environment.
	if bundle.HTTPRoute != nil {
		TransformHostnames(bundle.HTTPRoute, o.baseDomain)
		TransformGatewayNamespace(bundle.HTTPRoute, o.namespace)
		TransformBackendToLoadingService(bundle.HTTPRoute, o.loadingServiceName, o.loadingServicePort)
	}

	// Ensure the permissions ConfigMap exists in the bundle (handles bundles
	// created before the permissions feature was added).
	EnsurePermissionsConfigMap(&bundle)

	// Inject per-analysis vice-proxy args and ensure the cluster config secret
	// is referenced as envFrom so vice-proxy gets cluster-level env vars.
	TransformViceProxyArgs(bundle.Deployment, bundle.AnalysisID, o.clusterConfigSecret)

	// Rewrite GPU resource names to match the cluster's GPU vendor.
	TransformGPUVendor(bundle.Deployment, o.gpuVendor)

	// Apply all resources via upsert pattern.
	if err := o.applyBundle(ctx, &bundle); err != nil {
		log.Errorf("launch failed for analysis %s: %v", bundle.AnalysisID, err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	log.Infof("launch succeeded for analysis %s", bundle.AnalysisID)
	return c.JSON(http.StatusCreated, map[string]string{"analysisID": bundle.AnalysisID})
}

// HandleExit deletes all K8s resources associated with an analysis by its
// analysis-id label.
//
//	@Summary		Exit (delete) a VICE analysis
//	@Description	Deletes all K8s resources associated with an analysis.
//	@Tags			analyses
//	@Param			analysis-id	path	string	true	"The analysis ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id} [delete]
func (o *Operator) HandleExit(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	log.Infof("exiting analysis %s", analysisID)

	if err := o.deleteAnalysisResources(ctx, analysisID); err != nil {
		log.Errorf("exit failed for analysis %s: %v", analysisID, err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	log.Infof("exit complete for analysis %s", analysisID)
	return c.NoContent(http.StatusOK)
}

// StatusResponse describes the state of an analysis's K8s resources.
type StatusResponse struct {
	AnalysisID  string           `json:"analysisID"`
	Deployments []DeploymentInfo `json:"deployments"`
	Pods        []PodInfo        `json:"pods"`
	Services    []string         `json:"services"`
	Routes      []string         `json:"routes,omitempty"`
}

// DeploymentInfo holds basic deployment status.
type DeploymentInfo struct {
	Name            string `json:"name"`
	ReadyReplicas   int32  `json:"readyReplicas"`
	DesiredReplicas int32  `json:"desiredReplicas"`
}

// PodInfo holds basic pod status.
type PodInfo struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
	Ready bool   `json:"ready"`
}

// HandleStatus returns the status of all K8s resources for an analysis.
//
//	@Summary		Get analysis status
//	@Description	Returns the status of all K8s resources (deployments, pods,
//	@Description	services, routes) for the given analysis.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path		string	true	"The analysis ID"
//	@Success		200			{object}	StatusResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/status [get]
func (o *Operator) HandleStatus(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}
	log.Debugf("status check for analysis %s", analysisID)

	opts := analysisLabelSelector(analysisID)
	resp := StatusResponse{AnalysisID: analysisID}

	// Deployments
	deps, err := o.clientset.AppsV1().Deployments(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, d := range deps.Items {
		desired := int32(1)
		if d.Spec.Replicas != nil {
			desired = *d.Spec.Replicas
		}
		resp.Deployments = append(resp.Deployments, DeploymentInfo{
			Name:            d.Name,
			ReadyReplicas:   d.Status.ReadyReplicas,
			DesiredReplicas: desired,
		})
	}

	// Pods
	pods, err := o.clientset.CoreV1().Pods(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, p := range pods.Items {
		resp.Pods = append(resp.Pods, PodInfo{
			Name:  p.Name,
			Phase: string(p.Status.Phase),
			Ready: isPodReady(p),
		})
	}

	// Services
	svcs, err := o.clientset.CoreV1().Services(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, s := range svcs.Items {
		resp.Services = append(resp.Services, s.Name)
	}

	// HTTPRoutes
	routes, err := o.gatewayClient.HTTPRoutes(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, r := range routes.Items {
		resp.Routes = append(resp.Routes, r.Name)
	}

	return c.JSON(http.StatusOK, resp)
}

// URLReadyResponse indicates whether a VICE analysis is ready for user access.
type URLReadyResponse struct {
	Ready bool `json:"ready"`
}

// HandleURLReady checks if deployment has ready replicas, service exists,
// and an HTTPRoute exists for the given analysis.
//
//	@Summary		Check if analysis URL is ready
//	@Description	Returns whether the analysis has ready replicas, a service,
//	@Description	and an HTTPRoute.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path		string	true	"The analysis ID"
//	@Success		200			{object}	URLReadyResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/url-ready [get]
func (o *Operator) HandleURLReady(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}
	log.Debugf("url-ready check for analysis %s", analysisID)

	opts := analysisLabelSelector(analysisID)

	// Check deployment ready replicas.
	deps, err := o.clientset.AppsV1().Deployments(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	podReady := false
	for _, d := range deps.Items {
		if d.Status.ReadyReplicas > 0 {
			podReady = true
			break
		}
	}

	// Check service exists.
	svcs, err := o.clientset.CoreV1().Services(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	serviceExists := len(svcs.Items) > 0

	// Check HTTPRoute exists.
	routes, err := o.gatewayClient.HTTPRoutes(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	routingExists := len(routes.Items) > 0

	return c.JSON(http.StatusOK, URLReadyResponse{
		Ready: podReady && serviceExists && routingExists,
	})
}

// HandlePods returns pod information for an analysis.
//
//	@Summary		Get analysis pods
//	@Description	Returns pod name, phase, and readiness for all pods
//	@Description	belonging to the given analysis.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path		string	true	"The analysis ID"
//	@Success		200			{array}		PodInfo
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/pods [get]
func (o *Operator) HandlePods(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}
	log.Debugf("pods check for analysis %s", analysisID)

	opts := analysisLabelSelector(analysisID)
	pods, err := o.clientset.CoreV1().Pods(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	var podInfos []PodInfo
	for _, p := range pods.Items {
		podInfos = append(podInfos, PodInfo{
			Name:  p.Name,
			Phase: string(p.Status.Phase),
			Ready: isPodReady(p),
		})
	}

	return c.JSON(http.StatusOK, podInfos)
}

// isPodReady returns true if the pod has a PodReady condition set to True.
func isPodReady(p apiv1.Pod) bool {
	for _, cond := range p.Status.Conditions {
		if cond.Type == apiv1.PodReady && cond.Status == apiv1.ConditionTrue {
			return true
		}
	}
	return false
}

// LogEntry holds a single container's log output.
type LogEntry struct {
	PodName       string `json:"podName"`
	ContainerName string `json:"containerName"`
	Log           string `json:"log"`
}

// HandleLogs returns container logs for an analysis's pods.
//
//	@Summary		Get analysis logs
//	@Description	Returns the last 5 minutes of container logs for all pods
//	@Description	belonging to the given analysis.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path		string	true	"The analysis ID"
//	@Success		200			{array}		LogEntry
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/logs [get]
func (o *Operator) HandleLogs(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}
	log.Debugf("logs request for analysis %s", analysisID)

	opts := analysisLabelSelector(analysisID)
	pods, err := o.clientset.CoreV1().Pods(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	sinceSeconds := int64(300)
	logOpts := &apiv1.PodLogOptions{
		SinceSeconds: &sinceSeconds,
	}

	var entries []LogEntry
	for _, pod := range pods.Items {
		for _, container := range pod.Spec.Containers {
			logOpts.Container = container.Name
			logReq := o.clientset.CoreV1().Pods(o.namespace).GetLogs(pod.Name, logOpts)
			stream, err := logReq.Stream(ctx)
			if err != nil {
				log.Errorf("error getting logs for %s/%s: %v", pod.Name, container.Name, err)
				continue
			}
			logBytes, err := io.ReadAll(stream)
			_ = stream.Close() //nolint:errcheck // best-effort close inside loop; any error is secondary to read error above
			if err != nil {
				log.Errorf("error reading logs for %s/%s: %v", pod.Name, container.Name, err)
				continue
			}
			entries = append(entries, LogEntry{
				PodName:       pod.Name,
				ContainerName: container.Name,
				Log:           string(logBytes),
			})
		}
	}

	return c.JSON(http.StatusOK, entries)
}

// PermissionsResponse is the response body for HandleGetPermissions.
type PermissionsResponse struct {
	AllowedUsers []string `json:"allowedUsers"`
}

// UpdatePermissionsRequest is the request body for HandleUpdatePermissions.
type UpdatePermissionsRequest struct {
	AllowedUsers []string `json:"allowedUsers"`
}

// findPermissionsConfigMap locates the permissions ConfigMap for an analysis
// by its analysis-id label and permissions- name prefix.
func (o *Operator) findPermissionsConfigMap(ctx context.Context, analysisID string) (*apiv1.ConfigMap, error) {
	opts := analysisLabelSelector(analysisID)
	cmList, err := o.clientset.CoreV1().ConfigMaps(o.namespace).List(ctx, opts)
	if err != nil {
		return nil, err
	}

	prefix := constants.PermissionsConfigMapPrefix + "-"
	for i := range cmList.Items {
		if strings.HasPrefix(cmList.Items[i].Name, prefix) {
			return &cmList.Items[i], nil
		}
	}
	return nil, nil
}

// HandleGetPermissions returns the list of users allowed to access an analysis.
//
//	@Summary		Get analysis permissions
//	@Description	Returns the allowed-users list from the permissions ConfigMap
//	@Description	for the given analysis.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path		string	true	"The analysis ID"
//	@Success		200			{object}	PermissionsResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		404			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/permissions [get]
func (o *Operator) HandleGetPermissions(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	permsCM, err := o.findPermissionsConfigMap(ctx, analysisID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if permsCM == nil {
		return echo.NewHTTPError(http.StatusNotFound, "permissions configmap not found for analysis "+analysisID)
	}

	// Parse the allowed-users file: one username per line, skip blanks.
	var users []string
	for _, line := range strings.Split(permsCM.Data[constants.PermissionsFileName], "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			users = append(users, line)
		}
	}

	return c.JSON(http.StatusOK, PermissionsResponse{AllowedUsers: users})
}

// HandleUpdatePermissions rewrites the permissions ConfigMap for an analysis
// with a new list of allowed users.
//
//	@Summary		Update analysis permissions
//	@Description	Replaces the allowed-users list in the permissions ConfigMap
//	@Description	for the given analysis. The full list must be provided (not incremental).
//	@Tags			analyses
//	@Accept			json
//	@Param			analysis-id	path	string						true	"The analysis ID"
//	@Param			request		body	UpdatePermissionsRequest	true	"The new allowed users list"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		404	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/permissions [put]
func (o *Operator) HandleUpdatePermissions(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	var req UpdatePermissionsRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	// Guard against accidentally clearing all access to the analysis.
	if len(req.AllowedUsers) == 0 {
		return echo.NewHTTPError(http.StatusBadRequest, "allowedUsers must not be empty")
	}

	log.Infof("updating permissions for analysis %s (%d users)", analysisID, len(req.AllowedUsers))

	permsCM, err := o.findPermissionsConfigMap(ctx, analysisID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if permsCM == nil {
		return echo.NewHTTPError(http.StatusNotFound, "permissions configmap not found for analysis "+analysisID)
	}

	// Build the new allowed-users content (one username per line, trailing newline).
	if permsCM.Data == nil {
		permsCM.Data = make(map[string]string)
	}
	permsCM.Data[constants.PermissionsFileName] = strings.Join(req.AllowedUsers, "\n") + "\n"

	if _, err := o.clientset.CoreV1().ConfigMaps(o.namespace).Update(ctx, permsCM, metav1.UpdateOptions{}); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	log.Infof("permissions updated for analysis %s", analysisID)
	return c.NoContent(http.StatusOK)
}

// viceProxyURL builds the in-cluster URL for a vice-proxy sidecar endpoint.
func (o *Operator) viceProxyURL(svcName, path string) string {
	u := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s:%d", svcName, o.namespace, constants.VICEProxyServicePort),
		Path:   path,
	}
	return u.String()
}

// findAnalysisService returns the first Service matching the analysis-id label.
func (o *Operator) findAnalysisService(ctx context.Context, analysisID string) (*apiv1.Service, error) {
	opts := analysisLabelSelector(analysisID)
	svcList, err := o.clientset.CoreV1().Services(o.namespace).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("listing services for analysis %s: %w", analysisID, err)
	}
	if len(svcList.Items) == 0 {
		return nil, nil
	}
	return &svcList.Items[0], nil
}

// HandleBackChannelLogout forwards a Keycloak back-channel logout token to the
// vice-proxy sidecar for the given analysis.
//
//	@Summary		Forward back-channel logout to vice-proxy
//	@Description	Forwards a Keycloak logout_token to the vice-proxy sidecar
//	@Description	for the given analysis, invalidating the user's session.
//	@Tags			analyses
//	@Accept			application/x-www-form-urlencoded
//	@Param			analysis-id		path		string	true	"The analysis ID"
//	@Param			logout_token	formData	string	true	"The Keycloak logout token"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		404	{object}	common.ErrorResponse
//	@Failure		502	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/backchannel-logout [post]
func (o *Operator) HandleBackChannelLogout(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	logoutToken := c.FormValue("logout_token")
	if logoutToken == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "logout_token is required")
	}

	svc, err := o.findAnalysisService(ctx, analysisID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if svc == nil {
		return echo.NewHTTPError(http.StatusNotFound, "no service found for analysis "+analysisID)
	}

	// Forward the logout token to the vice-proxy sidecar.
	proxyURL := o.viceProxyURL(svc.Name, "/backchannel-logout")
	log.Infof("forwarding back-channel logout to %s for analysis %s", proxyURL, analysisID)

	formBody := strings.NewReader(url.Values{"logout_token": {logoutToken}}.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, proxyURL, formBody)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("creating backchannel-logout request: %v", err))
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := logoutHTTPClient.Do(req)
	if err != nil {
		log.Errorf("back-channel logout request to vice-proxy failed for analysis %s: %v", analysisID, err)
		return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("failed to reach vice-proxy: %v", err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort error body read
		log.Errorf("vice-proxy returned %d for back-channel logout on analysis %s: %s", resp.StatusCode, analysisID, string(body))
		return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("vice-proxy returned %d", resp.StatusCode))
	}

	log.Infof("back-channel logout forwarded for analysis %s", analysisID)
	return c.NoContent(http.StatusOK)
}

// HandleLogout forwards a logout request to the vice-proxy sidecar for the
// given analysis. Vice-proxy returns a redirect to Keycloak's logout page;
// this handler captures the redirect URL and returns it in JSON instead of
// following it (the operator is a backend, not a browser).
//
//	@Summary		Forward logout to vice-proxy
//	@Description	Forwards a logout request to the vice-proxy sidecar for the
//	@Description	given analysis. Returns the Keycloak logout redirect URL.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path	string	true	"The analysis ID"
//	@Success		200	{object}	operatorclient.LogoutResponse
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		404	{object}	common.ErrorResponse
//	@Failure		502	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/logout [post]
func (o *Operator) HandleLogout(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	svc, err := o.findAnalysisService(ctx, analysisID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if svc == nil {
		return echo.NewHTTPError(http.StatusNotFound, "no service found for analysis "+analysisID)
	}

	proxyURL := o.viceProxyURL(svc.Name, "/logout")
	log.Infof("forwarding logout to %s for analysis %s", proxyURL, analysisID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyURL, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("creating logout request: %v", err))
	}

	// Use the no-redirect client so we can capture the Keycloak logout URL
	// and return it to the caller instead of following the redirect.
	resp, err := noRedirectHTTPClient.Do(req)
	if err != nil {
		log.Errorf("logout request to vice-proxy failed for analysis %s: %v", analysisID, err)
		return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("failed to reach vice-proxy: %v", err))
	}
	defer func() { _ = resp.Body.Close() }()

	// Vice-proxy returns a 307 redirect to the Keycloak logout URL.
	redirectURL := resp.Header.Get("Location")
	if redirectURL == "" {
		// Non-redirect response — forward the status as-is.
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck // best-effort error body read
		log.Errorf("vice-proxy returned %d with no redirect for logout on analysis %s: %s", resp.StatusCode, analysisID, string(body))
		return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("vice-proxy returned %d with no redirect", resp.StatusCode))
	}

	log.Infof("logout forwarded for analysis %s, redirect: %s", analysisID, redirectURL)
	return c.JSON(http.StatusOK, operatorclient.LogoutResponse{RedirectURL: redirectURL})
}

// HandleGetActiveSessions returns the list of active user sessions for an
// analysis by forwarding the request to the vice-proxy sidecar.
//
//	@Summary		Get active sessions for an analysis
//	@Description	Returns the list of currently active user sessions from the
//	@Description	vice-proxy sidecar for the given analysis.
//	@Tags			analyses
//	@Produce		json
//	@Param			analysis-id	path		string	true	"The analysis ID"
//	@Success		200			{object}	operatorclient.ActiveSessionsResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		404			{object}	common.ErrorResponse
//	@Failure		502			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/active-sessions [get]
func (o *Operator) HandleGetActiveSessions(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	svc, err := o.findAnalysisService(ctx, analysisID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if svc == nil {
		return echo.NewHTTPError(http.StatusNotFound, "no service found for analysis "+analysisID)
	}

	proxyURL := o.viceProxyURL(svc.Name, "/active-sessions")
	log.Infof("fetching active sessions from %s for analysis %s", proxyURL, analysisID)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, proxyURL, nil)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("creating active-sessions request: %v", err))
	}

	resp, err := noRedirectHTTPClient.Do(req)
	if err != nil {
		log.Errorf("active-sessions request to vice-proxy failed for analysis %s: %v", analysisID, err)
		return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("failed to reach vice-proxy: %v", err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("reading vice-proxy response: %v", err))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Errorf("vice-proxy returned %d for active-sessions on analysis %s: %s", resp.StatusCode, analysisID, string(body))
		return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("vice-proxy returned %d", resp.StatusCode))
	}

	// Pass through the JSON response from vice-proxy.
	return c.JSONBlob(http.StatusOK, body)
}

// HandleLogoutUser invalidates all sessions for a specific user in an analysis
// by forwarding the request to the vice-proxy sidecar.
//
//	@Summary		Log out a user from an analysis
//	@Description	Invalidates all active sessions for the given username in the
//	@Description	vice-proxy sidecar for the given analysis.
//	@Tags			analyses
//	@Accept			json
//	@Produce		json
//	@Param			analysis-id	path		string							true	"The analysis ID"
//	@Param			request		body		operatorclient.LogoutUserRequest	true	"The user to log out"
//	@Success		200			{object}	operatorclient.LogoutUserResponse
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		404			{object}	common.ErrorResponse
//	@Failure		502			{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/logout-user [post]
func (o *Operator) HandleLogoutUser(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	var req operatorclient.LogoutUserRequest
	if err := c.Bind(&req); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}
	if req.Username == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "username is required")
	}

	svc, err := o.findAnalysisService(ctx, analysisID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	if svc == nil {
		return echo.NewHTTPError(http.StatusNotFound, "no service found for analysis "+analysisID)
	}

	// Forward the logout-user request to the vice-proxy sidecar.
	proxyURL := o.viceProxyURL(svc.Name, "/logout-user")
	log.Infof("forwarding logout-user to %s for analysis %s (user %s)", proxyURL, analysisID, req.Username)

	reqBody, err := json.Marshal(req)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("marshalling request: %v", err))
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, proxyURL, bytes.NewReader(reqBody))
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("creating logout-user request: %v", err))
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := noRedirectHTTPClient.Do(httpReq)
	if err != nil {
		log.Errorf("logout-user request to vice-proxy failed for analysis %s: %v", analysisID, err)
		return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("failed to reach vice-proxy: %v", err))
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("reading vice-proxy response: %v", err))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Errorf("vice-proxy returned %d for logout-user on analysis %s: %s", resp.StatusCode, analysisID, string(body))
		return echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("vice-proxy returned %d", resp.StatusCode))
	}

	log.Infof("logout-user forwarded for analysis %s (user %s)", analysisID, req.Username)
	return c.JSONBlob(http.StatusOK, body)
}

// HandleSwapRoute manually triggers the route swap for an analysis, pointing
// its HTTPRoute at the analysis Service regardless of readiness.
//
//	@Summary		Manually swap route to analysis service
//	@Description	Swaps the HTTPRoute backend from the loading page service to
//	@Description	the analysis Service. Idempotent.
//	@Tags			analyses
//	@Param			analysis-id	path	string	true	"The analysis ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/swap-route [post]
func (o *Operator) HandleSwapRoute(c echo.Context) error {
	ctx := c.Request().Context()
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	log.Infof("manual route swap requested for analysis %s", analysisID)

	if err := o.SwapRoute(ctx, analysisID); err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	return c.NoContent(http.StatusOK)
}

// HandleListing lists all VICE resources in the operator's namespace,
// returning full resource info for aggregation by app-exposer.
//
//	@Summary		List running VICE analyses
//	@Description	Returns all interactive (VICE) resources in the operator's namespace
//	@Description	including deployments, pods, configmaps, services, and routes.
//	@Tags			analyses
//	@Produce		json
//	@Success		200	{object}	reporting.ResourceInfo
//	@Failure		500	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/analyses [get]
func (o *Operator) HandleListing(c echo.Context) error {
	log.Debug("listing all VICE resources")
	ctx := c.Request().Context()
	viceSelector := labels.Set{"app-type": "interactive"}.AsSelector().String()
	opts := metav1.ListOptions{LabelSelector: viceSelector}

	result := reporting.NewResourceInfo()

	// Deployments
	deps, err := o.clientset.AppsV1().Deployments(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, d := range deps.Items {
		result.Deployments = append(result.Deployments, *reporting.DeploymentInfoFrom(&d))
	}

	// Pods
	pods, err := o.clientset.CoreV1().Pods(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, p := range pods.Items {
		result.Pods = append(result.Pods, *reporting.PodInfoFrom(&p))
	}

	// ConfigMaps
	cms, err := o.clientset.CoreV1().ConfigMaps(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, cm := range cms.Items {
		result.ConfigMaps = append(result.ConfigMaps, *reporting.ConfigMapInfoFrom(&cm))
	}

	// Services
	svcs, err := o.clientset.CoreV1().Services(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, svc := range svcs.Items {
		result.Services = append(result.Services, *reporting.ServiceInfoFrom(&svc))
	}

	// HTTPRoutes
	routes, err := o.gatewayClient.HTTPRoutes(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, route := range routes.Items {
		result.Routes = append(result.Routes, *reporting.RouteInfoFrom(&route))
	}

	return c.JSON(http.StatusOK, result)
}
