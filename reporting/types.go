// Package reporting provides shared types for describing K8s resources
// associated with running VICE analyses. Both the incluster and operator
// packages use these types so that listing and status responses share a
// single canonical representation.
package reporting

import (
	"cmp"
	"slices"

	corev1 "k8s.io/api/core/v1"
	netv1 "k8s.io/api/networking/v1"
)

// MetaInfo contains useful information provided by multiple resource types.
type MetaInfo struct {
	Name              string `json:"name"`
	Namespace         string `json:"namespace"`
	AnalysisID        string `json:"analysisID"`
	AnalysisName      string `json:"analysisName"`
	AppName           string `json:"appName"`
	AppID             string `json:"appID"`
	ExternalID        string `json:"externalID"`
	UserID            string `json:"userID"`
	Username          string `json:"username"`
	CreationTimestamp string `json:"creationTimestamp"`
}

// DeploymentInfo contains information returned about a Deployment.
type DeploymentInfo struct {
	MetaInfo
	Image   string   `json:"image"`
	Command []string `json:"command"`
	Port    int32    `json:"port"`
	User    int64    `json:"user"`
	Group   int64    `json:"group"`
}

// PodInfo tracks information about the pods for a VICE analysis.
type PodInfo struct {
	MetaInfo
	Phase                 string                   `json:"phase"`
	Message               string                   `json:"message"`
	Reason                string                   `json:"reason"`
	ContainerStatuses     []corev1.ContainerStatus `json:"containerStatuses"`
	InitContainerStatuses []corev1.ContainerStatus `json:"initContainerStatuses"`
}

// ConfigMapInfo contains useful info about a config map.
type ConfigMapInfo struct {
	MetaInfo
	Data map[string]string `json:"data"`
}

// ServiceInfoPort contains information about a service's Port.
type ServiceInfoPort struct {
	Name           string `json:"name"`
	NodePort       int32  `json:"nodePort"`
	TargetPort     int32  `json:"targetPort"`
	TargetPortName string `json:"targetPortName"`
	Port           int32  `json:"port"`
	Protocol       string `json:"protocol"`
}

// ServiceInfo contains info about a service.
type ServiceInfo struct {
	MetaInfo
	Ports []ServiceInfoPort `json:"ports"`
}

// IngressInfo contains useful Ingress VICE info.
type IngressInfo struct {
	MetaInfo
	DefaultBackend string              `json:"defaultBackend"`
	Rules          []netv1.IngressRule `json:"rules"`
}

// RouteInfo contains information about an HTTPRoute used for VICE apps.
type RouteInfo struct {
	MetaInfo
	Hostnames []string `json:"hostnames"`
}

// ResourceInfo contains all of the K8s resource information about running
// VICE analyses that we know of and care about.
type ResourceInfo struct {
	Deployments []DeploymentInfo `json:"deployments"`
	Pods        []PodInfo        `json:"pods"`
	ConfigMaps  []ConfigMapInfo  `json:"configMaps"`
	Services    []ServiceInfo    `json:"services"`
	Ingresses   []IngressInfo    `json:"ingresses"`
	Routes      []RouteInfo      `json:"routes"`
}

// NewResourceInfo returns a ResourceInfo with all slices initialized to empty
// (non-nil) so that JSON serialization produces [] instead of null.
func NewResourceInfo() *ResourceInfo {
	return &ResourceInfo{
		Deployments: []DeploymentInfo{},
		Pods:        []PodInfo{},
		ConfigMaps:  []ConfigMapInfo{},
		Services:    []ServiceInfo{},
		Ingresses:   []IngressInfo{},
		Routes:      []RouteInfo{},
	}
}

// SortByCreationTime sorts each slice in the ResourceInfo by
// CreationTimestamp descending (newest first). CreationTimestamp is produced
// by metav1.Time.String() with the date in YYYY-MM-DD format, so
// lexicographic comparison is correct.
func SortByCreationTime(r *ResourceInfo) {
	byTime := func(a, b MetaInfo) int {
		// Reverse order: newer timestamps sort first.
		return cmp.Compare(b.CreationTimestamp, a.CreationTimestamp)
	}
	slices.SortFunc(r.Deployments, func(a, b DeploymentInfo) int { return byTime(a.MetaInfo, b.MetaInfo) })
	slices.SortFunc(r.Pods, func(a, b PodInfo) int { return byTime(a.MetaInfo, b.MetaInfo) })
	slices.SortFunc(r.ConfigMaps, func(a, b ConfigMapInfo) int { return byTime(a.MetaInfo, b.MetaInfo) })
	slices.SortFunc(r.Services, func(a, b ServiceInfo) int { return byTime(a.MetaInfo, b.MetaInfo) })
	slices.SortFunc(r.Ingresses, func(a, b IngressInfo) int { return byTime(a.MetaInfo, b.MetaInfo) })
	slices.SortFunc(r.Routes, func(a, b RouteInfo) int { return byTime(a.MetaInfo, b.MetaInfo) })
}
