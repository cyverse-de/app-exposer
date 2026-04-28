# Gateway API Rebase & Operator Gateway Support Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Rebase multi-cluster on main (which uses Gateway API HTTPRoutes instead of Ingresses), update the AnalysisBundle to carry an HTTPRoute, and make vice-operator translate HTTPRoutes into Ingresses or Tailscale resources based on flags — or apply them directly for gateway-capable clusters.

**Architecture:** App-exposer on main already builds HTTPRoutes via the `incluster/httproutes` package. The AnalysisBundle will replace its `Ingress *netv1.Ingress` field with `HTTPRoute *gatewayv1.HTTPRoute`. The operator gains a third routing type, `gateway`, which applies the HTTPRoute directly. For `nginx` and `tailscale` routing types, the operator converts the HTTPRoute into an Ingress at apply time. This keeps the bundle format clean (one canonical networking type) and lets heterogeneous clusters each use their preferred networking.

**Tech Stack:** Go, K8s client-go, sigs.k8s.io/gateway-api v1.4.1, Echo v4, testify, fake clientsets

---

## File Map

### Modified files
| File | Responsibility |
|------|---------------|
| `operatorclient/types.go` | Add `HTTPRoute` field to AnalysisBundle (keep `Ingress` as `omitempty` for operator-side use) |
| `incluster/bundle.go` | Build HTTPRoute instead of Ingress in BuildAnalysisBundle |
| `operator/transforms.go` | Replace `TransformIngress` with `TransformRouting` that converts HTTPRoute → Ingress or applies directly |
| `operator/transforms_test.go` | Table-driven tests for all routing transforms |
| `operator/handlers.go` | Update Operator struct (add gatewayClient), HandleLaunch, HandleStatus, HandleURLReady, HandleListing |
| `operator/handlers_test.go` | Update tests for new bundle format and gateway routing |
| `operator/resources.go` | Add `upsertHTTPRoute`, update `applyBundle` and `deleteAnalysisResources` for HTTPRoutes |
| `cmd/vice-operator/main.go` | Add `gateway` routing type, add gateway client creation |
| `reporting/types.go` | Add `RouteInfo` type alongside `IngressInfo` (both needed: routes for gateway clusters, ingresses for legacy) |
| `reporting/convert.go` | Add `HTTPRouteInfoFrom` converter; fix `IngressInfoFrom` nil DefaultBackend panic |

### Files that change during rebase (conflict resolution)
| File | Nature of conflict |
|------|-------------------|
| `incluster/incluster.go` | Main removed ingress, multi-cluster added bundle builder; keep main's gateway changes + multi-cluster's operator/bundle additions |
| `incluster/ingresses.go` | Deleted on main; drop it (bundle.go will use httproutes instead) |
| `incluster/reporting.go` | Main has `RouteInfo` and uses Gateway API; accept main's version |
| `go.mod` / `go.sum` | Gateway API dependency from main vs multi-cluster additions |
| `httphandlers/*.go` | Main removed ingress handlers, multi-cluster modified them for operator routing; accept main's version, re-apply multi-cluster additions (dry-run, operator routing) on top |
| `cmd/app-exposer/main.go` / `app.go` | Accept main's version (gateway client init), re-apply operator config and dry-run route |

### Important notes
- **Dependency ordering**: Tasks 2-8 all depend on Task 1 (rebase) being complete. The `incluster/httproutes` and `incluster/jobinfo` packages only exist on main and will only be available after rebase.
- **AnalysisBundle struct**: The bundle carries both `HTTPRoute` (the canonical wire format from app-exposer) and `Ingress` (set by operator transform at apply time, `omitempty` so app-exposer never sends it). This is decided upfront — no mid-plan redesign.
- **Swagger docs**: Must be regenerated after all code changes (Task 8).
- **`cmd/vice-bundle` output format**: Changes from `ingress` to `httpRoute` in the JSON output. No code change needed, but users of the tool should be aware.

---

## Chunk 1: Rebase and Resolve Conflicts

### Task 1: Rebase multi-cluster on main

**Files:** All files in the branch

- [ ] **Step 1: Create a safety branch before rebasing**

```bash
git branch multi-cluster-pre-rebase
```

- [ ] **Step 2: Fetch latest main and start rebase**

```bash
git fetch origin main
git rebase origin/main
```

- [ ] **Step 3: Resolve conflicts**

Conflict resolution strategy for each area:

**`incluster/incluster.go`**: Accept main's version (Gateway API, `gatewayClient`, `getRoutesClient`, HTTPRoute creation/deletion). The multi-cluster branch's additions (`BuildAnalysisBundle`, operator routing) live in separate files (`incluster/bundle.go`, `operator/`), so they won't conflict with incluster.go's internals. The `Init` struct and `New()` constructor will use main's signature (which includes `gatewayClient *gatewayclient.GatewayV1Client` and `GatewayProvider string` instead of `IngressClass`).

**`incluster/ingresses.go`**: This file was deleted on main. Multi-cluster's `bundle.go` calls `i.getIngress()` from this file. During conflict resolution, drop `ingresses.go` (accept main's deletion). We'll fix `bundle.go` in Task 2 to build an HTTPRoute instead.

**`incluster/reporting.go`**: Accept main's version which uses `RouteInfo` and `gatewayv1.HTTPRouteRule`. The multi-cluster branch's `reporting/` package (separate from `incluster/reporting.go`) will need its own reconciliation in Task 4.

**`httphandlers/*.go`**: Accept main's changes (HTTPRoute-based). The multi-cluster operator routing additions are in the `operator/` package, not in httphandlers.

**`go.mod` / `go.sum`**: Accept main's version, then run `go mod tidy` to pick up any missing deps from multi-cluster additions.

**`cmd/app-exposer/main.go` and `cmd/app-exposer/app.go`**: Accept main's version (gateway client init). Multi-cluster additions (operator config, dry-run route) should be re-applied on top.

- [ ] **Step 4: After resolving each conflicting file, continue rebase**

```bash
git add <resolved-files>
git rebase --continue
```

Repeat for each commit that conflicts.

- [ ] **Step 5: Verify the rebase compiles**

```bash
go build ./...
```

This will likely fail because `bundle.go` still references `i.getIngress()` and `i.IngressClass` which no longer exist. That's expected — Task 2 fixes it.

- [ ] **Step 6: Run tests to see what passes**

```bash
go test ./... 2>&1 | head -50
```

Note which packages fail. Expected failures: `incluster` (bundle.go), `operator` (ingress transforms), `operatorclient` (bundle type).

---

## Chunk 2: Update AnalysisBundle and Bundle Builder

### Task 2: Replace Ingress with HTTPRoute in AnalysisBundle

**Files:**
- Modify: `operatorclient/types.go`
- Modify: `incluster/bundle.go`

- [ ] **Step 1: Update AnalysisBundle type**

In `operatorclient/types.go`, add `HTTPRoute` and keep `Ingress` as `omitempty` (used internally by operator transform, never sent by app-exposer):

```go
import (
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type AnalysisBundle struct {
	AnalysisID             string                         `json:"analysisID"`
	Deployment             *appsv1.Deployment             `json:"deployment"`
	Service                *apiv1.Service                 `json:"service"`
	HTTPRoute              *gatewayv1.HTTPRoute           `json:"httpRoute"`
	Ingress                *netv1.Ingress                 `json:"ingress,omitempty"` // Set by operator transform, not sent by app-exposer
	ConfigMaps             []*apiv1.ConfigMap             `json:"configMaps"`
	PersistentVolumes      []*apiv1.PersistentVolume      `json:"persistentVolumes"`
	PersistentVolumeClaims []*apiv1.PersistentVolumeClaim `json:"persistentVolumeClaims"`
	PodDisruptionBudget    *policyv1.PodDisruptionBudget  `json:"podDisruptionBudget"`
}
```

Add `gatewayv1` import, keep `netv1` import.

- [ ] **Step 2: Update bundle.go to build HTTPRoute instead of Ingress**

Replace the ingress-building section in `incluster/bundle.go`:

```go
package incluster

import (
	"context"

	"github.com/cyverse-de/app-exposer/incluster/httproutes"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/model/v10"
)

func (i *Incluster) BuildAnalysisBundle(ctx context.Context, job *model.Job, analysisID string) (*operatorclient.AnalysisBundle, error) {
	bundle := &operatorclient.AnalysisBundle{
		AnalysisID: analysisID,
	}

	// Build the excludes ConfigMap (always present).
	excludesCM, err := i.excludesConfigMap(ctx, job)
	if err != nil {
		return nil, err
	}
	bundle.ConfigMaps = append(bundle.ConfigMaps, excludesCM)

	// Build the input path list ConfigMap (present when there are inputs without tickets).
	inputCM, err := i.inputPathListConfigMap(ctx, job)
	if err != nil {
		return nil, err
	}
	if inputCM != nil {
		bundle.ConfigMaps = append(bundle.ConfigMaps, inputCM)
	}

	// Build the Deployment.
	deployment, err := i.GetDeployment(ctx, job)
	if err != nil {
		return nil, err
	}
	bundle.Deployment = deployment

	// Build the Service.
	svc, err := i.getService(ctx, job)
	if err != nil {
		return nil, err
	}
	bundle.Service = svc

	// Build the HTTPRoute using the configured gateway provider.
	routeBuilder := httproutes.NewHTTPRouteBuilder(
		i.GatewayProvider,
		i.VICEBackendNamespace,
		i.ViceNamespace,
		i.ViceDomain,
		i.jobInfo,
	)
	route, err := routeBuilder.BuildRoute(ctx, job, svc)
	if err != nil {
		return nil, err
	}
	bundle.HTTPRoute = route

	// Build PersistentVolumes (may be nil/empty when CSI is disabled).
	pvs, err := i.getPersistentVolumes(ctx, job)
	if err != nil {
		return nil, err
	}
	bundle.PersistentVolumes = pvs

	// Build PersistentVolumeClaims.
	pvcs, err := i.getVolumeClaims(ctx, job)
	if err != nil {
		return nil, err
	}
	bundle.PersistentVolumeClaims = pvcs

	// Build the PodDisruptionBudget.
	pdb, err := i.createPodDisruptionBudget(ctx, job)
	if err != nil {
		return nil, err
	}
	bundle.PodDisruptionBudget = pdb

	return bundle, nil
}
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./cmd/app-exposer/... ./incluster/... ./operatorclient/...
```

Expected: may still fail in `operator/` package (next task).

- [ ] **Step 4: Commit**

```bash
git add operatorclient/types.go incluster/bundle.go
git commit -m "Replace Ingress with HTTPRoute in AnalysisBundle

The bundle now carries a gatewayv1.HTTPRoute built by the same
httproutes package that main uses for direct launches. The operator
will translate this to Ingress or Tailscale as needed."
```

---

## Chunk 3: Update Operator Transforms and Routing

### Task 3: Rewrite operator transforms for HTTPRoute → Ingress conversion

**Files:**
- Modify: `operator/transforms.go`
- Modify: `operator/transforms_test.go`

- [ ] **Step 1: Write failing tests for the new transform functions**

Replace `operator/transforms_test.go` with table-driven tests covering:
- `nil` HTTPRoute → nil Ingress
- `RoutingGateway` → returns the HTTPRoute unchanged (no Ingress produced)
- `RoutingNginx` → converts HTTPRoute to Ingress with nginx class
- `RoutingTailscale` → converts HTTPRoute to Ingress with tailscale class, no nginx annotations

```go
package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func testHTTPRoute() *gatewayv1.HTTPRoute {
	port := gatewayv1.PortNumber(60002)
	return &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-route",
			Namespace: "vice-apps",
			Labels:    map[string]string{"analysis-id": "test-1", "app-type": "interactive"},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			Hostnames: []gatewayv1.Hostname{"abc123.cyverse.run"},
			Rules: []gatewayv1.HTTPRouteRule{
				{
					BackendRefs: []gatewayv1.HTTPBackendRef{
						{
							BackendRef: gatewayv1.BackendRef{
								BackendObjectReference: gatewayv1.BackendObjectReference{
									Name: "test-svc",
									Port: &port,
								},
							},
						},
					},
				},
			},
		},
	}
}

func TestTransformRouting(t *testing.T) {
	tests := []struct {
		name         string
		route        *gatewayv1.HTTPRoute
		routingType  RoutingType
		ingressClass string
		wantRoute    bool // true if HTTPRoute should be returned
		wantIngress  bool // true if Ingress should be returned
		wantClass    string
	}{
		{
			name:         "nil route returns nil for both",
			route:        nil,
			routingType:  RoutingNginx,
			ingressClass: "nginx",
			wantRoute:    false,
			wantIngress:  false,
		},
		{
			name:         "gateway routing returns route, no ingress",
			route:        testHTTPRoute(),
			routingType:  RoutingGateway,
			ingressClass: "",
			wantRoute:    true,
			wantIngress:  false,
		},
		{
			name:         "nginx routing returns ingress, no route",
			route:        testHTTPRoute(),
			routingType:  RoutingNginx,
			ingressClass: "nginx",
			wantRoute:    false,
			wantIngress:  true,
			wantClass:    "nginx",
		},
		{
			name:         "tailscale routing returns ingress, no route",
			route:        testHTTPRoute(),
			routingType:  RoutingTailscale,
			ingressClass: "tailscale",
			wantRoute:    false,
			wantIngress:  true,
			wantClass:    "tailscale",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route, ingress := TransformRouting(tt.route, tt.routingType, tt.ingressClass)

			if !tt.wantRoute {
				assert.Nil(t, route)
			} else {
				require.NotNil(t, route)
			}

			if !tt.wantIngress {
				assert.Nil(t, ingress)
			} else {
				require.NotNil(t, ingress)
				assert.Equal(t, tt.wantClass, *ingress.Spec.IngressClassName)
				// Verify the ingress was built from the route's data.
				assert.Equal(t, tt.route.Name, ingress.Name)
				assert.Equal(t, tt.route.Namespace, ingress.Namespace)
				assert.Equal(t, tt.route.Labels, ingress.Labels)
			}
		})
	}
}

func TestHTTPRouteToIngress(t *testing.T) {
	route := testHTTPRoute()
	ingressClass := "nginx"
	ingress := httpRouteToIngress(route, ingressClass)

	require.NotNil(t, ingress)
	assert.Equal(t, route.Name, ingress.Name)
	assert.Equal(t, route.Namespace, ingress.Namespace)
	assert.Equal(t, ingressClass, *ingress.Spec.IngressClassName)

	// Should have one rule per hostname with path routing to the backend.
	require.Len(t, ingress.Spec.Rules, 1)
	assert.Equal(t, "abc123.cyverse.run", ingress.Spec.Rules[0].Host)
	require.NotNil(t, ingress.Spec.Rules[0].HTTP)
	require.Len(t, ingress.Spec.Rules[0].HTTP.Paths, 1)
	assert.Equal(t, "test-svc", ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Name)
	assert.Equal(t, int32(60002), ingress.Spec.Rules[0].HTTP.Paths[0].Backend.Service.Port.Number)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./operator/... -run TestTransformRouting -v
go test ./operator/... -run TestHTTPRouteToIngress -v
```

Expected: FAIL — `TransformRouting` and `httpRouteToIngress` don't exist yet.

- [ ] **Step 3: Implement the new transforms**

Replace `operator/transforms.go`:

```go
package operator

import (
	netv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// RoutingType describes the networking strategy the operator's cluster uses.
type RoutingType string

const (
	// RoutingGateway applies HTTPRoutes directly via the Gateway API.
	RoutingGateway RoutingType = "gateway"

	// RoutingNginx converts HTTPRoutes to nginx Ingresses.
	RoutingNginx RoutingType = "nginx"

	// RoutingTailscale converts HTTPRoutes to Tailscale Ingresses.
	RoutingTailscale RoutingType = "tailscale"
)

// TransformRouting decides how to expose the analysis based on the operator's
// routing type. It returns at most one non-nil value:
//   - RoutingGateway: returns (route, nil) — apply the HTTPRoute directly
//   - RoutingNginx/RoutingTailscale: returns (nil, ingress) — apply a converted Ingress
func TransformRouting(route *gatewayv1.HTTPRoute, target RoutingType, ingressClass string) (*gatewayv1.HTTPRoute, *netv1.Ingress) {
	if route == nil {
		return nil, nil
	}

	switch target {
	case RoutingGateway:
		return route, nil
	default:
		return nil, httpRouteToIngress(route, ingressClass)
	}
}

// httpRouteToIngress converts a Gateway API HTTPRoute into a networking/v1
// Ingress. This is a pure data transformation with no K8s API calls.
func httpRouteToIngress(route *gatewayv1.HTTPRoute, ingressClass string) *netv1.Ingress {
	pathType := netv1.PathTypeImplementationSpecific

	var rules []netv1.IngressRule
	for _, hostname := range route.Spec.Hostnames {
		var paths []netv1.HTTPIngressPath

		for _, rule := range route.Spec.Rules {
			for _, backendRef := range rule.BackendRefs {
				path := netv1.HTTPIngressPath{
					PathType: &pathType,
					Backend: netv1.IngressBackend{
						Service: &netv1.IngressServiceBackend{
							Name: string(backendRef.Name),
						},
					},
				}
				if backendRef.Port != nil {
					path.Backend.Service.Port = netv1.ServiceBackendPort{
						Number: int32(*backendRef.Port),
					}
				}
				paths = append(paths, path)
			}
		}

		rules = append(rules, netv1.IngressRule{
			Host: string(hostname),
			IngressRuleValue: netv1.IngressRuleValue{
				HTTP: &netv1.HTTPIngressRuleValue{
					Paths: paths,
				},
			},
		})
	}

	return &netv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      route.Name,
			Namespace: route.Namespace,
			Labels:    route.Labels,
		},
		Spec: netv1.IngressSpec{
			IngressClassName: &ingressClass,
			Rules:            rules,
		},
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./operator/... -run TestTransformRouting -v
go test ./operator/... -run TestHTTPRouteToIngress -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add operator/transforms.go operator/transforms_test.go
git commit -m "Replace ingress transforms with Gateway API routing transforms

TransformRouting now accepts an HTTPRoute and produces either:
- the HTTPRoute unchanged (gateway mode), or
- a converted Ingress (nginx/tailscale mode).
httpRouteToIngress handles the data conversion."
```

---

### Task 4: Update operator resources.go for HTTPRoute support

**Files:**
- Modify: `operator/resources.go`
- Modify: `operator/handlers.go`

- [ ] **Step 1: Add Gateway API client to Operator struct**

In `operator/handlers.go`, update the `Operator` struct and constructor:

```go
import (
	// ... existing imports ...
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/typed/apis/v1"
)

type Operator struct {
	clientset     kubernetes.Interface
	gatewayClient gatewayclient.GatewayV1Client // nil when routing != gateway
	namespace     string
	routingType   RoutingType
	ingressClass  string
	capacityCalc  *CapacityCalculator
}

func NewOperator(
	clientset kubernetes.Interface,
	gatewayClient *gatewayclient.GatewayV1Client,
	namespace string,
	routingType RoutingType,
	ingressClass string,
	capacityCalc *CapacityCalculator,
) *Operator {
	op := &Operator{
		clientset:    clientset,
		namespace:    namespace,
		routingType:  routingType,
		ingressClass: ingressClass,
		capacityCalc: capacityCalc,
	}
	if gatewayClient != nil {
		op.gatewayClient = *gatewayClient
	}
	return op
}
```

- [ ] **Step 2: Add upsertHTTPRoute and deleteHTTPRoutes to resources.go**

Add to `operator/resources.go`:

```go
import (
	// ... existing imports ...
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

func (o *Operator) upsertHTTPRoute(ctx context.Context, route *gatewayv1.HTTPRoute) error {
	client := o.gatewayClient.HTTPRoutes(o.namespace)
	_, err := client.Get(ctx, route.Name, metav1.GetOptions{})
	if err != nil {
		log.Debugf("creating HTTPRoute %s", route.Name)
		_, err = client.Create(ctx, route, metav1.CreateOptions{})
	} else {
		log.Debugf("updating HTTPRoute %s", route.Name)
		_, err = client.Update(ctx, route, metav1.UpdateOptions{})
	}
	return err
}
```

- [ ] **Step 3: Update applyBundle to handle both routing types**

In `operator/resources.go`, replace the Ingress section of `applyBundle`:

```go
// Routing: apply either HTTPRoute or Ingress based on operator config.
// TransformRouting was already called before applyBundle, so the bundle
// will have at most one of these set.
if bundle.HTTPRoute != nil {
	if err := o.upsertHTTPRoute(ctx, bundle.HTTPRoute); err != nil {
		return fmt.Errorf("httproute %s: %w", bundle.HTTPRoute.Name, err)
	}
}
if bundle.Ingress != nil {
	if err := o.upsertIngress(ctx, bundle.Ingress); err != nil {
		return fmt.Errorf("ingress %s: %w", bundle.Ingress.Name, err)
	}
}
```

The AnalysisBundle already has both `HTTPRoute` and `Ingress` fields (defined in Task 2). App-exposer sets `HTTPRoute`; the operator's `HandleLaunch` calls `TransformRouting` which nils out one and populates the other. `applyBundle` then upserts whichever is non-nil.

- [ ] **Step 4: Update HandleLaunch in handlers.go**

Replace the ingress transform line:

```go
// Transform routing for this cluster's networking type.
route, ingress := TransformRouting(bundle.HTTPRoute, o.routingType, o.ingressClass)
bundle.HTTPRoute = route
bundle.Ingress = ingress
```

- [ ] **Step 5: Update deleteAnalysisResources for HTTPRoutes**

In `operator/resources.go`, add HTTPRoute deletion alongside Ingress deletion:

```go
// Delete HTTPRoutes (gateway mode). Guard against nil gatewayClient to
// prevent panics if routing mode was misconfigured.
if o.routingType == RoutingGateway && o.hasGatewayClient() {
	routeClient := o.gatewayClient.HTTPRoutes(o.namespace)
	routeList, err := routeClient.List(ctx, opts)
	if err != nil {
		return err
	}
	for _, route := range routeList.Items {
		if err := routeClient.Delete(ctx, route.Name, metav1.DeleteOptions{}); err != nil {
			log.Error(err)
		}
	}
}

// Delete Ingresses (nginx/tailscale mode).
if o.routingType != RoutingGateway {
	ingClient := o.clientset.NetworkingV1().Ingresses(o.namespace)
	// ... existing code ...
}
```

Also add a `hasGatewayClient` helper to `operator/handlers.go`:

```go
// hasGatewayClient returns true if the operator has a configured Gateway API client.
func (o *Operator) hasGatewayClient() bool {
	return o.gatewayClient.RESTClient() != nil
}
```

- [ ] **Step 6: Update HandleStatus and HandleURLReady**

These handlers currently check for Ingresses. Update them to check for HTTPRoutes when in gateway mode:

In `HandleStatus`, add a `Routes` field to `StatusResponse`:

```go
type StatusResponse struct {
	AnalysisID  string           `json:"analysisID"`
	Deployments []DeploymentInfo `json:"deployments"`
	Pods        []PodInfo        `json:"pods"`
	Services    []string         `json:"services"`
	Ingresses   []string         `json:"ingresses,omitempty"`
	Routes      []string         `json:"routes,omitempty"`
}
```

Check the appropriate resource type based on `o.routingType`.

In `HandleURLReady`, check for either an HTTPRoute or Ingress existing based on routing type.

In `HandleListing`, list HTTPRoutes for gateway mode, Ingresses for legacy modes.

- [ ] **Step 7: Update handler tests to match new constructor and bundle format**

The `NewOperator` signature changed (added `gatewayClient` parameter), so tests must be updated in the same task to keep the build green. Update `operator/handlers_test.go`:

Update `newTestOperator` to pass a nil gateway client:

```go
func newTestOperator(t *testing.T, maxAnalyses int) (*Operator, *fake.Clientset) {
	t.Helper()
	clientset := fake.NewSimpleClientset()
	calc := NewCapacityCalculator(clientset, "vice-apps", maxAnalyses, "")
	op := NewOperator(clientset, nil, "vice-apps", RoutingNginx, "nginx", calc)
	return op, clientset
}
```

Update test bundles to use `HTTPRoute` instead of `Ingress`:

```go
HTTPRoute: &gatewayv1.HTTPRoute{
	ObjectMeta: metav1.ObjectMeta{
		Name:      "test-route",
		Namespace: "vice-apps",
		Labels:    map[string]string{"analysis-id": "test-analysis-1"},
	},
	Spec: gatewayv1.HTTPRouteSpec{
		Hostnames: []gatewayv1.Hostname{"test.cyverse.run"},
		Rules: []gatewayv1.HTTPRouteRule{
			{
				BackendRefs: []gatewayv1.HTTPBackendRef{
					{BackendRef: gatewayv1.BackendRef{
						BackendObjectReference: gatewayv1.BackendObjectReference{
							Name: "test-svc",
							Port: func() *gatewayv1.PortNumber { p := gatewayv1.PortNumber(80); return &p }(),
						},
					}},
				},
			},
		},
	},
},
```

In the verification section, check that an Ingress was created (since test uses RoutingNginx, the HTTPRoute gets transformed):

```go
// In nginx mode, the HTTPRoute is converted to an Ingress.
_, err = clientset.NetworkingV1().Ingresses("vice-apps").Get(ctx, "test-route", metav1.GetOptions{})
assert.NoError(t, err, "ingress should exist (converted from HTTPRoute)")
```

Update `TestHandleURLReady` similarly.

- [ ] **Step 8: Verify compilation and tests**

```bash
go build ./operator/... ./operatorclient/... ./cmd/vice-operator/...
go test ./operator/... -v
```

- [ ] **Step 9: Commit**

```bash
git add operator/resources.go operator/handlers.go operator/handlers_test.go operatorclient/types.go
git commit -m "Add Gateway API support to operator resource management

Operator can now apply HTTPRoutes directly (gateway mode) or convert
them to Ingresses (nginx/tailscale mode). Delete, status, url-ready,
and listing handlers all support both resource types."
```

---

### Task 5: Update vice-operator main.go for gateway client

**Files:**
- Modify: `cmd/vice-operator/main.go`

- [ ] **Step 1: Add gateway routing type and client creation**

```go
import (
	// ... existing ...
	gatewayclient "sigs.k8s.io/gateway-api/pkg/client/clientset/versioned/typed/apis/v1"
)

// In main(), update the routing type parsing:
var rt operator.RoutingType
switch routingType {
case "tailscale":
	rt = operator.RoutingTailscale
case "gateway":
	rt = operator.RoutingGateway
default:
	rt = operator.RoutingNginx
}

// Create gateway client when in gateway mode.
var gwClient *gatewayclient.GatewayV1Client
if rt == operator.RoutingGateway {
	gwClient, err = gatewayclient.NewForConfig(config)
	if err != nil {
		log.Fatalf("error creating gateway API client: %v", err)
	}
}

capacityCalc := operator.NewCapacityCalculator(clientset, namespace, maxAnalyses, nodeLabelSelector)
op := operator.NewOperator(clientset, gwClient, namespace, rt, ingressClass, capacityCalc)
```

- [ ] **Step 2: Update the log line to show "gateway" as a valid option**

Update the log message to clarify the routing type:

```go
log.Infof("vice-operator listening on %s (namespace=%s, routing=%s, ingress-class=%s, max-analyses=%d)",
	listenAddr, namespace, routingType, ingressClass, maxAnalyses)
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./cmd/vice-operator/...
```

- [ ] **Step 4: Commit**

```bash
git add cmd/vice-operator/main.go
git commit -m "Add gateway routing type to vice-operator CLI

--routing-type now accepts 'gateway' (default), 'nginx', or 'tailscale'.
Gateway mode creates a Gateway API client for HTTPRoute management."
```

---

## Chunk 4: Update Reporting and Tests

### Task 6: Update reporting package for dual resource support

**Files:**
- Modify: `reporting/types.go`
- Modify: `reporting/convert.go`

- [ ] **Step 1: Add RouteInfo to reporting types**

Add to `reporting/types.go`:

```go
import (
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// RouteInfo contains information about an HTTPRoute used for VICE apps.
type RouteInfo struct {
	MetaInfo
	Rules []gatewayv1.HTTPRouteRule `json:"rules"`
}
```

Add `Routes` field to `ResourceInfo`:

```go
type ResourceInfo struct {
	Deployments []DeploymentInfo `json:"deployments"`
	Pods        []PodInfo        `json:"pods"`
	ConfigMaps  []ConfigMapInfo  `json:"configMaps"`
	Services    []ServiceInfo    `json:"services"`
	Ingresses   []IngressInfo    `json:"ingresses"`
	Routes      []RouteInfo      `json:"routes"`
}
```

Update `NewResourceInfo` and `SortByCreationTime` to include the new `Routes` field.

- [ ] **Step 2: Add HTTPRouteInfoFrom converter**

Add to `reporting/convert.go`:

```go
import (
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// HTTPRouteInfoFrom creates a RouteInfo from a Gateway API HTTPRoute.
func HTTPRouteInfoFrom(route *gatewayv1.HTTPRoute) *RouteInfo {
	labels := route.GetObjectMeta().GetLabels()
	return &RouteInfo{
		MetaInfo: MetaInfo{
			Name:              route.GetName(),
			Namespace:         route.GetNamespace(),
			AnalysisName:      labels["analysis-name"],
			AppName:           labels["app-name"],
			AppID:             labels["app-id"],
			ExternalID:        labels["external-id"],
			UserID:            labels["user-id"],
			Username:          labels["username"],
			CreationTimestamp: route.GetCreationTimestamp().String(),
		},
		Rules: route.Spec.Rules,
	}
}
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./reporting/... ./operator/...
```

- [ ] **Step 4: Commit**

```bash
git add reporting/types.go reporting/convert.go
git commit -m "Add RouteInfo to reporting package for Gateway API support

Both the operator listing and incluster reporting can now describe
HTTPRoutes alongside Ingresses."
```

---

### Task 7: Fix IngressInfoFrom nil DefaultBackend panic

**Files:**
- Modify: `reporting/convert.go`

After the gateway migration, Ingresses created via `httpRouteToIngress` have no `DefaultBackend`. The current `IngressInfoFrom` unconditionally dereferences `ingress.Spec.DefaultBackend.Service`, which would panic.

- [ ] **Step 1: Add nil guard to IngressInfoFrom**

```go
func IngressInfoFrom(ingress *netv1.Ingress) *IngressInfo {
	labels := ingress.GetObjectMeta().GetLabels()

	var defaultBackend string
	if ingress.Spec.DefaultBackend != nil && ingress.Spec.DefaultBackend.Service != nil {
		defaultBackend = fmt.Sprintf("%s:%d",
			ingress.Spec.DefaultBackend.Service.Name,
			ingress.Spec.DefaultBackend.Service.Port.Number,
		)
	}

	return &IngressInfo{
		MetaInfo: metaInfoFromLabels(
			ingress.GetName(),
			ingress.GetNamespace(),
			ingress.GetCreationTimestamp().String(),
			labels,
		),
		Rules:          ingress.Spec.Rules,
		DefaultBackend: defaultBackend,
	}
}
```

- [ ] **Step 2: Commit**

```bash
git add reporting/convert.go
git commit -m "Fix IngressInfoFrom panic on nil DefaultBackend

Ingresses created by the operator's HTTPRoute-to-Ingress conversion
have no DefaultBackend (Gateway API has no equivalent concept, and
main removed the VICE default backend in commit f667c83)."
```

---

### Task 8: Final verification and cleanup

**Files:** All

- [ ] **Step 1: Run full build**

```bash
go build ./...
```

- [ ] **Step 2: Run full test suite**

```bash
go test ./...
```

- [ ] **Step 3: Regenerate swagger docs**

```bash
just docs
just operator-docs
```

- [ ] **Step 4: Run linter**

```bash
golangci-lint run ./...
```

- [ ] **Step 5: Format code**

```bash
goimports -w .
gofmt -w .
```

- [ ] **Step 6: Fix any issues found, re-run tests**

- [ ] **Step 7: Commit any cleanup**

```bash
git add -A
git commit -m "Regenerate swagger docs and post-rebase cleanup"
```

---

## Summary of Routing Flow After Implementation

```
app-exposer (main)                    vice-operator
┌──────────────────┐                  ┌──────────────────────────────┐
│ BuildAnalysisBundle()                │ HandleLaunch()                │
│   ↓                                 │   ↓                          │
│ httproutes.BuildRoute()             │ TransformRouting(route, type) │
│   ↓                                 │   ├─ gateway → (route, nil)  │
│ bundle.HTTPRoute = route            │   ├─ nginx   → (nil, ingress)│
│   ↓                                 │   └─ tailscale→(nil, ingress)│
│ Send bundle to operator ──────────► │   ↓                          │
└──────────────────┘                  │ applyBundle()                 │
                                      │   ├─ upsertHTTPRoute (if set)│
                                      │   └─ upsertIngress (if set)  │
                                      └──────────────────────────────┘
```
