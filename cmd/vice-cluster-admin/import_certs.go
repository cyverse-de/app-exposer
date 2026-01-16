package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/cyverse-de/app-exposer/vicetypes"
	"github.com/spf13/cobra"
)

var (
	importCluster    string
	importCACert     string
	importClientCert string
	importClientKey  string
)

var importCertsCmd = &cobra.Command{
	Use:   "import-certs",
	Short: "Import mTLS certificates for a cluster",
	Long: `Import or update mTLS certificates for an existing cluster.

This command updates the CA certificate, client certificate, and client private key
for a cluster without changing other settings. It also enables mTLS on the cluster.

Examples:
  vice-cluster-admin import-certs --cluster my-cluster \
    --ca-cert ca.crt --client-cert client.crt --client-key client.key`,
	RunE: runImportCerts,
}

func init() {
	importCertsCmd.Flags().StringVar(&importCluster, "cluster", "", "cluster name or ID (required)")
	importCertsCmd.Flags().StringVar(&importCACert, "ca-cert", "", "path to CA certificate file (required)")
	importCertsCmd.Flags().StringVar(&importClientCert, "client-cert", "", "path to client certificate file (required)")
	importCertsCmd.Flags().StringVar(&importClientKey, "client-key", "", "path to client private key file (required)")

	importCertsCmd.MarkFlagRequired("cluster")
	importCertsCmd.MarkFlagRequired("ca-cert")
	importCertsCmd.MarkFlagRequired("client-cert")
	importCertsCmd.MarkFlagRequired("client-key")
}

func runImportCerts(cmd *cobra.Command, args []string) error {
	// Read certificate files
	caCert, err := os.ReadFile(importCACert)
	if err != nil {
		return fmt.Errorf("failed to read CA certificate: %w", err)
	}

	clientCert, err := os.ReadFile(importClientCert)
	if err != nil {
		return fmt.Errorf("failed to read client certificate: %w", err)
	}

	clientKey, err := os.ReadFile(importClientKey)
	if err != nil {
		return fmt.Errorf("failed to read client key: %w", err)
	}

	client := NewClient(apiURL, timeout)

	// Find the cluster by name or ID
	cluster, err := getClusterByNameOrID(client, importCluster)
	if err != nil {
		return err
	}

	// Build update request with certificates and enable mTLS
	caCertStr := string(caCert)
	clientCertStr := string(clientCert)
	clientKeyStr := string(clientKey)
	mtlsEnabled := true

	req := &vicetypes.ClusterUpdateRequest{
		MTLSEnabled: &mtlsEnabled,
		CACert:      &caCertStr,
		ClientCert:  &clientCertStr,
		ClientKey:   &clientKeyStr,
	}

	result, err := client.UpdateCluster(cluster.ID, req)
	if err != nil {
		return fmt.Errorf("failed to import certificates: %w", err)
	}

	if output == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}

	fmt.Printf("Certificates imported successfully for cluster '%s':\n", result.Name)
	fmt.Printf("  ID:          %s\n", result.ID)
	fmt.Printf("  mTLS:        %t\n", result.MTLSEnabled)
	fmt.Printf("  Updated At:  %s\n", result.UpdatedAt.Format("2006-01-02 15:04:05"))

	return nil
}
