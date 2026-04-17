// Package operatorclient provides types and an HTTP client for communicating
// with vice-operator instances running on remote (or local) clusters.
package operatorclient

import (
	"fmt"

	"github.com/cyverse-de/app-exposer/constants"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// Re-export domain ID types at the operatorclient boundary so callers
// don't have to import constants just to hold a method argument.
type (
	AnalysisID = constants.AnalysisID
	ExternalID = constants.ExternalID
)

// AnalysisBundle contains all pre-built K8s resource objects for a VICE
// analysis. App-exposer assembles this using its existing builder functions
// and sends it to an operator, which applies the resources to its local cluster.
// HTTPRoute is the canonical networking resource (Gateway API).
type AnalysisBundle struct {
	// AnalysisID is the unique identifier for the analysis.
	AnalysisID AnalysisID `json:"analysisID"`
	// Deployment is the Kubernetes Deployment object for the analysis.
	Deployment *appsv1.Deployment `json:"deployment"`
	// Service is the Kubernetes Service object for the analysis.
	Service *apiv1.Service `json:"service"`
	// HTTPRoute is the Gateway API HTTPRoute object for the analysis's routing.
	HTTPRoute *gatewayv1.HTTPRoute `json:"httpRoute"`
	// ConfigMaps is a list of Kubernetes ConfigMap objects for the analysis.
	ConfigMaps []*apiv1.ConfigMap `json:"configMaps"`
	// PersistentVolumes is a list of Kubernetes PersistentVolume objects for the analysis.
	PersistentVolumes []*apiv1.PersistentVolume `json:"persistentVolumes"`
	// PersistentVolumeClaims is a list of Kubernetes PersistentVolumeClaim objects for the analysis.
	PersistentVolumeClaims []*apiv1.PersistentVolumeClaim `json:"persistentVolumeClaims"`
	// PodDisruptionBudget is the Kubernetes PodDisruptionBudget object for the analysis.
	PodDisruptionBudget *policyv1.PodDisruptionBudget `json:"podDisruptionBudget"`
}

// CapacityResponse describes the current resource capacity and usage
// reported by an operator's cluster. AvailableSlots is three-valued:
// positive means "has capacity", 0 means "at capacity", and -1 means
// "unlimited" (autoscaling cluster with no configured cap). Prefer
// HasCapacity() over direct comparison so the convention lives in one
// place.
type CapacityResponse struct {
	MaxAnalyses       int   `json:"maxAnalyses"`
	RunningAnalyses   int   `json:"runningAnalyses"`
	AvailableSlots    int   `json:"availableSlots"`
	AllocatableCPU    int64 `json:"allocatableCPU"`    // millicores
	AllocatableMemory int64 `json:"allocatableMemory"` // bytes
	UsedCPU           int64 `json:"usedCPU"`           // millicores
	UsedMemory        int64 `json:"usedMemory"`        // bytes
}

// HasCapacity reports whether this operator can accept a new analysis.
// Encapsulates the three-valued AvailableSlots convention: -1 means
// unlimited, 0 means exhausted, anything greater is a finite but
// non-zero slot count.
func (c *CapacityResponse) HasCapacity() bool {
	return c.AvailableSlots != 0
}

// Validate checks the bundle's top-level required fields and then
// walks each labeled child resource, asserting its analysis-id label
// matches the bundle's AnalysisID. deleteAnalysisResources relies on
// that label to find every child at cleanup time, so a mismatch would
// produce a bundle that launches but can't be cleaned up cleanly.
func (b *AnalysisBundle) Validate() error {
	if b.AnalysisID == "" {
		return fmt.Errorf("analysisID is required")
	}
	if b.Deployment == nil {
		return fmt.Errorf("deployment is required")
	}
	if b.Service == nil {
		return fmt.Errorf("service is required")
	}

	if err := checkAnalysisIDLabel("deployment", b.Deployment.Labels, b.AnalysisID); err != nil {
		return err
	}
	if err := checkAnalysisIDLabel("service", b.Service.Labels, b.AnalysisID); err != nil {
		return err
	}
	if b.HTTPRoute != nil {
		if err := checkAnalysisIDLabel("httpRoute", b.HTTPRoute.Labels, b.AnalysisID); err != nil {
			return err
		}
	}
	for i, cm := range b.ConfigMaps {
		if cm == nil {
			continue
		}
		if err := checkAnalysisIDLabel(fmt.Sprintf("configMaps[%d]", i), cm.Labels, b.AnalysisID); err != nil {
			return err
		}
	}
	for i, pv := range b.PersistentVolumes {
		if pv == nil {
			continue
		}
		if err := checkAnalysisIDLabel(fmt.Sprintf("persistentVolumes[%d]", i), pv.Labels, b.AnalysisID); err != nil {
			return err
		}
	}
	for i, pvc := range b.PersistentVolumeClaims {
		if pvc == nil {
			continue
		}
		if err := checkAnalysisIDLabel(fmt.Sprintf("persistentVolumeClaims[%d]", i), pvc.Labels, b.AnalysisID); err != nil {
			return err
		}
	}
	if b.PodDisruptionBudget != nil {
		if err := checkAnalysisIDLabel("podDisruptionBudget", b.PodDisruptionBudget.Labels, b.AnalysisID); err != nil {
			return err
		}
	}
	return nil
}

// checkAnalysisIDLabel returns an error when labels[analysis-id] does
// not match the expected wantID. A missing label is reported as an
// empty-string mismatch so the error message points at the problem.
func checkAnalysisIDLabel(kind string, labels map[string]string, wantID AnalysisID) error {
	if got := labels[constants.AnalysisIDLabel]; got != string(wantID) {
		return fmt.Errorf("%s has analysis-id label %q, want %q", kind, got, wantID)
	}
	return nil
}

// StatusResponse describes the state of an analysis's K8s resources
// as reported by the operator's /analyses/{id}/status endpoint. Lives
// in operatorclient so the client can return it directly and the
// operator can re-import it — same pattern as UpdatePermissionsRequest
// and URLReadyResponse.
type StatusResponse struct {
	AnalysisID  AnalysisID         `json:"analysisID"`
	Deployments []StatusDeployment `json:"deployments"`
	Pods        []StatusPod        `json:"pods"`
	Services    []string           `json:"services"`
	Routes      []string           `json:"routes,omitempty"`
}

// StatusDeployment holds the minimal deployment status reported by the
// /analyses/{id}/status endpoint. Named distinctly from
// reporting.DeploymentInfo (which carries richer audit-level fields)
// because both types are visible in files that import the reporting
// package, and identical names would force local aliases at every call
// site.
type StatusDeployment struct {
	Name            string `json:"name"`
	ReadyReplicas   int32  `json:"readyReplicas"`
	DesiredReplicas int32  `json:"desiredReplicas"`
}

// StatusPod holds the minimal pod status reported by the
// /analyses/{id}/status and /analyses/{id}/pods endpoints. Named
// distinctly from reporting.PodInfo for the reason described on
// StatusDeployment.
type StatusPod struct {
	Name  string `json:"name"`
	Phase string `json:"phase"`
	Ready bool   `json:"ready"`
}

// ActiveSession describes a single active user session in a VICE analysis.
type ActiveSession struct {
	SessionID string `json:"session_id"`
	Username  string `json:"username"`
}

// ActiveSessionsResponse is returned by the active-sessions endpoint.
type ActiveSessionsResponse struct {
	Sessions []ActiveSession `json:"sessions"`
}

// LogoutUserRequest is the request body for the logout-user endpoint.
type LogoutUserRequest struct {
	Username string `json:"username"`
}

// UpdatePermissionsRequest is the request body for the permissions-update
// endpoint. Canonical definition lives in operatorclient so both app-exposer's
// user-facing handler and the operator's HTTP handler share one type.
type UpdatePermissionsRequest struct {
	AllowedUsers []string `json:"allowedUsers"`
}

// LogoutUserResponse is returned by the logout-user endpoint.
type LogoutUserResponse struct {
	SessionsInvalidated int `json:"sessions_invalidated"`
}

// URLReadyResponse indicates whether a VICE analysis is ready for user access
// and provides the frontend URL for it.
type URLReadyResponse struct {
	Ready     bool   `json:"ready"`
	AccessURL string `json:"access_url,omitempty"`
}

// OperatorConfig holds the public configuration of a single vice-operator
// instance. It is the canonical shape for operator metadata that crosses
// process boundaries: koanf tags let it be loaded from a config file;
// json tags let it be marshalled over the HTTP API; db tags let sqlx
// scan directly from the operators table's SELECT columns. This struct
// previously had three near-duplicate siblings (db.OperatorSummary,
// cmd/vice-operator-tool.OperatorSummary, cmd/vice-operator-tool.
// AddOperatorRequest); they were consolidated here to eliminate drift.
//
// The internal full-row struct db.Operator continues to exist for DB
// access that needs the write-only fields (timestamps, reconciliation
// state, etc.) that don't belong on the wire.
//
// Operators are listed in priority order; the scheduler tries them
// sequentially. Priority is preserved across every serialization form
// so downstream code does not have to reach back to the DB ordering to
// know the intended precedence.
type OperatorConfig struct {
	Name          string `json:"name"            koanf:"name"            db:"name"`
	URL           string `json:"url"             koanf:"url"             db:"url"`
	TLSSkipVerify bool   `json:"tls_skip_verify" koanf:"tls_skip_verify" db:"tls_skip_verify"`
	Priority      int    `json:"priority"        koanf:"priority"        db:"priority"`
}
