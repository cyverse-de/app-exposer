package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var enableName string

var enableCmd = &cobra.Command{
	Use:   "enable",
	Short: "Enable a cluster for deployments",
	Long: `Enable a cluster to accept new VICE deployments.

Examples:
  vice-cluster-admin enable --name my-cluster`,
	RunE: runEnable,
}

func init() {
	enableCmd.Flags().StringVar(&enableName, "name", "", "cluster name or ID (required)")
	enableCmd.MarkFlagRequired("name")
}

func runEnable(cmd *cobra.Command, args []string) error {
	client := NewClient(apiURL, timeout)

	// Find the cluster by name or ID
	cluster, err := getClusterByNameOrID(client, enableName)
	if err != nil {
		return err
	}

	result, err := client.EnableCluster(cluster.ID)
	if err != nil {
		return fmt.Errorf("failed to enable cluster: %w", err)
	}

	if output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Printf("Cluster '%s' enabled successfully.\n", result.Name)
	return nil
}
