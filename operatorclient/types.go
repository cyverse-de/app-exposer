// Package operatorclient provides types and an HTTP client for communicating
// with vice-operator instances running on remote (or local) clusters.
package operatorclient

import (
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

// AnalysisBundle contains all pre-built K8s resource objects for a VICE
// analysis. App-exposer assembles this using its existing builder functions
// and sends it to an operator, which applies the resources to its local cluster.
// HTTPRoute is the canonical networking resource (Gateway API); Ingress is
// retained for backward compatibility with operators that don't yet support
// the Gateway API.
type AnalysisBundle struct {
	AnalysisID             string                         `json:"analysisID"`
	Deployment             *appsv1.Deployment             `json:"deployment"`
	Service                *apiv1.Service                 `json:"service"`
	HTTPRoute              *gatewayv1.HTTPRoute           `json:"httpRoute"`
	Ingress                *netv1.Ingress                 `json:"ingress,omitempty"`
	ConfigMaps             []*apiv1.ConfigMap             `json:"configMaps"`
	PersistentVolumes      []*apiv1.PersistentVolume      `json:"persistentVolumes"`
	PersistentVolumeClaims []*apiv1.PersistentVolumeClaim `json:"persistentVolumeClaims"`
	PodDisruptionBudget    *policyv1.PodDisruptionBudget  `json:"podDisruptionBudget"`
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

// OperatorConfig holds the configuration for a single vice-operator instance.
// Operators are listed in priority order; the scheduler tries them sequentially.
// Username and Password are optional; when set, the client sends basic auth
// with every request to the operator.
type OperatorConfig struct {
	Name     string `json:"name"     koanf:"name"`
	URL      string `json:"url"      koanf:"url"`
	Username string `json:"username" koanf:"username"`
	Password string `json:"password" koanf:"password"`
}
