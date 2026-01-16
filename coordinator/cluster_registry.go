// Package coordinator provides the coordinator-side logic for multi-cluster
// VICE deployments, including cluster registry management, deployment client,
// and spec building.
package coordinator

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/vicetypes"
	"github.com/jmoiron/sqlx"
)

var log = common.Log

// ClusterRegistry manages the registry of deployer clusters with hot-reload
// support. It maintains HTTP clients with appropriate TLS configurations
// for each cluster.
type ClusterRegistry struct {
	mu sync.RWMutex

	// clusters maps cluster ID to cluster configuration
	clusters map[string]*vicetypes.ClusterInfo

	// httpClients maps cluster ID to configured HTTP client
	httpClients map[string]*http.Client

	// db is the database connection for loading cluster configs
	db *sqlx.DB

	// encryptor handles encryption/decryption of client private keys
	encryptor *Encryptor

	// pollInterval is how often to check for cluster config changes
	pollInterval time.Duration

	// stopChan signals the polling goroutine to stop
	stopChan chan struct{}
}

// NewClusterRegistry creates a new ClusterRegistry.
// If encryptionKey is nil or empty, encryption will be disabled (keys stored as-is).
// For production use, provide a 32-byte key for AES-256 encryption.
func NewClusterRegistry(db *sqlx.DB, encryptionKey []byte, pollInterval time.Duration) *ClusterRegistry {
	var encryptor *Encryptor
	if len(encryptionKey) > 0 {
		var err error
		encryptor, err = NewEncryptor(encryptionKey)
		if err != nil {
			log.Warnf("failed to create encryptor: %v - encryption disabled", err)
		}
	} else {
		log.Warn("no encryption key provided - client keys will be stored unencrypted")
	}

	return &ClusterRegistry{
		clusters:     make(map[string]*vicetypes.ClusterInfo),
		httpClients:  make(map[string]*http.Client),
		db:           db,
		encryptor:    encryptor,
		pollInterval: pollInterval,
		stopChan:     make(chan struct{}),
	}
}

// Start begins the background polling for cluster configuration changes.
func (r *ClusterRegistry) Start(ctx context.Context) error {
	// Initial load
	if err := r.Reload(ctx); err != nil {
		return fmt.Errorf("initial cluster load failed: %w", err)
	}

	// Start background polling
	go r.pollLoop(ctx)

	return nil
}

// Stop stops the background polling.
func (r *ClusterRegistry) Stop() {
	close(r.stopChan)
}

// pollLoop periodically reloads cluster configurations.
func (r *ClusterRegistry) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := r.Reload(ctx); err != nil {
				log.Errorf("failed to reload clusters: %v", err)
			}
		case <-r.stopChan:
			log.Info("cluster registry polling stopped")
			return
		case <-ctx.Done():
			log.Info("cluster registry polling cancelled")
			return
		}
	}
}

// Reload reloads all cluster configurations from the database.
func (r *ClusterRegistry) Reload(ctx context.Context) error {
	log.Debug("reloading cluster configurations")

	clusters, err := r.loadClustersFromDB(ctx)
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Track which clusters we've seen
	seenIDs := make(map[string]bool)

	for _, cluster := range clusters {
		seenIDs[cluster.ID] = true

		// Check if cluster config changed
		existing, exists := r.clusters[cluster.ID]
		if exists && !r.clusterChanged(existing, cluster) {
			continue
		}

		// Update or add cluster
		r.clusters[cluster.ID] = cluster

		// Rebuild HTTP client for this cluster
		client, err := r.buildHTTPClient(cluster)
		if err != nil {
			log.Errorf("failed to build HTTP client for cluster %s: %v", cluster.Name, err)
			continue
		}
		r.httpClients[cluster.ID] = client
		log.Infof("updated cluster configuration for %s", cluster.Name)
	}

	// Remove clusters that no longer exist
	for id := range r.clusters {
		if !seenIDs[id] {
			delete(r.clusters, id)
			delete(r.httpClients, id)
			log.Infof("removed cluster %s", id)
		}
	}

	log.Debugf("cluster registry now has %d clusters", len(r.clusters))
	return nil
}

// loadClustersFromDB loads cluster configurations from the database.
func (r *ClusterRegistry) loadClustersFromDB(ctx context.Context) ([]*vicetypes.ClusterInfo, error) {
	const query = `
		SELECT id, name, deployer_url, enabled, priority, mtls_enabled,
		       ca_cert, client_cert, client_key_encrypted,
		       created_at, updated_at
		FROM vice_clusters
		ORDER BY priority ASC, name ASC
	`

	var clusters []*vicetypes.ClusterInfo
	if err := r.db.SelectContext(ctx, &clusters, query); err != nil {
		return nil, fmt.Errorf("failed to query clusters: %w", err)
	}

	return clusters, nil
}

// clusterChanged checks if the cluster configuration has changed.
func (r *ClusterRegistry) clusterChanged(old, new *vicetypes.ClusterInfo) bool {
	if old.DeployerURL != new.DeployerURL {
		return true
	}
	if old.MTLSEnabled != new.MTLSEnabled {
		return true
	}
	if old.Enabled != new.Enabled {
		return true
	}
	if old.Priority != new.Priority {
		return true
	}
	// Check if certificates changed
	if (old.CACert == nil) != (new.CACert == nil) {
		return true
	}
	if old.CACert != nil && *old.CACert != *new.CACert {
		return true
	}
	if (old.ClientCert == nil) != (new.ClientCert == nil) {
		return true
	}
	if old.ClientCert != nil && *old.ClientCert != *new.ClientCert {
		return true
	}
	// Note: We don't compare encrypted keys directly, just their presence
	if (old.ClientKeyEncrypted == nil) != (new.ClientKeyEncrypted == nil) {
		return true
	}
	return false
}

// buildHTTPClient creates an HTTP client for the given cluster.
func (r *ClusterRegistry) buildHTTPClient(cluster *vicetypes.ClusterInfo) (*http.Client, error) {
	transport := &http.Transport{
		MaxIdleConns:        10,
		IdleConnTimeout:     30 * time.Second,
		DisableCompression:  true,
		MaxIdleConnsPerHost: 5,
	}

	if cluster.MTLSEnabled {
		tlsConfig, err := r.buildTLSConfig(cluster)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsConfig
	}

	return &http.Client{
		Transport: transport,
		Timeout:   60 * time.Second,
	}, nil
}

// buildTLSConfig creates a TLS configuration for mTLS with the cluster.
func (r *ClusterRegistry) buildTLSConfig(cluster *vicetypes.ClusterInfo) (*tls.Config, error) {
	if cluster.CACert == nil || cluster.ClientCert == nil || cluster.ClientKeyEncrypted == nil {
		return nil, fmt.Errorf("mTLS enabled but certificates not configured for cluster %s", cluster.Name)
	}

	// Decrypt the client key
	clientKey, err := r.decryptKey(cluster.ClientKeyEncrypted)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt client key: %w", err)
	}

	// Load client certificate
	cert, err := tls.X509KeyPair([]byte(*cluster.ClientCert), clientKey)
	if err != nil {
		return nil, fmt.Errorf("failed to load client certificate: %w", err)
	}

	// Load CA certificate
	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM([]byte(*cluster.CACert)) {
		return nil, fmt.Errorf("failed to parse CA certificate")
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// decryptKey decrypts a client private key.
// If no encryptor is configured, returns the key as-is (for backwards compatibility).
func (r *ClusterRegistry) decryptKey(encrypted []byte) ([]byte, error) {
	if r.encryptor == nil {
		// No encryption configured, return as-is
		return encrypted, nil
	}
	return r.encryptor.Decrypt(encrypted)
}

// GetCluster returns the cluster configuration for the given ID.
func (r *ClusterRegistry) GetCluster(id string) (*vicetypes.ClusterInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cluster, ok := r.clusters[id]
	return cluster, ok
}

// GetClusterByName returns the cluster configuration for the given name.
func (r *ClusterRegistry) GetClusterByName(name string) (*vicetypes.ClusterInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, cluster := range r.clusters {
		if cluster.Name == name {
			return cluster, true
		}
	}
	return nil, false
}

// GetHTTPClient returns the HTTP client for the given cluster ID.
func (r *ClusterRegistry) GetHTTPClient(id string) (*http.Client, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	client, ok := r.httpClients[id]
	return client, ok
}

// ListClusters returns all registered clusters.
func (r *ClusterRegistry) ListClusters() []*vicetypes.ClusterInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	clusters := make([]*vicetypes.ClusterInfo, 0, len(r.clusters))
	for _, cluster := range r.clusters {
		clusters = append(clusters, cluster)
	}
	return clusters
}

// ListEnabledClusters returns only enabled clusters, sorted by priority.
func (r *ClusterRegistry) ListEnabledClusters() []*vicetypes.ClusterInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var enabled []*vicetypes.ClusterInfo
	for _, cluster := range r.clusters {
		if cluster.Enabled {
			enabled = append(enabled, cluster)
		}
	}

	// Sort by priority (lower = higher priority)
	// Already sorted from DB query, but keeping explicit for clarity
	return enabled
}

// RegisterCluster adds or updates a cluster in the database.
func (r *ClusterRegistry) RegisterCluster(ctx context.Context, req *vicetypes.ClusterRegistrationRequest) (*vicetypes.ClusterInfo, error) {
	var encryptedKey []byte
	if req.ClientKey != "" {
		// Encrypt the client key before storing
		var err error
		encryptedKey, err = r.encryptKey([]byte(req.ClientKey))
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt client key: %w", err)
		}
	}

	const query = `
		INSERT INTO vice_clusters (name, deployer_url, enabled, priority, mtls_enabled, ca_cert, client_cert, client_key_encrypted)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (name) DO UPDATE SET
			deployer_url = EXCLUDED.deployer_url,
			enabled = EXCLUDED.enabled,
			priority = EXCLUDED.priority,
			mtls_enabled = EXCLUDED.mtls_enabled,
			ca_cert = EXCLUDED.ca_cert,
			client_cert = EXCLUDED.client_cert,
			client_key_encrypted = EXCLUDED.client_key_encrypted,
			updated_at = now()
		RETURNING id, name, deployer_url, enabled, priority, mtls_enabled, ca_cert, client_cert, created_at, updated_at
	`

	var caCert, clientCert *string
	if req.CACert != "" {
		caCert = &req.CACert
	}
	if req.ClientCert != "" {
		clientCert = &req.ClientCert
	}

	cluster := &vicetypes.ClusterInfo{}
	err := r.db.QueryRowContext(ctx, query,
		req.Name, req.DeployerURL, req.Enabled, req.Priority, req.MTLSEnabled,
		caCert, clientCert, encryptedKey,
	).Scan(
		&cluster.ID, &cluster.Name, &cluster.DeployerURL, &cluster.Enabled,
		&cluster.Priority, &cluster.MTLSEnabled, &cluster.CACert, &cluster.ClientCert,
		&cluster.CreatedAt, &cluster.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to register cluster: %w", err)
	}

	// Trigger reload to pick up the new cluster
	if err := r.Reload(ctx); err != nil {
		log.Errorf("failed to reload clusters after registration: %v", err)
	}

	return cluster, nil
}

// UpdateCluster updates an existing cluster in the database.
func (r *ClusterRegistry) UpdateCluster(ctx context.Context, id string, req *vicetypes.ClusterUpdateRequest) (*vicetypes.ClusterInfo, error) {
	// Build dynamic update query based on which fields are provided
	// For simplicity, we'll fetch, merge, and save
	existing, ok := r.GetCluster(id)
	if !ok {
		return nil, fmt.Errorf("cluster not found: %s", id)
	}

	// Merge updates
	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.DeployerURL != nil {
		existing.DeployerURL = *req.DeployerURL
	}
	if req.Enabled != nil {
		existing.Enabled = *req.Enabled
	}
	if req.Priority != nil {
		existing.Priority = *req.Priority
	}
	if req.MTLSEnabled != nil {
		existing.MTLSEnabled = *req.MTLSEnabled
	}
	if req.CACert != nil {
		existing.CACert = req.CACert
	}
	if req.ClientCert != nil {
		existing.ClientCert = req.ClientCert
	}

	var encryptedKey []byte
	if req.ClientKey != nil {
		var err error
		encryptedKey, err = r.encryptKey([]byte(*req.ClientKey))
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt client key: %w", err)
		}
	} else {
		encryptedKey = existing.ClientKeyEncrypted
	}

	const query = `
		UPDATE vice_clusters
		SET name = $2, deployer_url = $3, enabled = $4, priority = $5,
		    mtls_enabled = $6, ca_cert = $7, client_cert = $8, client_key_encrypted = $9,
		    updated_at = now()
		WHERE id = $1
		RETURNING id, name, deployer_url, enabled, priority, mtls_enabled, ca_cert, client_cert, created_at, updated_at
	`

	cluster := &vicetypes.ClusterInfo{}
	err := r.db.QueryRowContext(ctx, query,
		id, existing.Name, existing.DeployerURL, existing.Enabled, existing.Priority,
		existing.MTLSEnabled, existing.CACert, existing.ClientCert, encryptedKey,
	).Scan(
		&cluster.ID, &cluster.Name, &cluster.DeployerURL, &cluster.Enabled,
		&cluster.Priority, &cluster.MTLSEnabled, &cluster.CACert, &cluster.ClientCert,
		&cluster.CreatedAt, &cluster.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to update cluster: %w", err)
	}

	// Trigger reload
	if err := r.Reload(ctx); err != nil {
		log.Errorf("failed to reload clusters after update: %v", err)
	}

	return cluster, nil
}

// DeleteCluster removes a cluster from the database.
func (r *ClusterRegistry) DeleteCluster(ctx context.Context, id string) error {
	const query = `DELETE FROM vice_clusters WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete cluster: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("cluster not found: %s", id)
	}

	// Trigger reload
	if err := r.Reload(ctx); err != nil {
		log.Errorf("failed to reload clusters after deletion: %v", err)
	}

	return nil
}

// EnableCluster enables a cluster for new deployments.
func (r *ClusterRegistry) EnableCluster(ctx context.Context, id string) error {
	const query = `UPDATE vice_clusters SET enabled = true, updated_at = now() WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to enable cluster: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("cluster not found: %s", id)
	}

	// Trigger reload
	if err := r.Reload(ctx); err != nil {
		log.Errorf("failed to reload clusters after enable: %v", err)
	}

	return nil
}

// DisableCluster disables a cluster (no new deployments).
func (r *ClusterRegistry) DisableCluster(ctx context.Context, id string) error {
	const query = `UPDATE vice_clusters SET enabled = false, updated_at = now() WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to disable cluster: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}
	if rowsAffected == 0 {
		return fmt.Errorf("cluster not found: %s", id)
	}

	// Trigger reload
	if err := r.Reload(ctx); err != nil {
		log.Errorf("failed to reload clusters after disable: %v", err)
	}

	return nil
}

// encryptKey encrypts a client private key.
// If no encryptor is configured, returns the key as-is (for backwards compatibility).
func (r *ClusterRegistry) encryptKey(key []byte) ([]byte, error) {
	if r.encryptor == nil {
		// No encryption configured, store as-is
		return key, nil
	}
	return r.encryptor.Encrypt(key)
}
