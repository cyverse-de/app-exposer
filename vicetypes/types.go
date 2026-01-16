// Package vicetypes provides shared types for coordinator-deployer communication
// in multi-cluster VICE deployments.
package vicetypes

import (
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
)

// DeploymentMetadata contains identifying information for a VICE deployment.
// These fields are used for tracking, labeling, and coordinating deployments
// across clusters.
type DeploymentMetadata struct {
	// ExternalID is the unique identifier for the analysis (job_uuid/invocation_id)
	ExternalID string `json:"external_id"`

	// AnalysisID is the internal analysis identifier
	AnalysisID string `json:"analysis_id"`

	// UserID is the unique identifier for the user who submitted the analysis
	UserID string `json:"user_id"`

	// Username is the submitter's username
	Username string `json:"username"`

	// AppID is the application identifier
	AppID string `json:"app_id"`

	// AppName is the human-readable application name
	AppName string `json:"app_name"`

	// AnalysisName is the human-readable analysis name
	AnalysisName string `json:"analysis_name"`

	// Namespace is the Kubernetes namespace for the deployment
	Namespace string `json:"namespace"`

	// Subdomain is the ingress subdomain for routing traffic
	Subdomain string `json:"subdomain"`

	// LoginIP is the user's login IP address (used for ingress labels)
	LoginIP string `json:"login_ip"`

	// Labels contains all Kubernetes labels to apply to resources
	Labels map[string]string `json:"labels"`
}

// VICEDeploymentSpec is the complete specification sent from the coordinator
// to a deployer. It contains all Kubernetes resources needed to run a VICE
// analysis.
type VICEDeploymentSpec struct {
	// Metadata contains identifying information for the deployment
	Metadata DeploymentMetadata `json:"metadata"`

	// Deployment is the full Kubernetes Deployment spec
	Deployment *appsv1.Deployment `json:"deployment"`

	// Service is the full Kubernetes Service spec
	Service *apiv1.Service `json:"service"`

	// Ingress is the full Kubernetes Ingress spec
	Ingress *netv1.Ingress `json:"ingress"`

	// ConfigMaps contains ConfigMap specs (excludes file, input path list)
	ConfigMaps []*apiv1.ConfigMap `json:"config_maps"`

	// PersistentVolumes contains PV specs for CSI driver volumes (optional)
	PersistentVolumes []*apiv1.PersistentVolume `json:"persistent_volumes,omitempty"`

	// PersistentVolumeClaims contains PVC specs for storage
	PersistentVolumeClaims []*apiv1.PersistentVolumeClaim `json:"persistent_volume_claims"`

	// PodDisruptionBudget is the PDB spec to prevent disruption
	PodDisruptionBudget *policyv1.PodDisruptionBudget `json:"pod_disruption_budget"`
}

// DeploymentResponse is returned by the deployer after creating or deleting
// a deployment.
type DeploymentResponse struct {
	// Status is the result status ("created", "deleted", "error")
	Status string `json:"status"`

	// ExternalID is the deployment's external identifier
	ExternalID string `json:"external_id"`

	// ResourcesCreated lists resources that were created (for POST)
	ResourcesCreated []string `json:"resources_created,omitempty"`

	// ResourcesDeleted lists resources that were deleted (for DELETE)
	ResourcesDeleted []string `json:"resources_deleted,omitempty"`

	// Error contains error details if status is "error"
	Error string `json:"error,omitempty"`
}

// DeploymentStatusInfo contains status information about a Kubernetes Deployment.
type DeploymentStatusInfo struct {
	// ReadyReplicas is the number of ready replicas
	ReadyReplicas int32 `json:"ready_replicas"`

	// AvailableReplicas is the number of available replicas
	AvailableReplicas int32 `json:"available_replicas"`

	// Replicas is the total number of replicas
	Replicas int32 `json:"replicas"`
}

// PodStatus contains status information about a pod.
type PodStatus struct {
	// Name is the pod name
	Name string `json:"name"`

	// Phase is the pod phase (Pending, Running, Succeeded, Failed, Unknown)
	Phase string `json:"phase"`

	// ContainerStatuses contains status of each container
	ContainerStatuses []apiv1.ContainerStatus `json:"container_statuses,omitempty"`

	// Message contains a human-readable message about the pod status
	Message string `json:"message,omitempty"`

	// Reason contains a brief reason for the pod's current status
	Reason string `json:"reason,omitempty"`
}

// DeploymentStatus represents the current state of a VICE deployment.
type DeploymentStatus struct {
	// ExternalID is the deployment's external identifier
	ExternalID string `json:"external_id"`

	// Exists indicates whether the deployment exists
	Exists bool `json:"exists"`

	// DeploymentStatus contains Deployment-specific status (nil if not found)
	DeploymentStatus *DeploymentStatusInfo `json:"deployment,omitempty"`

	// Pods contains status of all pods
	Pods []PodStatus `json:"pods,omitempty"`

	// ServiceExists indicates whether the Service exists
	ServiceExists bool `json:"service_exists"`

	// IngressExists indicates whether the Ingress exists
	IngressExists bool `json:"ingress_exists"`
}

// URLReadyResponse indicates whether the deployment is ready to serve traffic.
type URLReadyResponse struct {
	// Ready indicates overall readiness
	Ready bool `json:"ready"`

	// IngressExists indicates whether the Ingress exists
	IngressExists bool `json:"ingress_exists"`

	// ServiceExists indicates whether the Service exists
	ServiceExists bool `json:"service_exists"`

	// PodReady indicates whether at least one pod is ready
	PodReady bool `json:"pod_ready"`
}

// FileTransferRequest is sent to trigger a file transfer operation.
type FileTransferRequest struct {
	// Type is the transfer type ("download" or "upload")
	Type string `json:"type"`
}

// FileTransferResponse is returned after initiating a file transfer.
type FileTransferResponse struct {
	// Status is the result status ("initiated", "error")
	Status string `json:"status"`

	// TransferID is the unique identifier for tracking the transfer
	TransferID string `json:"transfer_id,omitempty"`

	// Error contains error details if status is "error"
	Error string `json:"error,omitempty"`
}

// LogsRequest contains parameters for retrieving pod logs.
type LogsRequest struct {
	// Container is the container name (optional, defaults to first container)
	Container string `json:"container,omitempty"`

	// SinceSeconds returns logs from the last N seconds (optional)
	SinceSeconds *int64 `json:"since_seconds,omitempty"`

	// TailLines returns the last N lines (optional)
	TailLines *int64 `json:"tail_lines,omitempty"`

	// Previous returns logs from previous container instance (optional)
	Previous bool `json:"previous,omitempty"`
}

// LogsResponse contains the requested logs.
type LogsResponse struct {
	// Logs contains the log content
	Logs string `json:"logs"`

	// Error contains error details if retrieval failed
	Error string `json:"error,omitempty"`
}

// HealthResponse is returned by the health check endpoint.
type HealthResponse struct {
	// Status is the health status ("healthy", "unhealthy")
	Status string `json:"status"`

	// Version is the deployer version
	Version string `json:"version,omitempty"`

	// Kubernetes indicates whether K8s API is accessible
	Kubernetes bool `json:"kubernetes"`

	// Message contains additional health information
	Message string `json:"message,omitempty"`
}
