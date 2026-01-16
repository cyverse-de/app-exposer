package main

import (
	"fmt"

	"github.com/cyverse-de/app-exposer/coordinator"
	"github.com/spf13/cobra"
)

var generateKeyCmd = &cobra.Command{
	Use:   "generate-key",
	Short: "Generate an encryption key",
	Long: `Generate a random 32-byte AES-256 encryption key.

The key is output as a base64-encoded string that can be used with the
APP_EXPOSER_ENCRYPTION_KEY environment variable or the encryption.key
configuration setting.

Examples:
  # Generate a new key
  vice-cluster-admin generate-key

  # Set as environment variable
  export APP_EXPOSER_ENCRYPTION_KEY=$(vice-cluster-admin generate-key)`,
	RunE: runGenerateKey,
}

func runGenerateKey(cmd *cobra.Command, args []string) error {
	key, err := coordinator.GenerateKeyBase64()
	if err != nil {
		return fmt.Errorf("failed to generate key: %w", err)
	}

	fmt.Println(key)
	return nil
}
