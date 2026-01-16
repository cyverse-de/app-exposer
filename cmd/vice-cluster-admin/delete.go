package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

var (
	deleteName  string
	deleteForce bool
)

var deleteCmd = &cobra.Command{
	Use:   "delete",
	Short: "Delete a cluster",
	Long: `Delete a cluster from the registry.

By default, this command will prompt for confirmation. Use --force to skip
the confirmation prompt.

Examples:
  vice-cluster-admin delete --name my-cluster
  vice-cluster-admin delete --name my-cluster --force`,
	RunE: runDelete,
}

func init() {
	deleteCmd.Flags().StringVar(&deleteName, "name", "", "cluster name or ID (required)")
	deleteCmd.Flags().BoolVarP(&deleteForce, "force", "f", false, "skip confirmation prompt")

	deleteCmd.MarkFlagRequired("name")
}

func runDelete(cmd *cobra.Command, args []string) error {
	client := NewClient(apiURL, timeout)

	// Find the cluster by name or ID
	cluster, err := getClusterByNameOrID(client, deleteName)
	if err != nil {
		return err
	}

	// Confirm deletion unless --force is specified
	if !deleteForce {
		fmt.Printf("Are you sure you want to delete cluster '%s' (%s)? [y/N]: ", cluster.Name, cluster.ID)
		reader := bufio.NewReader(os.Stdin)
		response, err := reader.ReadString('\n')
		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}

		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Deletion cancelled.")
			return nil
		}
	}

	if err := client.DeleteCluster(cluster.ID); err != nil {
		return fmt.Errorf("failed to delete cluster: %w", err)
	}

	fmt.Printf("Cluster '%s' deleted successfully.\n", cluster.Name)
	return nil
}
