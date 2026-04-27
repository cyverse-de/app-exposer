# Vice-operator Loading Page Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a loading page to vice-operator that serves as the initial routing target for VICE analyses, swapping the route to the real analysis service once it's ready.

**Architecture:** Vice-operator gets a second HTTP server (loading page port) that serves server-rendered HTML loading pages on analysis subdomains. On bundle launch, the HTTPRoute/Ingress backend is transformed to point at vice-operator's loading page service. The loading page polls for status and triggers an idempotent route swap when the analysis is ready. A manual swap endpoint on the API port enables operational cleanup.

**Tech Stack:** Go `html/template`, `embed.FS`, Echo v4, K8s client-go, Gateway API client

**Spec:** `docs/superpowers/specs/2026-03-18-loading-page-design.md`

---

## File Structure

### New files

| File | Responsibility |
|------|---------------|
| `operator/loading.go` | Loading page HTTP handlers, types (`LoadingStatusResponse`, `LoadingPodInfo`, `LoadingContainerStatus`), subdomain resolution, stage computation |
| `operator/loading_test.go` | Tests for loading page handlers, subdomain resolution, stage computation |
| `operator/routeswap.go` | Routing-type-aware route swap logic (`SwapRoute`) |
| `operator/routeswap_test.go` | Tests for route swap across all routing types |
| `operator/templates/loading.html` | Embedded HTML template with inline CSS and JS |

### Modified files

| File | Change |
|------|--------|
| `operator/transforms.go` | Add `TransformBackendToLoadingService` function |
| `operator/transforms_test.go` | Tests for backend rewrite transform |
| `operator/handlers.go` | Add `loadingServiceName` field to `Operator`; add `HandleSwapRoute` handler; update `NewOperator` |
| `operator/handlers_test.go` | Test `HandleSwapRoute` |
| `cmd/vice-operator/main.go` | Add `--loading-port`, `--loading-service-name`, `--loading-timeout` flags |
| `cmd/vice-operator/app.go` | Create second Echo instance for loading pages; start both servers |

---

### Task 1: Backend rewrite transform

Adds the transform function that rewrites HTTPRoute/Ingress backends to point at the loading page service.

**Files:**
- Modify: `operator/transforms.go`
- Modify: `operator/transforms_test.go`

- [ ] **Step 1: Write failing tests for TransformBackendToLoadingService**

Add to `operator/transforms_test.go`:

```go
func TestTransformBackendToLoadingService(t *testing.T) {
	tests := []struct {
		name        string
		route       *gatewayv1.HTTPRoute
		ingress     *netv1.Ingress
		serviceName string
		servicePort int32
		wantRoute   bool
		wantIngress bool
	}{
		{
			name:        "HTTPRoute backend is rewritten",
			route:       makeTestHTTPRoute(),
			serviceName: "vice-operator-loading",
			servicePort: 80,
			wantRoute:   true,
		},
		{
			name: "Ingress backend is rewritten",
			ingress: &netv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ing"},
				Spec: netv1.IngressSpec{
					DefaultBackend: &netv1.IngressBackend{
						Service: &netv1.IngressServiceBackend{
							Name: "analysis-svc",
							Port: netv1.ServiceBackendPort{Number: 8080},
						},
					},
					Rules: []netv1.IngressRule{
						{
							Host: "abc123.vice.example.com",
							IngressRuleValue: netv1.IngressRuleValue{
								HTTP: &netv1.HTTPIngressRuleValue{
									Paths: []netv1.HTTPIngressPath{
										{
											Backend: netv1.IngressBackend{
												Service: &netv1.IngressServiceBackend{
													Name: "analysis-svc",
													Port: netv1.ServiceBackendPort{Number: 8080},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			serviceName: "vice-operator-loading",
			servicePort: 80,
			wantIngress: true,
		},
		{
			name:        "nil route and nil ingress is no-op",
			serviceName: "vice-operator-loading",
			servicePort: 80,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			TransformBackendToLoadingService(tt.route, tt.ingress, tt.serviceName, tt.servicePort)

			if tt.wantRoute {
				require.NotNil(t, tt.route)
				ref := tt.route.Spec.Rules[0].BackendRefs[0]
				assert.Equal(t, gatewayv1.ObjectName(tt.serviceName), ref.Name)
				assert.Equal(t, gatewayv1.PortNumber(tt.servicePort), *ref.Port)
			}

			if tt.wantIngress {
				require.NotNil(t, tt.ingress)
				assert.Equal(t, tt.serviceName, tt.ingress.Spec.DefaultBackend.Service.Name)
				assert.Equal(t, tt.servicePort, tt.ingress.Spec.DefaultBackend.Service.Port.Number)
				// Rule backends also rewritten.
				for _, rule := range tt.ingress.Spec.Rules {
					for _, path := range rule.HTTP.Paths {
						assert.Equal(t, tt.serviceName, path.Backend.Service.Name)
						assert.Equal(t, tt.servicePort, path.Backend.Service.Port.Number)
					}
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go test ./operator/ -run TestTransformBackendToLoadingService -v`
Expected: compilation error — `TransformBackendToLoadingService` undefined.

- [ ] **Step 3: Implement TransformBackendToLoadingService**

Add to `operator/transforms.go`:

```go
// TransformBackendToLoadingService rewrites the HTTPRoute and/or Ingress
// backend service references to point at the vice-operator loading page
// service. This is called in HandleLaunch so that initial traffic for a new
// analysis routes to the loading page instead of the (not-yet-ready) analysis.
func TransformBackendToLoadingService(
	route *gatewayv1.HTTPRoute,
	ingress *netv1.Ingress,
	serviceName string,
	servicePort int32,
) {
	if route != nil {
		port := gatewayv1.PortNumber(servicePort)
		name := gatewayv1.ObjectName(serviceName)
		for i := range route.Spec.Rules {
			for j := range route.Spec.Rules[i].BackendRefs {
				route.Spec.Rules[i].BackendRefs[j].Name = name
				route.Spec.Rules[i].BackendRefs[j].Port = &port
			}
		}
	}

	if ingress != nil {
		if ingress.Spec.DefaultBackend != nil && ingress.Spec.DefaultBackend.Service != nil {
			ingress.Spec.DefaultBackend.Service.Name = serviceName
			ingress.Spec.DefaultBackend.Service.Port = netv1.ServiceBackendPort{Number: servicePort}
		}
		for i := range ingress.Spec.Rules {
			if ingress.Spec.Rules[i].HTTP == nil {
				continue
			}
			for j := range ingress.Spec.Rules[i].HTTP.Paths {
				if ingress.Spec.Rules[i].HTTP.Paths[j].Backend.Service != nil {
					ingress.Spec.Rules[i].HTTP.Paths[j].Backend.Service.Name = serviceName
					ingress.Spec.Rules[i].HTTP.Paths[j].Backend.Service.Port = netv1.ServiceBackendPort{Number: servicePort}
				}
			}
		}
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go test ./operator/ -run TestTransformBackendToLoadingService -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /home/johnw/work/src/github.com/cyverse-de/app-exposer
git add operator/transforms.go operator/transforms_test.go
git commit -m "feat: add TransformBackendToLoadingService for loading page routing"
```

---

### Task 2: Route swap logic

Implements the routing-type-aware route swap that points the HTTPRoute/Ingress backend at the analysis service when it's ready.

**Files:**
- Create: `operator/routeswap.go`
- Create: `operator/routeswap_test.go`

- [ ] **Step 1: Write failing tests for SwapRoute**

Note: The `Operator` struct stores `*gatewayclient.GatewayV1Client` directly, which makes injecting fakes difficult (the generated fake returns `*FakeGatewayV1`, not `*GatewayV1Client`). These tests cover the Ingress swap path (nginx/tailscale). The gateway HTTPRoute swap path uses the same `swapHTTPRouteBackend` logic and should be verified with integration tests against a real or kind cluster.

Create `operator/routeswap_test.go`:

```go
package operator

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestSwapRoute(t *testing.T) {
	analysisID := "swap-test-1"
	labels := map[string]string{"analysis-id": analysisID}
	targetSvcName := "analysis-svc"

	tests := []struct {
		name        string
		routingType RoutingType
	}{
		{
			name:        "nginx: swaps Ingress backend",
			routingType: RoutingNginx,
		},
		{
			name:        "tailscale: swaps Ingress backend",
			routingType: RoutingTailscale,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			clientset := fake.NewSimpleClientset()
			calc := NewCapacityCalculator(clientset, "vice-apps", 10, "")
			cache := NewImageCacheManager(clientset, "vice-apps", "vice-image-pull-secret")
			op := NewOperator(clientset, nil, "vice-apps", tt.routingType, "nginx", GPUVendorNvidia, calc, cache,
				"vice-operator-loading", 80, 600000)

			// Create the target analysis service.
			_, err := clientset.CoreV1().Services("vice-apps").Create(ctx, &apiv1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: targetSvcName, Labels: labels},
				Spec:       apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{Port: 80}}},
			}, metav1.CreateOptions{})
			require.NoError(t, err)

			// Create ingress pointing at loading page service.
			pathType := netv1.PathTypePrefix
			_, err = clientset.NetworkingV1().Ingresses("vice-apps").Create(ctx, &netv1.Ingress{
				ObjectMeta: metav1.ObjectMeta{Name: "test-ing", Namespace: "vice-apps", Labels: labels},
				Spec: netv1.IngressSpec{
					DefaultBackend: &netv1.IngressBackend{
						Service: &netv1.IngressServiceBackend{
							Name: "vice-operator-loading",
							Port: netv1.ServiceBackendPort{Number: 80},
						},
					},
					Rules: []netv1.IngressRule{
						{
							Host: "abc123.vice.example.com",
							IngressRuleValue: netv1.IngressRuleValue{
								HTTP: &netv1.HTTPIngressRuleValue{
									Paths: []netv1.HTTPIngressPath{
										{
											Path:     "/",
											PathType: &pathType,
											Backend: netv1.IngressBackend{
												Service: &netv1.IngressServiceBackend{
													Name: "vice-operator-loading",
													Port: netv1.ServiceBackendPort{Number: 80},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			}, metav1.CreateOptions{})
			require.NoError(t, err)

			err = op.SwapRoute(ctx, analysisID)
			require.NoError(t, err)

			// Verify the ingress was swapped.
			ings, err := clientset.NetworkingV1().Ingresses("vice-apps").List(ctx, analysisLabelSelector(analysisID))
			require.NoError(t, err)
			require.Len(t, ings.Items, 1)
			assert.Equal(t, targetSvcName, ings.Items[0].Spec.DefaultBackend.Service.Name)
			for _, rule := range ings.Items[0].Spec.Rules {
				for _, path := range rule.HTTP.Paths {
					assert.Equal(t, targetSvcName, path.Backend.Service.Name)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go test ./operator/ -run TestSwapRoute -v`
Expected: compilation error — `SwapRoute` undefined.

- [ ] **Step 3: Implement SwapRoute**

Create `operator/routeswap.go`:

```go
package operator

import (
	"context"
	"fmt"

	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// SwapRoute updates the HTTPRoute or Ingress for the given analysis to point
// at the analysis Service instead of the loading page service. The operation
// is idempotent — calling it when the route already points at the analysis
// service is a no-op (the same values are written).
func (o *Operator) SwapRoute(ctx context.Context, analysisID string) error {
	opts := analysisLabelSelector(analysisID)

	// Find the analysis Service name.
	svcs, err := o.clientset.CoreV1().Services(o.namespace).List(ctx, opts)
	if err != nil {
		return fmt.Errorf("listing services for analysis %s: %w", analysisID, err)
	}
	if len(svcs.Items) == 0 {
		return fmt.Errorf("no service found for analysis %s", analysisID)
	}
	targetSvcName := svcs.Items[0].Name
	// Use port 80 as the default service port for the analysis service.
	var targetPort int32 = 80
	if len(svcs.Items[0].Spec.Ports) > 0 {
		targetPort = svcs.Items[0].Spec.Ports[0].Port
	}

	log.Infof("swapping route for analysis %s to service %s:%d", analysisID, targetSvcName, targetPort)

	// Swap based on routing type to avoid touching resources that don't apply.
	switch o.routingType {
	case RoutingGateway:
		if o.hasGatewayClient() {
			if err := o.swapHTTPRouteBackend(ctx, opts, targetSvcName, targetPort); err != nil {
				return err
			}
		}
	case RoutingNginx, RoutingTailscale:
		if err := o.swapIngressBackend(ctx, opts, targetSvcName, targetPort); err != nil {
			return err
		}
	}

	log.Infof("route swap complete for analysis %s", analysisID)
	return nil
}

// swapHTTPRouteBackend updates all HTTPRoutes matching the selector to point
// at the given service.
func (o *Operator) swapHTTPRouteBackend(
	ctx context.Context,
	opts metav1.ListOptions,
	svcName string,
	svcPort int32,
) error {
	routes, err := o.gatewayClient.HTTPRoutes(o.namespace).List(ctx, opts)
	if err != nil {
		return fmt.Errorf("listing HTTPRoutes: %w", err)
	}

	port := gatewayv1.PortNumber(svcPort)
	name := gatewayv1.ObjectName(svcName)
	for _, route := range routes.Items {
		for i := range route.Spec.Rules {
			for j := range route.Spec.Rules[i].BackendRefs {
				route.Spec.Rules[i].BackendRefs[j].Name = name
				route.Spec.Rules[i].BackendRefs[j].Port = &port
			}
		}
		if _, err := o.gatewayClient.HTTPRoutes(o.namespace).Update(ctx, &route, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("updating HTTPRoute %s: %w", route.Name, err)
		}
	}
	return nil
}

// swapIngressBackend updates all Ingresses matching the selector to point
// at the given service.
func (o *Operator) swapIngressBackend(
	ctx context.Context,
	opts metav1.ListOptions,
	svcName string,
	svcPort int32,
) error {
	ings, err := o.clientset.NetworkingV1().Ingresses(o.namespace).List(ctx, opts)
	if err != nil {
		return fmt.Errorf("listing Ingresses: %w", err)
	}

	for _, ing := range ings.Items {
		if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil {
			ing.Spec.DefaultBackend.Service.Name = svcName
			ing.Spec.DefaultBackend.Service.Port = netv1.ServiceBackendPort{Number: svcPort}
		}
		for i := range ing.Spec.Rules {
			if ing.Spec.Rules[i].HTTP == nil {
				continue
			}
			for j := range ing.Spec.Rules[i].HTTP.Paths {
				if ing.Spec.Rules[i].HTTP.Paths[j].Backend.Service != nil {
					ing.Spec.Rules[i].HTTP.Paths[j].Backend.Service.Name = svcName
					ing.Spec.Rules[i].HTTP.Paths[j].Backend.Service.Port = netv1.ServiceBackendPort{Number: svcPort}
				}
			}
		}
		if _, err := o.clientset.NetworkingV1().Ingresses(o.namespace).Update(ctx, &ing, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("updating Ingress %s: %w", ing.Name, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go test ./operator/ -run TestSwapRoute -v`
Expected: PASS (may need adjustments to the gateway fake client usage in the test)

- [ ] **Step 5: Commit**

```bash
cd /home/johnw/work/src/github.com/cyverse-de/app-exposer
git add operator/routeswap.go operator/routeswap_test.go
git commit -m "feat: add SwapRoute for routing-type-aware route swap"
```

---

### Task 3: Loading page types, stage computation, and Operator struct update

Defines the response types, the logic for computing the loading stage from K8s resource state, and adds the loading-related fields to the `Operator` struct. The struct fields are added here (before Tasks 4-5 which reference them) rather than in Task 6 to keep the code compilable at each step.

**Files:**
- Create: `operator/loading.go`
- Create: `operator/loading_test.go`
- Modify: `operator/handlers.go` (add fields to `Operator` struct and `NewOperator`)
- Modify: `operator/handlers_test.go` (update `newTestOperator`)

- [ ] **Step 0: Update Operator struct and NewOperator**

In `operator/handlers.go`, add fields to the `Operator` struct (after `imageCache` field, line 31):

```go
	loadingServiceName string
	loadingServicePort int32
	loadingTimeoutMs   int64
```

Update `NewOperator` signature to accept these fields:

```go
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
```

And set the new fields in the returned struct.

Update `newTestOperator` in `operator/handlers_test.go`:

```go
func newTestOperator(t *testing.T, maxAnalyses int) (*Operator, *fake.Clientset) {
	t.Helper()
	clientset := fake.NewSimpleClientset()
	calc := NewCapacityCalculator(clientset, "vice-apps", maxAnalyses, "")
	cache := NewImageCacheManager(clientset, "vice-apps", "vice-image-pull-secret")
	op := NewOperator(clientset, nil, "vice-apps", RoutingNginx, "nginx", GPUVendorNvidia, calc, cache,
		"vice-operator-loading", 80, 600000)
	return op, clientset
}
```

Also update the `TestHandleLaunchGPUVendorAMD` test's direct `NewOperator` call (around line 163) to include the new params.

Verify all existing tests still compile and pass:

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go test ./operator/ -v`
Expected: all tests PASS

- [ ] **Step 1: Write failing tests for computeStage and buildLoadingStatus**

Create `operator/loading_test.go`:

```go
package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestComputeStage(t *testing.T) {
	tests := []struct {
		name      string
		pods      []apiv1.Pod
		depReady  bool
		svcExists bool
		wantStage string
		wantError string
	}{
		{
			name:      "no pods returns deploying",
			pods:      nil,
			depReady:  false,
			svcExists: false,
			wantStage: StageDeploying,
		},
		{
			name: "pending pods returns deploying",
			pods: []apiv1.Pod{
				{Status: apiv1.PodStatus{Phase: apiv1.PodPending}},
			},
			depReady:  false,
			svcExists: true,
			wantStage: StageDeploying,
		},
		{
			name: "running pods not ready returns starting",
			pods: []apiv1.Pod{
				{
					Status: apiv1.PodStatus{
						Phase: apiv1.PodRunning,
						ContainerStatuses: []apiv1.ContainerStatus{
							{Name: "analysis", Ready: false},
						},
					},
				},
			},
			depReady:  false,
			svcExists: true,
			wantStage: StageStarting,
		},
		{
			name: "all ready returns almost-ready when dep not ready",
			pods: []apiv1.Pod{
				{
					Status: apiv1.PodStatus{
						Phase: apiv1.PodRunning,
						Conditions: []apiv1.PodCondition{
							{Type: apiv1.PodReady, Status: apiv1.ConditionTrue},
						},
						ContainerStatuses: []apiv1.ContainerStatus{
							{Name: "analysis", Ready: true},
						},
					},
				},
			},
			depReady:  false,
			svcExists: true,
			wantStage: StageAlmostReady,
		},
		{
			name: "dep ready and svc exists returns ready",
			pods: []apiv1.Pod{
				{
					Status: apiv1.PodStatus{
						Phase: apiv1.PodRunning,
						Conditions: []apiv1.PodCondition{
							{Type: apiv1.PodReady, Status: apiv1.ConditionTrue},
						},
						ContainerStatuses: []apiv1.ContainerStatus{
							{Name: "analysis", Ready: true},
						},
					},
				},
			},
			depReady:  true,
			svcExists: true,
			wantStage: StageReady,
		},
		{
			name: "crashloopbackoff returns error",
			pods: []apiv1.Pod{
				{
					Status: apiv1.PodStatus{
						Phase: apiv1.PodRunning,
						ContainerStatuses: []apiv1.ContainerStatus{
							{
								Name:         "analysis",
								Ready:        false,
								RestartCount: 3,
								State: apiv1.ContainerState{
									Waiting: &apiv1.ContainerStateWaiting{
										Reason: "CrashLoopBackOff",
									},
								},
							},
						},
					},
				},
			},
			depReady:  false,
			svcExists: true,
			wantStage: StageError,
			wantError: "container \"analysis\" is in CrashLoopBackOff",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stage, errMsg := computeStage(tt.pods, tt.depReady, tt.svcExists)
			assert.Equal(t, tt.wantStage, stage)
			if tt.wantError != "" {
				assert.Contains(t, errMsg, tt.wantError)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go test ./operator/ -run TestComputeStage -v`
Expected: compilation error — types and functions undefined.

- [ ] **Step 3: Implement types and stage computation**

Add to `operator/loading.go`:

```go
package operator

import (
	"context"
	"embed"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

//go:embed templates/loading.html
var loadingTemplateFS embed.FS

// loadingTemplate is the parsed loading page template. Parsed once at init
// time rather than on every request.
var loadingTemplate = template.Must(template.ParseFS(loadingTemplateFS, "templates/loading.html"))

// Stage constants for the loading page status response.
const (
	StageDeploying   = "deploying"
	StageStarting    = "starting"
	StageAlmostReady = "almost-ready"
	StageReady       = "ready"
	StageError       = "error"
)

// LoadingStatusResponse is the JSON response for the loading page status endpoint.
type LoadingStatusResponse struct {
	Ready bool             `json:"ready"`
	Stage string           `json:"stage"`
	Error string           `json:"error"`
	Pods  []LoadingPodInfo `json:"pods"`
}

// LoadingPodInfo holds pod status for the loading page.
type LoadingPodInfo struct {
	Name              string                  `json:"name"`
	Phase             string                  `json:"phase"`
	Ready             bool                    `json:"ready"`
	RestartCount      int32                   `json:"restartCount"`
	ContainerStatuses []LoadingContainerStatus `json:"containerStatuses"`
}

// LoadingContainerStatus holds per-container status for the loading page.
type LoadingContainerStatus struct {
	Name         string `json:"name"`
	State        string `json:"state"`
	Reason       string `json:"reason"`
	Ready        bool   `json:"ready"`
	RestartCount int32  `json:"restartCount"`
}

// loadingPageData is the template data for the loading page.
type loadingPageData struct {
	AppName    string
	AnalysisID string
	TimeoutMs  int64
}

// computeStage determines the loading stage from pod state and resource readiness.
// Returns the stage string and an error message (empty if no error).
func computeStage(pods []apiv1.Pod, depReady, svcExists bool) (string, string) {
	if len(pods) == 0 {
		return StageDeploying, ""
	}

	// Check for error conditions first.
	for _, pod := range pods {
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.RestartCount > 2 && cs.State.Waiting != nil {
				reason := cs.State.Waiting.Reason
				if reason == "CrashLoopBackOff" || reason == "Error" {
					return StageError, fmt.Sprintf("container %q is in %s (restarted %d times)",
						cs.Name, reason, cs.RestartCount)
				}
			}
		}
	}

	// Check if all pods are pending.
	allPending := true
	for _, pod := range pods {
		if pod.Status.Phase != apiv1.PodPending {
			allPending = false
			break
		}
	}
	if allPending {
		return StageDeploying, ""
	}

	// Check if deployment is ready and service exists.
	if depReady && svcExists {
		return StageReady, ""
	}

	// Check if all pod containers are ready.
	allContainersReady := true
	for _, pod := range pods {
		if !isPodReady(pod) {
			allContainersReady = false
			break
		}
	}
	if allContainersReady {
		return StageAlmostReady, ""
	}

	return StageStarting, ""
}

// buildLoadingPodInfo converts K8s Pod objects to LoadingPodInfo.
func buildLoadingPodInfo(pods []apiv1.Pod) []LoadingPodInfo {
	result := make([]LoadingPodInfo, 0, len(pods))
	for _, pod := range pods {
		var totalRestarts int32
		var containers []LoadingContainerStatus

		for _, cs := range pod.Status.ContainerStatuses {
			totalRestarts += cs.RestartCount
			containers = append(containers, containerStatusToLoading(cs))
		}
		for _, cs := range pod.Status.InitContainerStatuses {
			totalRestarts += cs.RestartCount
			containers = append(containers, containerStatusToLoading(cs))
		}

		result = append(result, LoadingPodInfo{
			Name:              pod.Name,
			Phase:             string(pod.Status.Phase),
			Ready:             isPodReady(pod),
			RestartCount:      totalRestarts,
			ContainerStatuses: containers,
		})
	}
	return result
}

// containerStatusToLoading converts a K8s ContainerStatus to LoadingContainerStatus.
func containerStatusToLoading(cs apiv1.ContainerStatus) LoadingContainerStatus {
	state, reason := containerStateString(cs.State)
	return LoadingContainerStatus{
		Name:         cs.Name,
		State:        state,
		Reason:       reason,
		Ready:        cs.Ready,
		RestartCount: cs.RestartCount,
	}
}

// containerStateString returns a human-readable state and reason from a ContainerState.
func containerStateString(state apiv1.ContainerState) (string, string) {
	if state.Running != nil {
		return "running", ""
	}
	if state.Waiting != nil {
		return "waiting", state.Waiting.Reason
	}
	if state.Terminated != nil {
		return "terminated", state.Terminated.Reason
	}
	return "unknown", ""
}

// resolveSubdomain extracts the subdomain from the Host header and looks up
// the analysis-id and app-name by listing Deployments with matching subdomain label.
// Returns analysisID, appName, error.
func (o *Operator) resolveSubdomain(ctx context.Context, host string) (string, string, error) {
	// Extract subdomain: take the first part before any dot or colon.
	subdomain := host
	if idx := strings.IndexByte(subdomain, '.'); idx != -1 {
		subdomain = subdomain[:idx]
	}
	if idx := strings.IndexByte(subdomain, ':'); idx != -1 {
		subdomain = subdomain[:idx]
	}

	if subdomain == "" {
		return "", "", fmt.Errorf("empty subdomain from host %q", host)
	}

	selector := labels.Set{"subdomain": subdomain}.AsSelector().String()
	deps, err := o.clientset.AppsV1().Deployments(o.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return "", "", fmt.Errorf("listing deployments for subdomain %s: %w", subdomain, err)
	}
	if len(deps.Items) == 0 {
		return "", "", fmt.Errorf("no deployment found for subdomain %s", subdomain)
	}

	dep := deps.Items[0]
	analysisID := dep.Labels["analysis-id"]
	appName := dep.Labels["app-name"]

	return analysisID, appName, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go test ./operator/ -run TestComputeStage -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /home/johnw/work/src/github.com/cyverse-de/app-exposer
git add operator/loading.go operator/loading_test.go
git commit -m "feat: add loading page types, stage computation, and subdomain resolution"
```

---

### Task 4: Loading page HTML template

Creates the embedded HTML template with inline CSS and JS for the loading page.

**Files:**
- Create: `operator/templates/loading.html`

- [ ] **Step 1: Create the loading page template**

Create `operator/templates/loading.html`:

```html
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Launching: {{.AppName}}</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            background: #f5f5f5;
            color: #333;
            display: flex;
            justify-content: center;
            align-items: center;
            min-height: 100vh;
        }
        .container {
            background: white;
            border-radius: 8px;
            box-shadow: 0 2px 8px rgba(0,0,0,0.1);
            padding: 40px;
            max-width: 600px;
            width: 90%;
            text-align: center;
        }
        h1 { font-size: 1.4em; margin-bottom: 24px; font-weight: 500; }
        .progress-bar {
            background: #e0e0e0;
            border-radius: 4px;
            height: 8px;
            margin-bottom: 16px;
            overflow: hidden;
        }
        .progress-fill {
            background: #1976d2;
            height: 100%;
            border-radius: 4px;
            transition: width 0.5s ease;
            width: 0%;
        }
        .progress-fill.error { background: #d32f2f; }
        .status-msg {
            font-size: 0.95em;
            color: #666;
            margin-bottom: 8px;
        }
        .status-msg.error { color: #d32f2f; font-weight: 500; }
        .support-link {
            display: none;
            margin-top: 16px;
            color: #1976d2;
            text-decoration: none;
        }
        .support-link:hover { text-decoration: underline; }
        .details-toggle {
            margin-top: 20px;
            font-size: 0.85em;
            color: #999;
            cursor: pointer;
            user-select: none;
        }
        .details-toggle:hover { color: #666; }
        .details {
            display: none;
            margin-top: 12px;
            text-align: left;
            font-size: 0.8em;
            background: #fafafa;
            border: 1px solid #eee;
            border-radius: 4px;
            padding: 12px;
            max-height: 300px;
            overflow-y: auto;
        }
        .details pre {
            white-space: pre-wrap;
            word-break: break-all;
            font-family: "SF Mono", Monaco, Consolas, monospace;
            font-size: 0.9em;
            line-height: 1.5;
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>Launching: {{.AppName}}</h1>
        <div class="progress-bar">
            <div class="progress-fill" id="progress"></div>
        </div>
        <div class="status-msg" id="status">Connecting...</div>
        <a class="support-link" id="support" href="mailto:support@cyverse.org?subject=VICE%20Launch%20Failure%20-%20{{.AnalysisID}}">
            Contact Support
        </a>
        <div class="details-toggle" id="toggle">Show Details</div>
        <div class="details" id="details">
            <pre id="details-content">Waiting for status...</pre>
        </div>
    </div>

    <script>
    (function() {
        var analysisID = "{{.AnalysisID}}";
        var timeoutMs = {{.TimeoutMs}};
        var startTime = Date.now();
        var pollInterval = 5000;
        var progress = document.getElementById("progress");
        var status = document.getElementById("status");
        var support = document.getElementById("support");
        var toggle = document.getElementById("toggle");
        var details = document.getElementById("details");
        var detailsContent = document.getElementById("details-content");
        var detailsVisible = false;

        toggle.addEventListener("click", function() {
            detailsVisible = !detailsVisible;
            details.style.display = detailsVisible ? "block" : "none";
            toggle.textContent = detailsVisible ? "Hide Details" : "Show Details";
        });

        var stageProgress = {
            "deploying": 20,
            "starting": 50,
            "almost-ready": 80,
            "ready": 100,
            "error": 0
        };

        var stageMessages = {
            "deploying": "Deploying...",
            "starting": "Starting...",
            "almost-ready": "Almost ready...",
            "ready": "Ready! Redirecting...",
            "error": "Failed to start."
        };

        function poll() {
            // Check client-side timeout.
            if (timeoutMs > 0 && (Date.now() - startTime) > timeoutMs) {
                showError("Launch timed out. Please contact support.");
                return;
            }

            fetch("/loading/status")
                .then(function(resp) {
                    if (!resp.ok) throw new Error("status " + resp.status);
                    return resp.json();
                })
                .then(function(data) {
                    var pct = stageProgress[data.stage] || 0;
                    progress.style.width = pct + "%";

                    if (data.stage === "error") {
                        showError(data.error || stageMessages.error);
                        updateDetails(data);
                        return;
                    }

                    status.textContent = stageMessages[data.stage] || data.stage;
                    status.className = "status-msg";
                    progress.className = "progress-fill";

                    updateDetails(data);

                    if (data.ready) {
                        // Route has been swapped. Redirect to the same URL which
                        // now serves the real app.
                        setTimeout(function() {
                            window.location.reload();
                        }, 1000);
                        return;
                    }

                    setTimeout(poll, pollInterval);
                })
                .catch(function(err) {
                    // Network errors during loading are expected (e.g., route
                    // propagation delay). Keep polling.
                    console.warn("poll error:", err);
                    setTimeout(poll, pollInterval);
                });
        }

        function showError(msg) {
            status.textContent = msg;
            status.className = "status-msg error";
            progress.className = "progress-fill error";
            support.style.display = "inline";
        }

        function updateDetails(data) {
            if (!data.pods || data.pods.length === 0) {
                detailsContent.textContent = "No pods found yet.";
                return;
            }
            var lines = [];
            data.pods.forEach(function(pod) {
                lines.push("Pod: " + pod.name + " (" + pod.phase + ", ready=" + pod.ready + ")");
                if (pod.containerStatuses) {
                    pod.containerStatuses.forEach(function(cs) {
                        var line = "  " + cs.name + ": " + cs.state;
                        if (cs.reason) line += " (" + cs.reason + ")";
                        line += " ready=" + cs.ready + " restarts=" + cs.restartCount;
                        lines.push(line);
                    });
                }
            });
            detailsContent.textContent = lines.join("\n");
        }

        // Start polling.
        poll();
    })();
    </script>
</body>
</html>
```

- [ ] **Step 2: Verify the template parses**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go vet ./operator/`
Expected: no errors (the embed directive in loading.go references this template)

- [ ] **Step 3: Commit**

```bash
cd /home/johnw/work/src/github.com/cyverse-de/app-exposer
git add operator/templates/loading.html
git commit -m "feat: add loading page HTML template"
```

---

### Task 5: Loading page HTTP handlers

Adds the handlers that serve the loading page and the status endpoint on the loading page port.

**Files:**
- Modify: `operator/loading.go`
- Modify: `operator/loading_test.go`

- [ ] **Step 1: Write failing tests for HandleLoadingPage and HandleLoadingStatus**

Add to `operator/loading_test.go`:

```go
func TestHandleLoadingPage(t *testing.T) {
	op, clientset := newTestOperator(t, 10)
	ctx := context.Background()
	analysisID := "loading-page-test"

	// Create a deployment with subdomain and app-name labels.
	_, err := clientset.AppsV1().Deployments("vice-apps").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-dep",
			Labels: map[string]string{
				"analysis-id": analysisID,
				"subdomain":   "a1234abcd",
				"app-name":    "JupyterLab",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "test"}},
				Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "a1234abcd.cyverse.run"
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err = op.HandleLoadingPage(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "JupyterLab")
	assert.Contains(t, rec.Body.String(), analysisID)
}

func TestHandleLoadingPageUnknownSubdomain(t *testing.T) {
	op, _ := newTestOperator(t, 10)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "unknown.cyverse.run"
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := op.HandleLoadingPage(c)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, he.Code)
}

func TestHandleLoadingStatus(t *testing.T) {
	op, clientset := newTestOperator(t, 10)
	ctx := context.Background()
	analysisID := "status-test"

	// Create deployment with subdomain label.
	_, err := clientset.AppsV1().Deployments("vice-apps").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dep1",
			Labels: map[string]string{
				"analysis-id": analysisID,
				"subdomain":   "b5678efgh",
				"app-name":    "RStudio",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "test"}},
				Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/loading/status", nil)
	req.Host = "b5678efgh.cyverse.run"
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err = op.HandleLoadingStatus(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp LoadingStatusResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.False(t, resp.Ready)
	assert.Equal(t, StageDeploying, resp.Stage)
}
```

Also add a test for the ready state with route swap:

```go
func TestHandleLoadingStatusReady(t *testing.T) {
	op, clientset := newTestOperator(t, 10)
	ctx := context.Background()
	analysisID := "ready-status-test"

	// Create deployment with subdomain label and ready replicas.
	_, err := clientset.AppsV1().Deployments("vice-apps").Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: "dep1",
			Labels: map[string]string{
				"analysis-id": analysisID,
				"subdomain":   "c9999xyz",
				"app-name":    "JupyterLab",
			},
		},
		Spec: appsv1.DeploymentSpec{
			Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "test"}},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "test"}},
				Spec:       apiv1.PodSpec{Containers: []apiv1.Container{{Name: "c", Image: "img"}}},
			},
		},
		Status: appsv1.DeploymentStatus{ReadyReplicas: 1},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Create service.
	_, err = clientset.CoreV1().Services("vice-apps").Create(ctx, &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "analysis-svc", Labels: map[string]string{"analysis-id": analysisID}},
		Spec:       apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{Port: 80}}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Create a ready pod.
	_, err = clientset.CoreV1().Pods("vice-apps").Create(ctx, &apiv1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "pod1",
			Labels: map[string]string{"analysis-id": analysisID},
		},
		Status: apiv1.PodStatus{
			Phase: apiv1.PodRunning,
			Conditions: []apiv1.PodCondition{
				{Type: apiv1.PodReady, Status: apiv1.ConditionTrue},
			},
			ContainerStatuses: []apiv1.ContainerStatus{
				{Name: "analysis", Ready: true, State: apiv1.ContainerState{Running: &apiv1.ContainerStateRunning{}}},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Create ingress pointing at loading page.
	pathType := netv1.PathTypePrefix
	_, err = clientset.NetworkingV1().Ingresses("vice-apps").Create(ctx, &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "test-ing", Labels: map[string]string{"analysis-id": analysisID}},
		Spec: netv1.IngressSpec{
			DefaultBackend: &netv1.IngressBackend{
				Service: &netv1.IngressServiceBackend{
					Name: "vice-operator-loading",
					Port: netv1.ServiceBackendPort{Number: 80},
				},
			},
			Rules: []netv1.IngressRule{{
				Host: "c9999xyz.cyverse.run",
				IngressRuleValue: netv1.IngressRuleValue{
					HTTP: &netv1.HTTPIngressRuleValue{
						Paths: []netv1.HTTPIngressPath{{
							Path: "/", PathType: &pathType,
							Backend: netv1.IngressBackend{
								Service: &netv1.IngressServiceBackend{
									Name: "vice-operator-loading",
									Port: netv1.ServiceBackendPort{Number: 80},
								},
							},
						}},
					},
				},
			}},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/loading/status", nil)
	req.Host = "c9999xyz.cyverse.run"
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err = op.HandleLoadingStatus(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp LoadingStatusResponse
	err = json.Unmarshal(rec.Body.Bytes(), &resp)
	require.NoError(t, err)
	assert.True(t, resp.Ready)
	assert.Equal(t, StageReady, resp.Stage)

	// Verify the ingress was swapped to the analysis service.
	ings, err := clientset.NetworkingV1().Ingresses("vice-apps").List(ctx, analysisLabelSelector(analysisID))
	require.NoError(t, err)
	require.Len(t, ings.Items, 1)
	assert.Equal(t, "analysis-svc", ings.Items[0].Spec.DefaultBackend.Service.Name)
}
```

Add required imports to the test file: `"encoding/json"`, `"net/http"`, `"net/http/httptest"`, `"github.com/labstack/echo/v4"`, `netv1 "k8s.io/api/networking/v1"`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go test ./operator/ -run "TestHandleLoadingPage|TestHandleLoadingStatus" -v`
Expected: compilation error — `HandleLoadingPage` and `HandleLoadingStatus` undefined.

- [ ] **Step 3: Implement the handlers**

Add to `operator/loading.go`:

```go
// HandleLoadingPage serves the loading page HTML for the analysis identified
// by the request's Host header subdomain.
func (o *Operator) HandleLoadingPage(c echo.Context) error {
	ctx := c.Request().Context()
	host := c.Request().Host

	analysisID, appName, err := o.resolveSubdomain(ctx, host)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Analysis not found.")
	}

	data := loadingPageData{
		AppName:    appName,
		AnalysisID: analysisID,
		TimeoutMs:  o.loadingTimeoutMs,
	}

	c.Response().Header().Set("Content-Type", "text/html; charset=utf-8")
	c.Response().WriteHeader(http.StatusOK)
	return loadingTemplate.Execute(c.Response().Writer, data)
}

// HandleLoadingStatus returns the current loading status for the analysis
// identified by the request's Host header subdomain. If the analysis is ready,
// performs the route swap before responding.
func (o *Operator) HandleLoadingStatus(c echo.Context) error {
	ctx := c.Request().Context()
	host := c.Request().Host

	analysisID, _, err := o.resolveSubdomain(ctx, host)
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "Analysis not found.")
	}

	opts := analysisLabelSelector(analysisID)

	// Check deployment readiness.
	deps, err := o.clientset.AppsV1().Deployments(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	depReady := false
	for _, d := range deps.Items {
		if d.Status.ReadyReplicas > 0 {
			depReady = true
			break
		}
	}

	// Check service existence.
	svcs, err := o.clientset.CoreV1().Services(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}
	svcExists := len(svcs.Items) > 0

	// Get pods.
	podList, err := o.clientset.CoreV1().Pods(o.namespace).List(ctx, opts)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	stage, errMsg := computeStage(podList.Items, depReady, svcExists)
	ready := stage == StageReady

	// Perform route swap if ready.
	if ready {
		if swapErr := o.SwapRoute(ctx, analysisID); swapErr != nil {
			log.Errorf("route swap failed for analysis %s: %v", analysisID, swapErr)
			// Don't fail the status response; report ready but log the swap error.
			// The next poll will retry the swap.
		}
	}

	return c.JSON(http.StatusOK, LoadingStatusResponse{
		Ready: ready,
		Stage: stage,
		Error: errMsg,
		Pods:  buildLoadingPodInfo(podList.Items),
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go test ./operator/ -run "TestHandleLoadingPage|TestHandleLoadingStatus" -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
cd /home/johnw/work/src/github.com/cyverse-de/app-exposer
git add operator/loading.go operator/loading_test.go
git commit -m "feat: add loading page and status HTTP handlers"
```

---

### Task 6: Integrate backend rewrite into HandleLaunch and add HandleSwapRoute

Integrates the backend rewrite transform into `HandleLaunch` and adds the `HandleSwapRoute` API endpoint. The Operator struct fields were already added in Task 3.

**Files:**
- Modify: `operator/handlers.go`
- Modify: `operator/handlers_test.go`

- [ ] **Step 1: Add backend rewrite call to HandleLaunch**

In `operator/handlers.go`, after the GPU vendor transform (line 151) and before `applyBundle` (line 154), add:

```go
	// Rewrite routing backend to point at vice-operator's loading page service.
	TransformBackendToLoadingService(bundle.HTTPRoute, bundle.Ingress, o.loadingServiceName, o.loadingServicePort)
```

- [ ] **Step 4: Add HandleSwapRoute handler**

Add to `operator/handlers.go`:

```go
// HandleSwapRoute manually triggers the route swap for an analysis, pointing
// its HTTPRoute/Ingress at the analysis Service regardless of readiness.
//
//	@Summary		Manually swap route to analysis service
//	@Description	Swaps the HTTPRoute or Ingress backend from the loading page
//	@Description	service to the analysis Service. Idempotent.
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
```

- [ ] **Step 5: Run all existing tests to ensure nothing is broken**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go test ./operator/ -v`
Expected: all tests PASS

- [ ] **Step 6: Write test for HandleSwapRoute**

Add to `operator/handlers_test.go`:

```go
func TestHandleSwapRoute(t *testing.T) {
	op, clientset := newTestOperator(t, 10)
	ctx := context.Background()
	analysisID := "swap-route-test"
	labels := map[string]string{"analysis-id": analysisID}

	// Create analysis service and ingress pointing at loading page.
	_, err := clientset.CoreV1().Services("vice-apps").Create(ctx, &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "analysis-svc", Labels: labels},
		Spec:       apiv1.ServiceSpec{Ports: []apiv1.ServicePort{{Port: 80}}},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	pathType := netv1.PathTypePrefix
	_, err = clientset.NetworkingV1().Ingresses("vice-apps").Create(ctx, &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "test-ing", Labels: labels},
		Spec: netv1.IngressSpec{
			DefaultBackend: &netv1.IngressBackend{
				Service: &netv1.IngressServiceBackend{
					Name: "vice-operator-loading",
					Port: netv1.ServiceBackendPort{Number: 80},
				},
			},
			Rules: []netv1.IngressRule{
				{
					Host: "abc123.cyverse.run",
					IngressRuleValue: netv1.IngressRuleValue{
						HTTP: &netv1.HTTPIngressRuleValue{
							Paths: []netv1.HTTPIngressPath{
								{
									Path:     "/",
									PathType: &pathType,
									Backend: netv1.IngressBackend{
										Service: &netv1.IngressServiceBackend{
											Name: "vice-operator-loading",
											Port: netv1.ServiceBackendPort{Number: 80},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/analyses/"+analysisID+"/swap-route", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues(analysisID)

	err = op.HandleSwapRoute(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify the ingress was swapped.
	ings, err := clientset.NetworkingV1().Ingresses("vice-apps").List(ctx, analysisLabelSelector(analysisID))
	require.NoError(t, err)
	require.Len(t, ings.Items, 1)
	assert.Equal(t, "analysis-svc", ings.Items[0].Spec.DefaultBackend.Service.Name)
}
```

- [ ] **Step 7: Run tests**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go test ./operator/ -run TestHandleSwapRoute -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
cd /home/johnw/work/src/github.com/cyverse-de/app-exposer
git add operator/handlers.go operator/handlers_test.go
git commit -m "feat: integrate loading page transform into HandleLaunch; add HandleSwapRoute"
```

---

### Task 7: Wire up flags and second HTTP server

Adds the new command-line flags and starts the loading page server alongside the API server.

**Files:**
- Modify: `cmd/vice-operator/main.go`
- Modify: `cmd/vice-operator/app.go`

- [ ] **Step 1: Add flags to main.go**

In `cmd/vice-operator/main.go`, add flag variables (after line 45):

```go
		loadingPort        int
		loadingServiceName string
		loadingTimeout     time.Duration
```

Add flag definitions (after line 65):

```go
	flag.IntVar(&loadingPort, "loading-port", 8080, "Listen port for loading page server")
	flag.StringVar(&loadingServiceName, "loading-service-name", "vice-operator-loading", "K8s Service name for loading page")
	flag.DurationVar(&loadingTimeout, "loading-timeout", 10*time.Minute, "Client-side loading page timeout")
```

Update the `NewOperator` call in `main.go` (around line 147) to pass the new fields (the `NewOperator` signature was already updated in Task 3):

```go
	op := operator.NewOperator(clientset, gwClient, namespace, rt, ingressClass, gpuVendor, capacityCalc, imageCache,
		loadingServiceName, 80, loadingTimeout.Milliseconds())
```

Update the log line (around line 151) to include loading port:

```go
	log.Infof("vice-operator listening on :%d (loading page on :%d, namespace=%s, routing=%s, ingress-class=%s, gpu-vendor=%s, vice-base-url=%s, max-analyses=%d)",
		port, loadingPort, namespace, routingType, ingressClass, gpuVendorFlag, viceBaseURL, maxAnalyses)
```

Replace the single `app.Start` call with dual-server startup:

```go
	app := NewApp(op, basicAuth, basicAuthUsername, basicAuthPassword)
	loadingApp := NewLoadingApp(op)

	apiAddr := fmt.Sprintf(":%d", port)
	loadingAddr := fmt.Sprintf(":%d", loadingPort)

	// Start loading page server in a goroutine.
	go func() {
		log.Infof("loading page server starting on %s", loadingAddr)
		if err := loadingApp.Start(loadingAddr); err != nil {
			log.Errorf("loading page server error: %v", err)
		}
	}()

	// API server blocks on the main goroutine.
	if err := app.Start(apiAddr); err != nil {
		log.Error(err)
		os.Exit(1)
	}
```

- [ ] **Step 2: Add NewLoadingApp to app.go and swap-route API endpoint**

In `cmd/vice-operator/app.go`, add the loading page app constructor and register the swap-route endpoint:

```go
// LoadingApp wraps the Echo router for the loading page server.
type LoadingApp struct {
	router *echo.Echo
}

// NewLoadingApp creates a new loading page server with routes for serving
// loading pages on analysis subdomains.
func NewLoadingApp(op *operator.Operator) *LoadingApp {
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	e.GET("/", op.HandleLoadingPage)
	e.GET("/loading/status", op.HandleLoadingStatus)

	return &LoadingApp{router: e}
}

// Start begins listening on the given address.
func (a *LoadingApp) Start(addr string) error {
	return a.router.Start(addr)
}
```

Also add the swap-route endpoint to the API router in `NewApp` (after line 65):

```go
	analyses.POST("/swap-route", op.HandleSwapRoute)
```

- [ ] **Step 3: Verify it compiles**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go build ./cmd/vice-operator/`
Expected: build succeeds

- [ ] **Step 4: Run all tests**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/app-exposer && go test ./operator/ -v`
Expected: all tests PASS

- [ ] **Step 5: Commit**

```bash
cd /home/johnw/work/src/github.com/cyverse-de/app-exposer
git add cmd/vice-operator/main.go cmd/vice-operator/app.go
git commit -m "feat: add loading page server, flags, and swap-route API endpoint"
```

---

### Task 8: Update Sonora to link directly to analysis URL

Removes the loading page from Sonora and updates the interactive URL handler to navigate directly to the analysis URL.

**Files:**
- Modify: `/home/johnw/work/src/github.com/cyverse-de/sonora/src/components/analyses/utils.js`
- Delete: `/home/johnw/work/src/github.com/cyverse-de/sonora/src/pages/vice/[accessUrl].js`
- Delete: `/home/johnw/work/src/github.com/cyverse-de/sonora/src/components/vice/loading/` (entire directory)
- Delete: `/home/johnw/work/src/github.com/cyverse-de/sonora/src/serviceFacades/vice/loading.js`
- Delete: `/home/johnw/work/src/github.com/cyverse-de/sonora/public/static/locales/en/vice-loading.json`

**Important:** This task should be done on the Sonora repo's own branch and committed there. Coordinate timing with the vice-operator deployment — the Sonora changes can be deployed after vice-operator is updated since the old loading page will still work until the route-swapping behavior is active.

- [ ] **Step 1: Read the current openInteractiveUrl function**

Read: `/home/johnw/work/src/github.com/cyverse-de/sonora/src/components/analyses/utils.js`
Find the `openInteractiveUrl` function.

- [ ] **Step 2: Update openInteractiveUrl to navigate directly**

Change from:
```javascript
const openInteractiveUrl = (url) => {
    window.open(`/vice/${encodeURIComponent(url)}`, "_blank");
};
```
To:
```javascript
const openInteractiveUrl = (url) => {
    window.open(url, "_blank");
};
```

- [ ] **Step 3: Delete removed files**

```bash
cd /home/johnw/work/src/github.com/cyverse-de/sonora
rm -f src/pages/vice/\[accessUrl\].js
rm -rf src/components/vice/loading/
rm -f src/serviceFacades/vice/loading.js
rm -f public/static/locales/en/vice-loading.json
```

- [ ] **Step 4: Verify Sonora still builds**

Run: `cd /home/johnw/work/src/github.com/cyverse-de/sonora && npm run build`
Expected: build succeeds (or at least no errors related to the removed files — there may be other warnings unrelated to this change)

- [ ] **Step 5: Check for remaining imports of removed modules**

Search for any remaining imports of the removed modules:
```bash
cd /home/johnw/work/src/github.com/cyverse-de/sonora
grep -r "vice/loading" src/ --include="*.js" --include="*.jsx"
grep -r "vice-loading" src/ public/ --include="*.js" --include="*.json"
```

Fix any remaining references.

- [ ] **Step 6: Commit**

```bash
cd /home/johnw/work/src/github.com/cyverse-de/sonora
git add -A
git commit -m "feat: remove VICE loading page, link directly to analysis URL

The loading page is now served by vice-operator per-cluster instead of
Sonora. Users are linked directly to the analysis URL where vice-operator
serves the loading page until the app is ready."
```

---

### Task 9: Lint, format, and final verification

Run linting and formatting on all changed code.

**Files:** All modified files in app-exposer and sonora.

- [ ] **Step 1: Format and lint app-exposer**

```bash
cd /home/johnw/work/src/github.com/cyverse-de/app-exposer
goimports -w operator/ cmd/vice-operator/
gofmt -w operator/ cmd/vice-operator/
golangci-lint run ./operator/ ./cmd/vice-operator/
```

Fix any issues found.

- [ ] **Step 2: Run all app-exposer tests**

```bash
cd /home/johnw/work/src/github.com/cyverse-de/app-exposer
go test ./operator/ -v -count=1
```

Expected: all tests PASS

- [ ] **Step 3: Build the vice-operator binary**

```bash
cd /home/johnw/work/src/github.com/cyverse-de/app-exposer
go build ./cmd/vice-operator/
```

Expected: build succeeds

- [ ] **Step 4: Commit any lint/format fixes**

```bash
cd /home/johnw/work/src/github.com/cyverse-de/app-exposer
git add -A
git commit -m "chore: lint and format loading page code"
```
