package vicetypes

import "time"

// ClusterInfo describes a remote cluster for deployment.
// This is used by the coordinator to manage deployer endpoints.
type ClusterInfo struct {
	// ID is the unique identifier for the cluster
	ID string `json:"id" db:"id"`

	// Name is the human-readable cluster name (unique)
	Name string `json:"name" db:"name"`

	// DeployerURL is the URL of the deployer service
	DeployerURL string `json:"deployer_url" db:"deployer_url"`

	// Enabled indicates whether this cluster accepts new deployments
	Enabled bool `json:"enabled" db:"enabled"`

	// Priority is the selection priority (lower = higher priority)
	Priority int `json:"priority" db:"priority"`

	// MTLSEnabled indicates whether mTLS is required for this cluster
	MTLSEnabled bool `json:"mtls_enabled" db:"mtls_enabled"`

	// CACert is the PEM-encoded CA certificate (nil if no mTLS)
	CACert *string `json:"ca_cert,omitempty" db:"ca_cert"`

	// ClientCert is the PEM-encoded client certificate (nil if no mTLS)
	ClientCert *string `json:"client_cert,omitempty" db:"client_cert"`

	// ClientKeyEncrypted is the encrypted client private key (nil if no mTLS)
	// Note: This field is not exposed via JSON for security
	ClientKeyEncrypted []byte `json:"-" db:"client_key_encrypted"`

	// CreatedAt is when the cluster was registered
	CreatedAt time.Time `json:"created_at" db:"created_at"`

	// UpdatedAt is when the cluster was last updated
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// ClusterRegistrationRequest is sent to register a new cluster.
type ClusterRegistrationRequest struct {
	// Name is the human-readable cluster name (required, unique)
	Name string `json:"name"`

	// DeployerURL is the URL of the deployer service (required)
	DeployerURL string `json:"deployer_url"`

	// Enabled indicates whether this cluster accepts new deployments
	Enabled bool `json:"enabled"`

	// Priority is the selection priority (lower = higher priority)
	Priority int `json:"priority"`

	// MTLSEnabled indicates whether mTLS is required for this cluster
	MTLSEnabled bool `json:"mtls_enabled"`

	// CACert is the PEM-encoded CA certificate (required if mTLS enabled)
	CACert string `json:"ca_cert,omitempty"`

	// ClientCert is the PEM-encoded client certificate (required if mTLS enabled)
	ClientCert string `json:"client_cert,omitempty"`

	// ClientKey is the PEM-encoded client private key (required if mTLS enabled)
	// This will be encrypted before storage
	ClientKey string `json:"client_key,omitempty"`
}

// ClusterUpdateRequest is sent to update a cluster's configuration.
type ClusterUpdateRequest struct {
	// Name is the human-readable cluster name (optional)
	Name *string `json:"name,omitempty"`

	// DeployerURL is the URL of the deployer service (optional)
	DeployerURL *string `json:"deployer_url,omitempty"`

	// Enabled indicates whether this cluster accepts new deployments (optional)
	Enabled *bool `json:"enabled,omitempty"`

	// Priority is the selection priority (optional)
	Priority *int `json:"priority,omitempty"`

	// MTLSEnabled indicates whether mTLS is required (optional)
	MTLSEnabled *bool `json:"mtls_enabled,omitempty"`

	// CACert is the PEM-encoded CA certificate (optional)
	CACert *string `json:"ca_cert,omitempty"`

	// ClientCert is the PEM-encoded client certificate (optional)
	ClientCert *string `json:"client_cert,omitempty"`

	// ClientKey is the PEM-encoded client private key (optional)
	ClientKey *string `json:"client_key,omitempty"`
}

// ClusterResponse is returned after cluster operations.
type ClusterResponse struct {
	// ID is the cluster identifier
	ID string `json:"id"`

	// Name is the human-readable cluster name
	Name string `json:"name"`

	// DeployerURL is the URL of the deployer service
	DeployerURL string `json:"deployer_url"`

	// Enabled indicates whether this cluster accepts new deployments
	Enabled bool `json:"enabled"`

	// Priority is the selection priority
	Priority int `json:"priority"`

	// MTLSEnabled indicates whether mTLS is configured
	MTLSEnabled bool `json:"mtls_enabled"`

	// Status is the operational status ("registered", "healthy", "unreachable", "auth_failed")
	Status string `json:"status,omitempty"`

	// LastHealthCheck is when the cluster was last checked
	LastHealthCheck *time.Time `json:"last_health_check,omitempty"`

	// CreatedAt is when the cluster was registered
	CreatedAt time.Time `json:"created_at"`

	// UpdatedAt is when the cluster was last updated
	UpdatedAt time.Time `json:"updated_at"`
}

// ClusterListResponse is returned when listing clusters.
type ClusterListResponse struct {
	// Clusters is the list of registered clusters
	Clusters []ClusterResponse `json:"clusters"`

	// Total is the total number of clusters
	Total int `json:"total"`
}

// ClusterHealthStatus represents the health status of a cluster.
type ClusterHealthStatus string

const (
	// ClusterHealthy indicates the cluster is healthy and accepting deployments
	ClusterHealthy ClusterHealthStatus = "healthy"

	// ClusterUnreachable indicates the deployer cannot be reached
	ClusterUnreachable ClusterHealthStatus = "unreachable"

	// ClusterAuthFailed indicates mTLS authentication failed
	ClusterAuthFailed ClusterHealthStatus = "auth_failed"

	// ClusterDisabled indicates the cluster is disabled
	ClusterDisabled ClusterHealthStatus = "disabled"

	// ClusterUnknown indicates the health status is unknown
	ClusterUnknown ClusterHealthStatus = "unknown"
)
