package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cyverse-de/app-exposer/vicetypes"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var (
	getName string
)

var getCmd = &cobra.Command{
	Use:   "get",
	Short: "Get details for a specific cluster",
	Long:  `Get detailed information about a specific cluster by ID or name.`,
	RunE:  runGet,
}

func init() {
	getCmd.Flags().StringVar(&getName, "name", "", "cluster name or ID (required)")
	getCmd.MarkFlagRequired("name")
}

func runGet(cmd *cobra.Command, args []string) error {
	client := NewClient(apiURL, timeout)

	// Try to get by name first by listing and filtering
	cluster, err := getClusterByNameOrID(client, getName)
	if err != nil {
		return err
	}

	if output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(cluster)
	}

	// Detailed table output
	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"Field", "Value"})
	table.SetBorder(false)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetColWidth(60)

	enabled := "No"
	if cluster.Enabled {
		enabled = "Yes"
	}
	mtls := "No"
	if cluster.MTLSEnabled {
		mtls = "Yes"
	}
	lastCheck := "N/A"
	if cluster.LastHealthCheck != nil {
		lastCheck = cluster.LastHealthCheck.Format("2006-01-02 15:04:05")
	}

	table.Append([]string{"ID", cluster.ID})
	table.Append([]string{"Name", cluster.Name})
	table.Append([]string{"Deployer URL", cluster.DeployerURL})
	table.Append([]string{"Enabled", enabled})
	table.Append([]string{"Priority", fmt.Sprintf("%d", cluster.Priority)})
	table.Append([]string{"mTLS Enabled", mtls})
	table.Append([]string{"Status", cluster.Status})
	table.Append([]string{"Last Health Check", lastCheck})
	table.Append([]string{"Created At", cluster.CreatedAt.Format("2006-01-02 15:04:05")})
	table.Append([]string{"Updated At", cluster.UpdatedAt.Format("2006-01-02 15:04:05")})

	table.Render()

	return nil
}

// getClusterByNameOrID looks up a cluster by name or ID.
func getClusterByNameOrID(client *Client, nameOrID string) (*vicetypes.ClusterResponse, error) {
	// First try direct lookup by ID
	cluster, err := client.GetCluster(nameOrID)
	if err == nil {
		return cluster, nil
	}

	// If not found, try to find by name
	list, err := client.ListClusters()
	if err != nil {
		return nil, fmt.Errorf("failed to list clusters: %w", err)
	}

	for _, c := range list.Clusters {
		if c.Name == nameOrID {
			return client.GetCluster(c.ID)
		}
	}

	return nil, fmt.Errorf("cluster not found: %s", nameOrID)
}
