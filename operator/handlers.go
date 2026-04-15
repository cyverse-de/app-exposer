package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/typed/apis/v1"
)

// HTTPClient is an interface that matches http.Client's Do method.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// noRedirectHTTPClient is used for vice-proxy requests where a redirect
// indicates an auth wall rather than a valid response.
var noRedirectHTTPClient HTTPClient = &http.Client{
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
	gatewayNamespace    string
	gatewayName         string
	gpuVendor           GPUVendor
	capacityCalc        *CapacityCalculator
	imageCache          *ImageCacheManager
	loadingServiceName  string
	loadingServicePort  int32
	loadingTimeoutMs    int64
	baseDomain          string
	clusterConfigSecret string              // Name of the Secret holding cluster config for vice-proxy envFrom.
	egressConfig        NetworkPolicyConfig // Egress policy config for per-analysis policies.
	httpClient          HTTPClient          // Client for contacting the vice-proxy sidecar.
}

// NewOperator creates a new Operator. Panics if required dependencies are nil
// or invalid, since these indicate programmer error in wiring.
func NewOperator(
	clientset kubernetes.Interface,
	gatewayClient gatewayclient.GatewayV1Interface,
	namespace string,
	gatewayNamespace string,
	gatewayName string,
	gpuVendor GPUVendor,
	capacityCalc *CapacityCalculator,
	imageCache *ImageCacheManager,
	loadingServiceName string,
	loadingServicePort int32,
	loadingTimeoutMs int64,
	baseDomain string,
	clusterConfigSecret string,
	egressConfig NetworkPolicyConfig,
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
		gatewayNamespace:    gatewayNamespace,
		gatewayName:         gatewayName,
		gpuVendor:           gpuVendor,
		capacityCalc:        capacityCalc,
		imageCache:          imageCache,
		loadingServiceName:  loadingServiceName,
		loadingServicePort:  loadingServicePort,
		loadingTimeoutMs:    loadingTimeoutMs,
		baseDomain:          baseDomain,
		clusterConfigSecret: clusterConfigSecret,
		egressConfig:        egressConfig,
		httpClient:          noRedirectHTTPClient,
	}
}

// getAccessURL contacts the vice-proxy sidecar through its in-cluster Service
// and returns the full frontend URL. This requires the vice-proxy to be
// running and reachable within the same namespace.
func (o *Operator) getAccessURL(ctx context.Context, serviceName string) (string, error) {
	endpoint := fmt.Sprintf(
		"http://%s.%s.svc.cluster.local:%d/frontend-url",
		serviceName,
		o.namespace,
		constants.VICEProxyServicePort,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build request for %s: %w", endpoint, err)
	}

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to contact vice-proxy at %s: %w", endpoint, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("vice-proxy at %s returned status %d", endpoint, resp.StatusCode)
	}

	var result struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode vice-proxy response from %s: %w", endpoint, err)
	}

	return result.URL, nil
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

		gwNamespace := o.gatewayNamespace
		if gwNamespace == "" {
			gwNamespace = o.namespace
		}
		TransformGatewayNamespace(bundle.HTTPRoute, gwNamespace, o.gatewayName)

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

	// Create a per-analysis egress NetworkPolicy using the cluster's egress
	// config. This is done by vice-operator (not app-exposer) because only
	// the operator knows the cluster environment (blocked CIDRs, Keycloak
	// IPs, internet access setting, etc.). Deployment metadata labels are
	// used (not pod template labels) because they always include analysis-id,
	// which deleteAnalysisResources uses for label-based cleanup.
	bundleLabels := bundle.Deployment.Labels
	np := buildAnalysisEgressPolicy(bundle.AnalysisID, o.namespace, bundleLabels, o.egressConfig)
	if len(np.Spec.Egress) == 0 {
		log.Warnf("analysis %s egress policy has no allow rules; pods will have DNS-only egress", bundle.AnalysisID)
	}
	npClient := o.clientset.NetworkingV1().NetworkPolicies(o.namespace)
	if err := upsert(ctx, npClient, "NetworkPolicy", np.Name, np); err != nil {
		log.Errorf("egress policy failed for analysis %s: %v", bundle.AnalysisID, err)
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

// HandleListing lists interactive (VICE) resources in the operator's namespace,
// optionally filtered by label key-value pairs provided in the query string.
//
//	@Summary		List running VICE analyses
//	@Description	Returns interactive (VICE) resources in the operator's namespace
//	@Description	including deployments, pods, configmaps, services, and routes.
//	@Description	Query parameters are used as label filters.
//	@Tags			analyses
//	@Produce		json
//	@Success		200	{object}	reporting.ResourceInfo
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/analyses [get]
func (o *Operator) HandleListing(c echo.Context) error {
	ctx := c.Request().Context()
	filter := common.FilterMap(c.Request().URL.Query())

	log.Debugf("listing interactive resources with filter: %v", filter)

	// Build label selector starting with the mandatory app-type=interactive label.
	ls := labels.Set{"app-type": "interactive"}
	for k, v := range filter {
		ls[k] = v
	}
	opts := metav1.ListOptions{LabelSelector: ls.AsSelector().String()}

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

// RegenerateResponse summarizes the results of a network policy regeneration.
type RegenerateResponse struct {
	Updated int      `json:"updated"`
	Errors  []string `json:"errors,omitempty"`
}

// HandleRegenerateNetworkPolicies rebuilds and upserts per-analysis egress
// NetworkPolicies for all running analyses using the operator's current
// configuration. This allows admins to roll out config changes (blocked CIDRs,
// Keycloak IPs, internet access setting, etc.) to already-running analyses
// without restarting them.
//
//	@Summary		Regenerate per-analysis network policies
//	@Description	Rebuilds egress NetworkPolicies for all running analyses to
//	@Description	match the operator's current configuration.
//	@Tags			network-policies
//	@Produce		json
//	@Success		200	{object}	RegenerateResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Router			/regenerate-network-policies [post]
func (o *Operator) HandleRegenerateNetworkPolicies(c echo.Context) error {
	ctx := c.Request().Context()
	log.Info("regenerating per-analysis network policies")

	// List all VICE deployments to discover running analyses and their labels.
	viceSelector := labels.Set{"app-type": "interactive"}.AsSelector().String()
	deps, err := o.clientset.AppsV1().Deployments(o.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: viceSelector,
	})
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	npClient := o.clientset.NetworkingV1().NetworkPolicies(o.namespace)
	var updated int
	var errs []string

	for _, dep := range deps.Items {
		analysisID := dep.Labels["analysis-id"]
		if analysisID == "" {
			log.Warnf("deployment %s has no analysis-id label, skipping", dep.Name)
			continue
		}

		bundleLabels := dep.Labels
		np := buildAnalysisEgressPolicy(analysisID, o.namespace, bundleLabels, o.egressConfig)
		if err := upsert(ctx, npClient, "NetworkPolicy", np.Name, np); err != nil {
			log.Errorf("regenerating egress policy for analysis %s: %v", analysisID, err)
			errs = append(errs, fmt.Sprintf("analysis %s: %v", analysisID, err))
			continue
		}
		updated++
		log.Debugf("regenerated egress policy for analysis %s", analysisID)
	}

	log.Infof("network policy regeneration complete: %d updated, %d errors", updated, len(errs))
	return c.JSON(http.StatusOK, RegenerateResponse{
		Updated: updated,
		Errors:  errs,
	})
}
