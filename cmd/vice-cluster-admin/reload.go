package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Force reload cluster configurations",
	Long: `Force an immediate reload of cluster configurations from the database.

This is useful after making direct database changes or when you want to
ensure all cluster configurations are up to date.

Examples:
  vice-cluster-admin reload`,
	RunE: runReload,
}

func runReload(cmd *cobra.Command, args []string) error {
	client := NewClient(apiURL, timeout)

	count, err := client.ReloadClusters()
	if err != nil {
		return fmt.Errorf("failed to reload clusters: %w", err)
	}

	if output == "json" {
		result := map[string]interface{}{
			"status":        "reloaded",
			"cluster_count": count,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Printf("Cluster configurations reloaded successfully.\n")
	fmt.Printf("Total clusters: %d\n", count)
	return nil
}
