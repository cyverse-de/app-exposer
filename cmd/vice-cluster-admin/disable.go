package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var disableName string

var disableCmd = &cobra.Command{
	Use:   "disable",
	Short: "Disable a cluster",
	Long: `Disable a cluster to stop it from accepting new VICE deployments.

Existing deployments on the cluster will continue to run, but no new
deployments will be scheduled to this cluster.

Examples:
  vice-cluster-admin disable --name my-cluster`,
	RunE: runDisable,
}

func init() {
	disableCmd.Flags().StringVar(&disableName, "name", "", "cluster name or ID (required)")
	disableCmd.MarkFlagRequired("name")
}

func runDisable(cmd *cobra.Command, args []string) error {
	client := NewClient(apiURL, timeout)

	// Find the cluster by name or ID
	cluster, err := getClusterByNameOrID(client, disableName)
	if err != nil {
		return err
	}

	result, err := client.DisableCluster(cluster.ID)
	if err != nil {
		return fmt.Errorf("failed to disable cluster: %w", err)
	}

	if output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Printf("Cluster '%s' disabled successfully.\n", result.Name)
	return nil
}
