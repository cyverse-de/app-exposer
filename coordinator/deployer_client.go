package coordinator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/cyverse-de/app-exposer/vicetypes"
)

// DeployerClient provides methods to communicate with deployer services.
type DeployerClient struct {
	registry *ClusterRegistry
}

// NewDeployerClient creates a new DeployerClient.
func NewDeployerClient(registry *ClusterRegistry) *DeployerClient {
	return &DeployerClient{
		registry: registry,
	}
}

// CreateDeployment sends a deployment spec to the specified cluster's deployer.
func (c *DeployerClient) CreateDeployment(ctx context.Context, clusterID string, spec *vicetypes.VICEDeploymentSpec) (*vicetypes.DeploymentResponse, error) {
	cluster, ok := c.registry.GetCluster(clusterID)
	if !ok {
		return nil, fmt.Errorf("cluster not found: %s", clusterID)
	}

	client, ok := c.registry.GetHTTPClient(clusterID)
	if !ok {
		return nil, fmt.Errorf("HTTP client not found for cluster: %s", clusterID)
	}

	endpoint := cluster.DeployerURL + "/api/v1/deployments"

	body, err := json.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal deployment spec: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request to deployer: %w", err)
	}
	defer resp.Body.Close()

	var result vicetypes.DeploymentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return &result, fmt.Errorf("deployer returned error: %s", result.Error)
	}

	return &result, nil
}

// DeleteDeployment requests deletion of a deployment from the specified cluster.
func (c *DeployerClient) DeleteDeployment(ctx context.Context, clusterID, externalID, namespace string) (*vicetypes.DeploymentResponse, error) {
	cluster, ok := c.registry.GetCluster(clusterID)
	if !ok {
		return nil, fmt.Errorf("cluster not found: %s", clusterID)
	}

	client, ok := c.registry.GetHTTPClient(clusterID)
	if !ok {
		return nil, fmt.Errorf("HTTP client not found for cluster: %s", clusterID)
	}

	endpoint := fmt.Sprintf("%s/api/v1/deployments/%s", cluster.DeployerURL, url.PathEscape(externalID))
	if namespace != "" {
		endpoint += "?namespace=" + url.QueryEscape(namespace)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request to deployer: %w", err)
	}
	defer resp.Body.Close()

	var result vicetypes.DeploymentResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return &result, fmt.Errorf("deployer returned error: %s", result.Error)
	}

	return &result, nil
}

// GetStatus retrieves the status of a deployment from the specified cluster.
func (c *DeployerClient) GetStatus(ctx context.Context, clusterID, externalID, namespace string) (*vicetypes.DeploymentStatus, error) {
	cluster, ok := c.registry.GetCluster(clusterID)
	if !ok {
		return nil, fmt.Errorf("cluster not found: %s", clusterID)
	}

	client, ok := c.registry.GetHTTPClient(clusterID)
	if !ok {
		return nil, fmt.Errorf("HTTP client not found for cluster: %s", clusterID)
	}

	endpoint := fmt.Sprintf("%s/api/v1/deployments/%s/status", cluster.DeployerURL, url.PathEscape(externalID))
	if namespace != "" {
		endpoint += "?namespace=" + url.QueryEscape(namespace)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request to deployer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("deployer returned error: %s", string(body))
	}

	var result vicetypes.DeploymentStatus
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// CheckURLReady checks if a deployment is ready to serve traffic.
func (c *DeployerClient) CheckURLReady(ctx context.Context, clusterID, externalID, namespace string) (*vicetypes.URLReadyResponse, error) {
	cluster, ok := c.registry.GetCluster(clusterID)
	if !ok {
		return nil, fmt.Errorf("cluster not found: %s", clusterID)
	}

	client, ok := c.registry.GetHTTPClient(clusterID)
	if !ok {
		return nil, fmt.Errorf("HTTP client not found for cluster: %s", clusterID)
	}

	endpoint := fmt.Sprintf("%s/api/v1/deployments/%s/url-ready", cluster.DeployerURL, url.PathEscape(externalID))
	if namespace != "" {
		endpoint += "?namespace=" + url.QueryEscape(namespace)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request to deployer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("deployer returned error: %s", string(body))
	}

	var result vicetypes.URLReadyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// GetLogs retrieves logs from a deployment.
func (c *DeployerClient) GetLogs(ctx context.Context, clusterID, externalID, namespace string, logsReq *vicetypes.LogsRequest) (*vicetypes.LogsResponse, error) {
	cluster, ok := c.registry.GetCluster(clusterID)
	if !ok {
		return nil, fmt.Errorf("cluster not found: %s", clusterID)
	}

	client, ok := c.registry.GetHTTPClient(clusterID)
	if !ok {
		return nil, fmt.Errorf("HTTP client not found for cluster: %s", clusterID)
	}

	endpoint := fmt.Sprintf("%s/api/v1/deployments/%s/logs", cluster.DeployerURL, url.PathEscape(externalID))

	// Build query params
	params := url.Values{}
	if namespace != "" {
		params.Set("namespace", namespace)
	}
	if logsReq.Container != "" {
		params.Set("container", logsReq.Container)
	}
	if logsReq.SinceSeconds != nil {
		params.Set("since", fmt.Sprintf("%d", *logsReq.SinceSeconds))
	}
	if logsReq.TailLines != nil {
		params.Set("tail", fmt.Sprintf("%d", *logsReq.TailLines))
	}
	if logsReq.Previous {
		params.Set("previous", "true")
	}
	if len(params) > 0 {
		endpoint += "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request to deployer: %w", err)
	}
	defer resp.Body.Close()

	var result vicetypes.LogsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &result, nil
}

// CheckHealth checks the health of a deployer.
func (c *DeployerClient) CheckHealth(ctx context.Context, clusterID string) (*vicetypes.HealthResponse, error) {
	cluster, ok := c.registry.GetCluster(clusterID)
	if !ok {
		return nil, fmt.Errorf("cluster not found: %s", clusterID)
	}

	client, ok := c.registry.GetHTTPClient(clusterID)
	if !ok {
		return nil, fmt.Errorf("HTTP client not found for cluster: %s", clusterID)
	}

	endpoint := cluster.DeployerURL + "/api/v1/health"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return &vicetypes.HealthResponse{
			Status:     "unhealthy",
			Kubernetes: false,
			Message:    fmt.Sprintf("failed to connect: %v", err),
		}, nil
	}
	defer resp.Body.Close()

	var result vicetypes.HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return &vicetypes.HealthResponse{
			Status:     "unhealthy",
			Kubernetes: false,
			Message:    fmt.Sprintf("failed to decode response: %v", err),
		}, nil
	}

	return &result, nil
}

// IsDeployed checks if a deployment exists on any cluster.
// Returns the cluster ID if found, empty string if not found.
func (c *DeployerClient) IsDeployed(ctx context.Context, externalID string) (string, bool) {
	clusters := c.registry.ListEnabledClusters()

	for _, cluster := range clusters {
		status, err := c.GetStatus(ctx, cluster.ID, externalID, "")
		if err != nil {
			log.Debugf("error checking status on cluster %s: %v", cluster.Name, err)
			continue
		}
		if status.Exists {
			return cluster.ID, true
		}
	}

	return "", false
}
