// Package operatorclient provides types and an HTTP client for communicating
// with vice-operator instances running on remote (or local) clusters.
package operatorclient

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// AnalysisBundle contains all pre-built K8s resource objects for a VICE
// analysis. App-exposer assembles this using its existing builder functions
// and sends it to an operator, which applies the resources to its local cluster.
// HTTPRoute is the canonical networking resource (Gateway API).
type AnalysisBundle struct {
	// AnalysisID is the unique identifier for the analysis.
	AnalysisID string `json:"analysisID"`
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
// reported by an operator's cluster.
type CapacityResponse struct {
	MaxAnalyses       int   `json:"maxAnalyses"`
	RunningAnalyses   int   `json:"runningAnalyses"`
	AvailableSlots    int   `json:"availableSlots"`
	AllocatableCPU    int64 `json:"allocatableCPU"`    // millicores
	AllocatableMemory int64 `json:"allocatableMemory"` // bytes
	UsedCPU           int64 `json:"usedCPU"`           // millicores
	UsedMemory        int64 `json:"usedMemory"`        // bytes
}

// Validate checks that the bundle has the minimum required fields.
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
	return nil
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
