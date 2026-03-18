package operator

import (
	"io"
	"net/http"

	"github.com/cyverse-de/app-exposer/common"
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

var log = common.Log.WithFields(logrus.Fields{"package": "operator"})

// Operator holds the state and dependencies for the vice-operator HTTP handlers.
type Operator struct {
	clientset          kubernetes.Interface
	gatewayClient      *gatewayclient.GatewayV1Client
	namespace          string
	routingType        RoutingType
	ingressClass       string
	gpuVendor          GPUVendor
	capacityCalc       *CapacityCalculator
	imageCache         *ImageCacheManager
	loadingServiceName string
	loadingServicePort int32
	loadingTimeoutMs   int64
}

// NewOperator creates a new Operator. Panics if required dependencies are nil
// or invalid, since these indicate programmer error in wiring.
func NewOperator(
	clientset kubernetes.Interface,
	gatewayClient *gatewayclient.GatewayV1Client,
	namespace string,
	routingType RoutingType,
	ingressClass string,
	gpuVendor GPUVendor,
	capacityCalc *CapacityCalculator,
	imageCache *ImageCacheManager,
	loadingServiceName string,
	loadingServicePort int32,
	loadingTimeoutMs int64,
) *Operator {
	if clientset == nil {
		panic("operator: clientset must not be nil")
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
		clientset:          clientset,
		gatewayClient:      gatewayClient,
		namespace:          namespace,
		routingType:        routingType,
		ingressClass:       ingressClass,
		gpuVendor:          gpuVendor,
		capacityCalc:       capacityCalc,
		imageCache:         imageCache,
		loadingServiceName: loadingServiceName,
		loadingServicePort: loadingServicePort,
		loadingTimeoutMs:   loadingTimeoutMs,
	}
}

// hasGatewayClient returns true if the operator has a Gateway API client.
func (o *Operator) hasGatewayClient() bool {
	return o.gatewayClient != nil
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

	// Transform routing: convert HTTPRoute to the appropriate resource type.
	if bundle.HTTPRoute != nil {
		route, ingress := TransformRouting(bundle.HTTPRoute, o.routingType, o.ingressClass)
		bundle.HTTPRoute = route
		// If the transform produced an ingress (nginx/tailscale fallback), set it.
		if ingress != nil {
			bundle.Ingress = ingress
		}
	} else if bundle.Ingress != nil {
		// Legacy bundle with only an Ingress: transform it directly.
		bundle.Ingress = TransformIngress(bundle.Ingress, o.routingType, o.ingressClass)
	}

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
	Ingresses   []string         `json:"ingresses"`
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
//	@Description	services, ingresses/routes) for the given analysis.
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

	// Check for HTTPRoutes if gateway client is available.
	if o.hasGatewayClient() {
		routes, err := o.gatewayClient.HTTPRoutes(o.namespace).List(ctx, opts)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		for _, r := range routes.Items {
			resp.Routes = append(resp.Routes, r.Name)
		}
	}

	// Ingresses
	ings, err := o.clientset.NetworkingV1().Ingresses(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, i := range ings.Items {
		resp.Ingresses = append(resp.Ingresses, i.Name)
	}

	return c.JSON(http.StatusOK, resp)
}

// URLReadyResponse indicates whether a VICE analysis is ready for user access.
type URLReadyResponse struct {
	Ready bool `json:"ready"`
}

// HandleURLReady checks if deployment has ready replicas, service exists,
// and routing resource exists for the given analysis.
//
//	@Summary		Check if analysis URL is ready
//	@Description	Returns whether the analysis has ready replicas, a service,
//	@Description	and a routing resource (HTTPRoute or Ingress).
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

	// Check routing resource exists (HTTPRoute or Ingress).
	routingExists := false
	if o.hasGatewayClient() {
		routes, err := o.gatewayClient.HTTPRoutes(o.namespace).List(ctx, opts)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		if len(routes.Items) > 0 {
			routingExists = true
		}
	}
	if !routingExists {
		ings, err := o.clientset.NetworkingV1().Ingresses(o.namespace).List(ctx, opts)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		if len(ings.Items) > 0 {
			routingExists = true
		}
	}

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
			_ = stream.Close()
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

// HandleListing lists all VICE resources in the operator's namespace,
// returning full resource info for aggregation by app-exposer.
//
//	@Summary		List running VICE analyses
//	@Description	Returns all interactive (VICE) resources in the operator's namespace
//	@Description	including deployments, pods, configmaps, services, and ingresses/routes.
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

	// HTTPRoutes (gateway-first)
	if o.hasGatewayClient() {
		routes, err := o.gatewayClient.HTTPRoutes(o.namespace).List(ctx, opts)
		if err != nil {
			return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
		}
		for _, route := range routes.Items {
			result.Routes = append(result.Routes, *reporting.RouteInfoFrom(&route))
		}
	}

	// Ingresses (fallback or legacy)
	ings, err := o.clientset.NetworkingV1().Ingresses(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	for _, ing := range ings.Items {
		result.Ingresses = append(result.Ingresses, *reporting.IngressInfoFrom(&ing))
	}

	return c.JSON(http.StatusOK, result)
}
