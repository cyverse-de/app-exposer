package operator

import (
	"net/http"
	"time"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/typed/apis/v1"
)

// noRedirectHTTPClient is used for vice-proxy requests where a redirect
// indicates an auth wall rather than a valid response.
var noRedirectHTTPClient = &http.Client{
	Timeout: 5 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

var log = common.Log.WithFields(logrus.Fields{"package": "operator"})

// requiredParam extracts a path parameter and returns 400 if it's empty.
func requiredParam(c echo.Context, name string) (string, error) {
	v := c.Param(name)
	if v == "" {
		return "", echo.NewHTTPError(http.StatusBadRequest, name+" is required")
	}
	return v, nil
}

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

	// Bind and validate first (cheap) before the capacity check (expensive
	// K8s API call) so malformed requests are rejected without wasted work.
	var bundle operatorclient.AnalysisBundle
	if err := c.Bind(&bundle); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	if err := bundle.Validate(); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, err.Error())
	}

	cap, err := o.capacityCalc.Calculate(ctx)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// AvailableSlots: >0 = has capacity, 0 = at capacity, -1 = unlimited.
	if cap.AvailableSlots == 0 {
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
	analysisID, err := requiredParam(c, "analysis-id")
	if err != nil {
		return err
	}

	log.Infof("exiting analysis %s", analysisID)

	if err := o.deleteAnalysisResources(ctx, analysisID); err != nil {
		log.Errorf("exit failed for analysis %s: %v", analysisID, err)
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	log.Infof("exit complete for analysis %s", analysisID)
	return c.NoContent(http.StatusOK)
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
	analysisID, err := requiredParam(c, "analysis-id")
	if err != nil {
		return err
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
