package main

import (
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"
)

var (
	// Global flags
	apiURL  string
	timeout time.Duration
	output  string
)

// rootCmd represents the base command
var rootCmd = &cobra.Command{
	Use:   "vice-cluster-admin",
	Short: "VICE cluster administration tool",
	Long: `vice-cluster-admin is a CLI tool for managing VICE deployer clusters
in the Discovery Environment.

It provides commands for registering, listing, updating, and managing
deployer clusters that run VICE applications.`,
}

func init() {
	// Global flags available to all commands
	rootCmd.PersistentFlags().StringVar(&apiURL, "api-url", "http://localhost:60000", "app-exposer API URL")
	rootCmd.PersistentFlags().DurationVar(&timeout, "timeout", 30*time.Second, "request timeout")
	rootCmd.PersistentFlags().StringVarP(&output, "output", "o", "table", "output format (table, json)")

	// Add subcommands
	rootCmd.AddCommand(listCmd)
	rootCmd.AddCommand(getCmd)
	rootCmd.AddCommand(registerCmd)
	rootCmd.AddCommand(updateCmd)
	rootCmd.AddCommand(deleteCmd)
	rootCmd.AddCommand(enableCmd)
	rootCmd.AddCommand(disableCmd)
	rootCmd.AddCommand(reloadCmd)
	rootCmd.AddCommand(importCertsCmd)
	rootCmd.AddCommand(generateKeyCmd)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
