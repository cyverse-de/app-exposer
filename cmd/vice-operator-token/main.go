// vice-operator-token reads a JSON config containing Keycloak client-credentials
// settings and prints a fresh OAuth2 access token on stdout. The intended use is
// inline shell substitution to authenticate curl calls against vice-operator:
//
//	curl -H "Authorization: Bearer $(vice-operator-token --config foo.json)" \
//	     https://vice-operator.example.org/image-cache
//
// The Keycloak service account behind client_id must hold the realm role that
// vice-operator was started with (see --admin-role; default "vice-operator"),
// otherwise the token will validate but fail vice-operator's admin gate.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"golang.org/x/oauth2/clientcredentials"
)

// Config mirrors the vice.keycloak.* keys used by app-exposer's YAML config
// (see configs/default.yml) so admins can copy values verbatim.
type Config struct {
	KeycloakBaseURL string `json:"keycloak_base_url"`
	Realm           string `json:"realm"`
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"client_secret"`
}

func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	if c.KeycloakBaseURL == "" || c.Realm == "" || c.ClientID == "" || c.ClientSecret == "" {
		return nil, fmt.Errorf("config %s is missing one or more required fields: keycloak_base_url, realm, client_id, client_secret", path)
	}
	return &c, nil
}

func run() error {
	configPath := flag.String("config", "", "path to JSON config file with Keycloak client-credentials settings (required)")
	flag.Parse()

	if *configPath == "" {
		return fmt.Errorf("--config is required")
	}

	cfg, err := loadConfig(*configPath)
	if err != nil {
		return err
	}

	// Token URL format matches cmd/app-exposer/main.go so both code paths
	// stay consistent against the same Keycloak deployment.
	tokenURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/token", cfg.KeycloakBaseURL, cfg.Realm)
	cc := &clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     tokenURL,
	}

	tok, err := cc.Token(context.Background())
	if err != nil {
		return fmt.Errorf("requesting token from %s: %w", tokenURL, err)
	}

	fmt.Println(tok.AccessToken)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "vice-operator-token:", err)
		os.Exit(1)
	}
}
