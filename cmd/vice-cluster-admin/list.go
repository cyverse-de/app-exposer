package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List all registered clusters",
	Long:  `List all registered deployer clusters with their status and configuration.`,
	RunE:  runList,
}

func runList(cmd *cobra.Command, args []string) error {
	client := NewClient(apiURL, timeout)

	result, err := client.ListClusters()
	if err != nil {
		return fmt.Errorf("failed to list clusters: %w", err)
	}

	if output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	// Table output
	if len(result.Clusters) == 0 {
		fmt.Println("No clusters registered.")
		return nil
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"ID", "Name", "URL", "Enabled", "Priority", "mTLS", "Status"})
	table.SetBorder(false)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)

	for _, c := range result.Clusters {
		enabled := "No"
		if c.Enabled {
			enabled = "Yes"
		}
		mtls := "No"
		if c.MTLSEnabled {
			mtls = "Yes"
		}
		// Truncate ID to first 8 chars for display
		shortID := c.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		table.Append([]string{
			shortID,
			c.Name,
			c.DeployerURL,
			enabled,
			fmt.Sprintf("%d", c.Priority),
			mtls,
			c.Status,
		})
	}

	table.Render()
	fmt.Printf("\nTotal: %d cluster(s)\n", result.Total)

	return nil
}
