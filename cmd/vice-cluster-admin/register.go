package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cyverse-de/app-exposer/vicetypes"
	"github.com/spf13/cobra"
)

var (
	registerName        string
	registerDeployerURL string
	registerPriority    int
	registerEnabled     bool
	registerMTLS        bool
	registerCACert      string
	registerClientCert  string
	registerClientKey   string
)

var registerCmd = &cobra.Command{
	Use:   "register",
	Short: "Register a new cluster",
	Long: `Register a new deployer cluster with optional mTLS configuration.

Examples:
  # Register a cluster without mTLS
  vice-cluster-admin register --name my-cluster --deployer-url http://deployer:8080

  # Register a cluster with mTLS
  vice-cluster-admin register --name my-cluster --deployer-url https://deployer:8443 \
    --mtls --ca-cert ca.crt --client-cert client.crt --client-key client.key`,
	RunE: runRegister,
}

func init() {
	registerCmd.Flags().StringVar(&registerName, "name", "", "cluster name (required)")
	registerCmd.Flags().StringVar(&registerDeployerURL, "deployer-url", "", "deployer service URL (required)")
	registerCmd.Flags().IntVar(&registerPriority, "priority", 100, "selection priority (lower = higher priority)")
	registerCmd.Flags().BoolVar(&registerEnabled, "enabled", true, "enable cluster for deployments")
	registerCmd.Flags().BoolVar(&registerMTLS, "mtls", false, "enable mTLS authentication")
	registerCmd.Flags().StringVar(&registerCACert, "ca-cert", "", "path to CA certificate file (required if --mtls)")
	registerCmd.Flags().StringVar(&registerClientCert, "client-cert", "", "path to client certificate file (required if --mtls)")
	registerCmd.Flags().StringVar(&registerClientKey, "client-key", "", "path to client private key file (required if --mtls)")

	registerCmd.MarkFlagRequired("name")
	registerCmd.MarkFlagRequired("deployer-url")
}

func runRegister(cmd *cobra.Command, args []string) error {
	// Validate mTLS flags
	if registerMTLS {
		if registerCACert == "" || registerClientCert == "" || registerClientKey == "" {
			return fmt.Errorf("--ca-cert, --client-cert, and --client-key are required when --mtls is enabled")
		}
	}

	req := &vicetypes.ClusterRegistrationRequest{
		Name:        registerName,
		DeployerURL: registerDeployerURL,
		Priority:    registerPriority,
		Enabled:     registerEnabled,
		MTLSEnabled: registerMTLS,
	}

	// Read certificate files if mTLS is enabled
	if registerMTLS {
		caCert, err := os.ReadFile(registerCACert)
		if err != nil {
			return fmt.Errorf("failed to read CA certificate: %w", err)
		}
		req.CACert = string(caCert)

		clientCert, err := os.ReadFile(registerClientCert)
		if err != nil {
			return fmt.Errorf("failed to read client certificate: %w", err)
		}
		req.ClientCert = string(clientCert)

		clientKey, err := os.ReadFile(registerClientKey)
		if err != nil {
			return fmt.Errorf("failed to read client key: %w", err)
		}
		req.ClientKey = string(clientKey)
	}

	client := NewClient(apiURL, timeout)

	result, err := client.RegisterCluster(req)
	if err != nil {
		return fmt.Errorf("failed to register cluster: %w", err)
	}

	if output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Printf("Cluster registered successfully:\n")
	fmt.Printf("  ID:          %s\n", result.ID)
	fmt.Printf("  Name:        %s\n", result.Name)
	fmt.Printf("  Deployer URL: %s\n", result.DeployerURL)
	fmt.Printf("  Enabled:     %t\n", result.Enabled)
	fmt.Printf("  Priority:    %d\n", result.Priority)
	fmt.Printf("  mTLS:        %t\n", result.MTLSEnabled)

	return nil
}
