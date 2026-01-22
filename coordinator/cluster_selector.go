package coordinator

import (
	"context"
	"fmt"
	"sync"

	"github.com/cyverse-de/app-exposer/vicetypes"
)

// SelectionStrategy defines how clusters are selected for deployments.
type SelectionStrategy string

const (
	// StrategyPriority selects clusters by priority (lowest priority number first).
	StrategyPriority SelectionStrategy = "priority"

	// StrategyRoundRobin distributes deployments evenly across clusters.
	StrategyRoundRobin SelectionStrategy = "round-robin"

	// StrategyLeastLoaded selects the cluster with the fewest active deployments.
	// Note: This requires tracking deployment counts, which is not yet implemented.
	StrategyLeastLoaded SelectionStrategy = "least-loaded"
)

// ClusterSelector selects which cluster should receive a deployment.
type ClusterSelector struct {
	registry       *ClusterRegistry
	deployerClient *DeployerClient
	strategy       SelectionStrategy

	// For round-robin strategy
	mu            sync.Mutex
	roundRobinIdx int
}

// NewClusterSelector creates a new ClusterSelector.
func NewClusterSelector(registry *ClusterRegistry, deployerClient *DeployerClient, strategy SelectionStrategy) *ClusterSelector {
	if strategy == "" {
		strategy = StrategyPriority
	}

	return &ClusterSelector{
		registry:       registry,
		deployerClient: deployerClient,
		strategy:       strategy,
	}
}

// SelectResult contains the result of cluster selection.
type SelectResult struct {
	ClusterID   string
	ClusterName string
	DeployerURL string
}

// Select chooses a cluster for a new deployment.
// It returns an error if no suitable cluster is available.
func (s *ClusterSelector) Select(ctx context.Context) (*SelectResult, error) {
	clusters := s.registry.ListEnabledClusters()
	if len(clusters) == 0 {
		return nil, fmt.Errorf("no enabled clusters available")
	}

	// Filter to only healthy clusters
	healthyClusters := s.filterHealthyClusters(ctx, clusters)
	if len(healthyClusters) == 0 {
		return nil, fmt.Errorf("no healthy clusters available")
	}

	var selected *vicetypes.ClusterInfo

	switch s.strategy {
	case StrategyRoundRobin:
		selected = s.selectRoundRobin(healthyClusters)
	case StrategyLeastLoaded:
		// Fall back to priority for now since we don't have load tracking yet
		log.Warn("least-loaded strategy not fully implemented, falling back to priority")
		selected = s.selectByPriority(healthyClusters)
	case StrategyPriority:
		fallthrough
	default:
		selected = s.selectByPriority(healthyClusters)
	}

	return &SelectResult{
		ClusterID:   selected.ID,
		ClusterName: selected.Name,
		DeployerURL: selected.DeployerURL,
	}, nil
}

// SelectForExternalID finds the cluster where an existing deployment is running.
// This is used for operations like exit, file transfers, etc. on existing deployments.
func (s *ClusterSelector) SelectForExternalID(ctx context.Context, externalID string) (*SelectResult, error) {
	clusterID, found := s.deployerClient.IsDeployed(ctx, externalID)
	if !found {
		return nil, fmt.Errorf("deployment %s not found on any cluster", externalID)
	}

	cluster, ok := s.registry.GetCluster(clusterID)
	if !ok {
		return nil, fmt.Errorf("cluster %s not found in registry", clusterID)
	}

	return &SelectResult{
		ClusterID:   cluster.ID,
		ClusterName: cluster.Name,
		DeployerURL: cluster.DeployerURL,
	}, nil
}

// filterHealthyClusters returns only clusters that are responding to health checks.
func (s *ClusterSelector) filterHealthyClusters(ctx context.Context, clusters []*vicetypes.ClusterInfo) []*vicetypes.ClusterInfo {
	var healthy []*vicetypes.ClusterInfo

	for _, cluster := range clusters {
		health, err := s.deployerClient.CheckHealth(ctx, cluster.ID)
		if err != nil {
			log.Warnf("cluster %s health check failed: %v", cluster.Name, err)
			continue
		}
		if health.Status == "healthy" {
			healthy = append(healthy, cluster)
		} else {
			log.Warnf("cluster %s is not healthy: %s", cluster.Name, health.Message)
		}
	}

	return healthy
}

// selectByPriority returns the cluster with the lowest priority number.
// Clusters are already sorted by priority from the registry.
func (s *ClusterSelector) selectByPriority(clusters []*vicetypes.ClusterInfo) *vicetypes.ClusterInfo {
	// Clusters come sorted by priority from ListEnabledClusters
	return clusters[0]
}

// selectRoundRobin distributes deployments evenly across clusters.
func (s *ClusterSelector) selectRoundRobin(clusters []*vicetypes.ClusterInfo) *vicetypes.ClusterInfo {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.roundRobinIdx >= len(clusters) {
		s.roundRobinIdx = 0
	}

	selected := clusters[s.roundRobinIdx]
	s.roundRobinIdx++

	return selected
}
