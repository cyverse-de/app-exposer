package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cyverse-de/app-exposer/vicetypes"
	"github.com/spf13/cobra"
)

var (
	updateName        string
	updateDeployerURL string
	updatePriority    int
	updateEnabled     bool
	updateDisabled    bool
	updateSetPriority bool
)

var updateCmd = &cobra.Command{
	Use:   "update",
	Short: "Update cluster configuration",
	Long: `Update an existing cluster's configuration.

Only the fields you specify will be updated. Use --enable or --disable to change
the enabled state, and --priority to change the selection priority.

Examples:
  # Update deployer URL
  vice-cluster-admin update --name my-cluster --deployer-url https://new-deployer:8443

  # Update priority
  vice-cluster-admin update --name my-cluster --priority 50

  # Disable a cluster
  vice-cluster-admin update --name my-cluster --disable`,
	RunE: runUpdate,
}

func init() {
	updateCmd.Flags().StringVar(&updateName, "name", "", "cluster name or ID (required)")
	updateCmd.Flags().StringVar(&updateDeployerURL, "deployer-url", "", "new deployer service URL")
	updateCmd.Flags().IntVar(&updatePriority, "priority", 0, "new selection priority")
	updateCmd.Flags().BoolVar(&updateSetPriority, "set-priority", false, "set priority (use with --priority)")
	updateCmd.Flags().BoolVar(&updateEnabled, "enable", false, "enable the cluster")
	updateCmd.Flags().BoolVar(&updateDisabled, "disable", false, "disable the cluster")

	updateCmd.MarkFlagRequired("name")
}

func runUpdate(cmd *cobra.Command, args []string) error {
	if updateEnabled && updateDisabled {
		return fmt.Errorf("cannot specify both --enable and --disable")
	}

	client := NewClient(apiURL, timeout)

	// Find the cluster by name or ID
	cluster, err := getClusterByNameOrID(client, updateName)
	if err != nil {
		return err
	}

	// Build update request with only the fields that were specified
	req := &vicetypes.ClusterUpdateRequest{}
	hasUpdates := false

	if cmd.Flags().Changed("deployer-url") {
		req.DeployerURL = &updateDeployerURL
		hasUpdates = true
	}

	if cmd.Flags().Changed("priority") || updateSetPriority {
		req.Priority = &updatePriority
		hasUpdates = true
	}

	if updateEnabled {
		enabled := true
		req.Enabled = &enabled
		hasUpdates = true
	}

	if updateDisabled {
		enabled := false
		req.Enabled = &enabled
		hasUpdates = true
	}

	if !hasUpdates {
		return fmt.Errorf("no updates specified")
	}

	result, err := client.UpdateCluster(cluster.ID, req)
	if err != nil {
		return fmt.Errorf("failed to update cluster: %w", err)
	}

	if output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Printf("Cluster updated successfully:\n")
	fmt.Printf("  ID:          %s\n", result.ID)
	fmt.Printf("  Name:        %s\n", result.Name)
	fmt.Printf("  Deployer URL: %s\n", result.DeployerURL)
	fmt.Printf("  Enabled:     %t\n", result.Enabled)
	fmt.Printf("  Priority:    %d\n", result.Priority)
	fmt.Printf("  Updated At:  %s\n", result.UpdatedAt.Format("2006-01-02 15:04:05"))

	return nil
}
