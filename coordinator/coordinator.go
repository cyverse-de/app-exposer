// Package coordinator provides the coordinator-side logic for multi-cluster
// VICE deployments, including cluster registry management, deployment routing,
// and spec building.
package coordinator

import (
	"context"
	"fmt"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/vicetypes"
	"github.com/cyverse-de/model/v9"
	"github.com/jmoiron/sqlx"
)

// Coordinator orchestrates VICE deployments across multiple clusters.
// It handles cluster selection, spec building, and deployment routing.
type Coordinator struct {
	registry       *ClusterRegistry
	deployerClient *DeployerClient
	selector       *ClusterSelector
	specBuilder    *SpecBuilder

	// Database for tracking which cluster each deployment is on
	db *sqlx.DB

	// Configuration
	enabled bool
}

// CoordinatorConfig contains configuration for creating a Coordinator.
type CoordinatorConfig struct {
	// Whether coordinator mode is enabled
	Enabled bool

	// Cluster selection strategy
	SelectionStrategy SelectionStrategy

	// Spec builder configuration
	SpecBuilderConfig SpecBuilderConfig
}

// NewCoordinator creates a new Coordinator.
// If not enabled, returns a Coordinator that will return errors for all operations.
func NewCoordinator(
	db *sqlx.DB,
	registry *ClusterRegistry,
	apps *apps.Apps,
	config CoordinatorConfig,
) *Coordinator {
	deployerClient := NewDeployerClient(registry)
	selector := NewClusterSelector(registry, deployerClient, config.SelectionStrategy)
	specBuilder := NewSpecBuilder(config.SpecBuilderConfig, apps)

	return &Coordinator{
		registry:       registry,
		deployerClient: deployerClient,
		selector:       selector,
		specBuilder:    specBuilder,
		db:             db,
		enabled:        config.Enabled,
	}
}

// IsEnabled returns whether coordinator mode is enabled.
func (c *Coordinator) IsEnabled() bool {
	return c.enabled
}

// Launch deploys a VICE analysis to a selected cluster.
func (c *Coordinator) Launch(ctx context.Context, job *model.Job) error {
	if !c.enabled {
		return fmt.Errorf("coordinator mode is not enabled")
	}

	// Check if already deployed on any cluster
	if clusterID, found := c.deployerClient.IsDeployed(ctx, job.InvocationID); found {
		log.Infof("deployment %s already exists on cluster %s", job.InvocationID, clusterID)
		return nil
	}

	// Select a cluster for the deployment
	selected, err := c.selector.Select(ctx)
	if err != nil {
		return fmt.Errorf("failed to select cluster: %w", err)
	}

	log.Infof("selected cluster %s (%s) for deployment %s", selected.ClusterName, selected.ClusterID, job.InvocationID)

	// Build the deployment spec
	spec, err := c.specBuilder.BuildSpec(ctx, job)
	if err != nil {
		return fmt.Errorf("failed to build deployment spec: %w", err)
	}

	// Send to the deployer
	resp, err := c.deployerClient.CreateDeployment(ctx, selected.ClusterID, spec)
	if err != nil {
		return fmt.Errorf("failed to create deployment on cluster %s: %w", selected.ClusterName, err)
	}

	if resp.Status == "error" {
		return fmt.Errorf("deployer returned error: %s", resp.Error)
	}

	// Record which cluster the deployment is on
	if err := c.recordDeploymentCluster(ctx, job.InvocationID, selected.ClusterID); err != nil {
		log.Errorf("failed to record deployment cluster: %v", err)
		// Don't fail the whole operation for this
	}

	log.Infof("deployment %s created on cluster %s", job.InvocationID, selected.ClusterName)
	return nil
}

// LaunchToCluster deploys a VICE analysis to a specific cluster by name.
// This is used when the caller explicitly specifies which cluster to use.
func (c *Coordinator) LaunchToCluster(ctx context.Context, job *model.Job, clusterName string) error {
	if !c.enabled {
		return fmt.Errorf("coordinator mode is not enabled")
	}

	// Check if already deployed on any cluster
	if existingCluster, found := c.deployerClient.IsDeployed(ctx, job.InvocationID); found {
		log.Infof("deployment %s already exists on cluster %s", job.InvocationID, existingCluster)
		return nil
	}

	// Look up cluster by name
	cluster, ok := c.registry.GetClusterByName(clusterName)
	if !ok {
		return fmt.Errorf("cluster not found: %s", clusterName)
	}

	if !cluster.Enabled {
		return fmt.Errorf("cluster %s is disabled", clusterName)
	}

	log.Infof("deploying %s to specified cluster %s (%s)", job.InvocationID, cluster.Name, cluster.ID)

	// Build the deployment spec
	spec, err := c.specBuilder.BuildSpec(ctx, job)
	if err != nil {
		return fmt.Errorf("failed to build deployment spec: %w", err)
	}

	// Send to the deployer on the specified cluster
	resp, err := c.deployerClient.CreateDeployment(ctx, cluster.ID, spec)
	if err != nil {
		return fmt.Errorf("failed to create deployment on cluster %s: %w", clusterName, err)
	}

	if resp.Status == "error" {
		return fmt.Errorf("deployer returned error: %s", resp.Error)
	}

	// Record which cluster the deployment is on
	if err := c.recordDeploymentCluster(ctx, job.InvocationID, cluster.ID); err != nil {
		log.Errorf("failed to record deployment cluster: %v", err)
		// Don't fail the whole operation for this
	}

	log.Infof("deployment %s created on cluster %s", job.InvocationID, clusterName)
	return nil
}

// Exit terminates a VICE analysis on whichever cluster it's running on.
func (c *Coordinator) Exit(ctx context.Context, externalID string) error {
	if !c.enabled {
		return fmt.Errorf("coordinator mode is not enabled")
	}

	// Find which cluster has the deployment
	selected, err := c.selector.SelectForExternalID(ctx, externalID)
	if err != nil {
		// If not found on any cluster, it might already be gone
		log.Warnf("deployment %s not found on any cluster: %v", externalID, err)
		return nil
	}

	log.Infof("found deployment %s on cluster %s, initiating exit", externalID, selected.ClusterName)

	// Send delete request to the deployer
	resp, err := c.deployerClient.DeleteDeployment(ctx, selected.ClusterID, externalID, "")
	if err != nil {
		return fmt.Errorf("failed to delete deployment on cluster %s: %w", selected.ClusterName, err)
	}

	if resp.Status == "error" {
		return fmt.Errorf("deployer returned error: %s", resp.Error)
	}

	// Remove the cluster tracking record
	if err := c.removeDeploymentCluster(ctx, externalID); err != nil {
		log.Errorf("failed to remove deployment cluster record: %v", err)
	}

	log.Infof("deployment %s deleted from cluster %s", externalID, selected.ClusterName)
	return nil
}

// GetStatus returns the status of a deployment.
func (c *Coordinator) GetStatus(ctx context.Context, externalID string) (*vicetypes.DeploymentStatus, error) {
	if !c.enabled {
		return nil, fmt.Errorf("coordinator mode is not enabled")
	}

	// Find which cluster has the deployment
	selected, err := c.selector.SelectForExternalID(ctx, externalID)
	if err != nil {
		return &vicetypes.DeploymentStatus{
			Exists: false,
		}, nil
	}

	return c.deployerClient.GetStatus(ctx, selected.ClusterID, externalID, "")
}

// CheckURLReady checks if a deployment is ready to serve traffic.
func (c *Coordinator) CheckURLReady(ctx context.Context, externalID string) (*vicetypes.URLReadyResponse, error) {
	if !c.enabled {
		return nil, fmt.Errorf("coordinator mode is not enabled")
	}

	// Find which cluster has the deployment
	selected, err := c.selector.SelectForExternalID(ctx, externalID)
	if err != nil {
		return &vicetypes.URLReadyResponse{
			Ready: false,
		}, nil
	}

	return c.deployerClient.CheckURLReady(ctx, selected.ClusterID, externalID, "")
}

// GetLogs retrieves logs from a deployment.
func (c *Coordinator) GetLogs(ctx context.Context, externalID string, req *vicetypes.LogsRequest) (*vicetypes.LogsResponse, error) {
	if !c.enabled {
		return nil, fmt.Errorf("coordinator mode is not enabled")
	}

	// Find which cluster has the deployment
	selected, err := c.selector.SelectForExternalID(ctx, externalID)
	if err != nil {
		return nil, fmt.Errorf("deployment not found: %w", err)
	}

	return c.deployerClient.GetLogs(ctx, selected.ClusterID, externalID, "", req)
}

// IsDeployed checks if a deployment exists on any cluster.
func (c *Coordinator) IsDeployed(ctx context.Context, externalID string) (bool, error) {
	if !c.enabled {
		return false, fmt.Errorf("coordinator mode is not enabled")
	}

	_, found := c.deployerClient.IsDeployed(ctx, externalID)
	return found, nil
}

// GetDeploymentCluster returns the cluster ID where a deployment is running.
// First checks the local tracking table, then falls back to querying all clusters.
func (c *Coordinator) GetDeploymentCluster(ctx context.Context, externalID string) (string, error) {
	if !c.enabled {
		return "", fmt.Errorf("coordinator mode is not enabled")
	}

	// Try local tracking first
	clusterID, err := c.lookupDeploymentCluster(ctx, externalID)
	if err == nil && clusterID != "" {
		return clusterID, nil
	}

	// Fall back to checking all clusters
	clusterID, found := c.deployerClient.IsDeployed(ctx, externalID)
	if !found {
		return "", fmt.Errorf("deployment %s not found on any cluster", externalID)
	}

	return clusterID, nil
}

// recordDeploymentCluster records which cluster a deployment is on.
func (c *Coordinator) recordDeploymentCluster(ctx context.Context, externalID, clusterID string) error {
	const query = `
		INSERT INTO vice_deployment_clusters (external_id, cluster_id)
		VALUES ($1, $2)
		ON CONFLICT (external_id) DO UPDATE SET
			cluster_id = EXCLUDED.cluster_id,
			updated_at = now()
	`
	_, err := c.db.ExecContext(ctx, query, externalID, clusterID)
	return err
}

// removeDeploymentCluster removes the cluster tracking record for a deployment.
func (c *Coordinator) removeDeploymentCluster(ctx context.Context, externalID string) error {
	const query = `DELETE FROM vice_deployment_clusters WHERE external_id = $1`
	_, err := c.db.ExecContext(ctx, query, externalID)
	return err
}

// lookupDeploymentCluster looks up which cluster a deployment is on from the tracking table.
func (c *Coordinator) lookupDeploymentCluster(ctx context.Context, externalID string) (string, error) {
	const query = `SELECT cluster_id FROM vice_deployment_clusters WHERE external_id = $1`
	var clusterID string
	err := c.db.GetContext(ctx, &clusterID, query, externalID)
	if err != nil {
		return "", err
	}
	return clusterID, nil
}

// TriggerFileTransfer initiates a file transfer (upload or download) on a deployment.
func (c *Coordinator) TriggerFileTransfer(ctx context.Context, externalID, transferType string, async bool) error {
	if !c.enabled {
		return fmt.Errorf("coordinator mode is not enabled")
	}

	// Find which cluster has the deployment
	selected, err := c.selector.SelectForExternalID(ctx, externalID)
	if err != nil {
		return fmt.Errorf("deployment not found: %w", err)
	}

	log.Infof("triggering %s file transfer for %s on cluster %s", transferType, externalID, selected.ClusterName)

	resp, err := c.deployerClient.TriggerFileTransfer(ctx, selected.ClusterID, externalID, "", transferType, async)
	if err != nil {
		return fmt.Errorf("failed to trigger file transfer on cluster %s: %w", selected.ClusterName, err)
	}

	if resp.Status == "error" || resp.Status == "failed" {
		return fmt.Errorf("file transfer error: %s", resp.Error)
	}

	log.Infof("file transfer %s for %s: %s", transferType, externalID, resp.Status)
	return nil
}

// Registry returns the cluster registry for direct access.
func (c *Coordinator) Registry() *ClusterRegistry {
	return c.registry
}

// DeployerClient returns the deployer client for direct access.
func (c *Coordinator) DeployerClient() *DeployerClient {
	return c.deployerClient
}
