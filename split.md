# Plan: Splitting app-exposer for Multi-Cluster VICE Deployment

## Overview

Split app-exposer into two services:
1. **Coordinator** - Runs in main cluster, handles job submission, database ops, quota validation, orchestrates deployments
2. **Deployer** - Runs in each cluster (including main), stateless, creates K8s resources from pre-assembled specs

Batch jobs remain unchanged (Argo Workflows stays in current architecture).

---

## Service Boundaries

### Coordinator (Main Cluster)
**Responsibilities:**
- Receive job submissions from apps service (`/vice/launch`)
- Query DE database (user info, analysis IDs, login IPs)
- Validate quotas via NATS/QMS
- Calculate and store millicores
- Build complete K8s resource specs (Deployments, Services, Ingresses, etc.)
- Select target cluster for deployment
- Send specs to appropriate Deployer via REST API
- Track deployment state, publish status to job-status-listener
- Handle time limits, exit/cleanup coordination

**Does NOT:** Create K8s resources directly, access remote cluster APIs

### Deployer (Each Cluster)
**Responsibilities:**
- Accept deployment specs via REST API
- Create K8s resources: Deployments, Services, Ingresses, ConfigMaps, PVs, PVCs, PDBs
- Delete resources on cleanup
- Report status on demand

**Does NOT:** Access DE database, validate quotas, track analysis state, decide where to deploy

**Deployment Modes:**
- **Standalone service** (default): Long-running HTTP server for self-hosted K8s clusters
- **AWS Lambda**: Serverless function behind API Gateway for AWS-hosted clusters

```
# Standalone mode (default)
./deployer --mode=standalone --port=8443

# Lambda mode
./deployer --mode=lambda
```

---

## API Contract (Deployer Endpoints)

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/api/v1/deployments` | POST | Create deployment from spec |
| `/api/v1/deployments/{external_id}` | DELETE | Delete all resources |
| `/api/v1/deployments/{external_id}/status` | GET | Get current deployment status |
| `/api/v1/deployments/{external_id}/url-ready` | GET | Check if ready to serve |
| `/api/v1/deployments/{external_id}/logs` | GET | Get pod logs |
| `/api/v1/deployments/{external_id}/file-transfers/{type}` | POST | Trigger file transfer |
| `/api/v1/health` | GET | Health check |

**Request body for POST /deployments:**
```json
{
  "metadata": { "external_id", "analysis_id", "user_id", "username", ... },
  "deployment": { /* Full K8s Deployment spec */ },
  "service": { /* Full K8s Service spec */ },
  "ingress": { /* Full K8s Ingress spec */ },
  "config_maps": [ /* ConfigMap specs */ ],
  "persistent_volumes": [ /* PV specs */ ],
  "persistent_volume_claims": [ /* PVC specs */ ],
  "pod_disruption_budget": { /* PDB spec */ }
}
```

---

## Key Code Changes

### Files to Modify/Refactor

| Current File | Changes |
|-------------|---------|
| `incluster/deployments.go` | Extract spec generation into new `coordinator/spec_builder.go` |
| `incluster/incluster.go` | Split: spec building -> coordinator, K8s ops -> deployer |
| `incluster/volumes.go` | Move PV/PVC generation to coordinator |
| `incluster/services.go`, `ingresses.go` | Move spec generation to coordinator |
| `httphandlers/launch.go` | Refactor to build spec, select cluster, call deployer |
| `httphandlers/exit.go` | Refactor to look up cluster, call deployer DELETE |
| `quota/enforcer.go` | Remove K8s client dependency, get job counts from deployers |

### New Packages to Create

| Package | Purpose |
|---------|---------|
| `vicetypes/` | Shared API types (VICEDeploymentSpec, DeploymentResponse, etc.) |
| `coordinator/spec_builder.go` | Build VICEDeploymentSpec from model.Job |
| `coordinator/deployer_client.go` | HTTP client for deployer API (with mTLS) |
| `coordinator/cluster_selector.go` | Select target cluster for deployment |
| `coordinator/cluster_registry.go` | Hot-reloadable cluster config with optional TLS certs |
| `coordinator/cluster_handlers.go` | HTTP handlers for cluster management API |
| `cmd/deployer/main.go` | Deployer entry point with mode switching (standalone/lambda) |
| `cmd/deployer/lambda.go` | AWS Lambda handler adapter |
| `cmd/deployer/standalone.go` | Standalone HTTP server setup |
| `deployer/deployer.go` | Core deployer logic (shared between modes) |
| `deployer/handlers.go` | HTTP handlers for deployer API |

### Authentication: mTLS (Optional)

mTLS between coordinator and deployers is **optional** and **disabled by default**.

**When to use mTLS:**
- Self-hosted deployers exposed directly (no API gateway)
- Additional security layer desired

**When to skip mTLS:**
- AWS Lambda behind API Gateway (API Gateway handles mTLS)
- Internal network with other auth mechanisms

**Coordinator flags:**
```
# Enable mTLS for a specific cluster (stored per-cluster in DB)
# Default: disabled (plain HTTPS or HTTP)
```

**Deployer flags:**
```
# Standalone mode with mTLS
./deployer --mode=standalone --mtls --tls-cert=/path/to/cert --tls-key=/path/to/key --client-ca=/path/to/ca

# Standalone mode without mTLS (default)
./deployer --mode=standalone --port=8080

# Lambda mode (API Gateway handles TLS)
./deployer --mode=lambda
```

When mTLS is enabled:
- Each deployer has server certificate signed by internal CA
- Coordinator has client certificate for authentication
- Configuration includes CA cert, client cert, client key per cluster

---

## AWS Lambda Deployment

For AWS-hosted EKS clusters, the deployer can run as a Lambda function behind API Gateway.

### Architecture

```
Coordinator → API Gateway (handles TLS/auth) → Lambda → EKS
```

### Implementation

The deployer uses a single binary with mode switching:

```go
// cmd/deployer/main.go
func main() {
    mode := flag.String("mode", "standalone", "Run mode: standalone or lambda")
    flag.Parse()

    // Core deployer logic is shared
    deployer := NewDeployer(k8sClient)
    handler := NewHandler(deployer)

    switch *mode {
    case "lambda":
        // Use aws-lambda-go adapter
        lambda.Start(handler.HandleLambdaRequest)
    case "standalone":
        // Standard HTTP server
        server := echo.New()
        RegisterRoutes(server, handler)
        server.Start(":8080")
    }
}
```

### Lambda Handler Adapter

```go
// cmd/deployer/lambda.go
func (h *Handler) HandleLambdaRequest(ctx context.Context, req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
    // Convert API Gateway request to standard HTTP request
    // Route to appropriate handler based on path/method
    // Return API Gateway response
}
```

### AWS Infrastructure (Terraform/CloudFormation)

```hcl
# Lambda function
resource "aws_lambda_function" "deployer" {
  function_name = "vice-deployer"
  runtime       = "provided.al2"  # Go binary
  handler       = "bootstrap"

  environment {
    variables = {
      KUBECONFIG_SECRET = aws_secretsmanager_secret.kubeconfig.arn
    }
  }

  vpc_config {
    subnet_ids         = var.private_subnet_ids
    security_group_ids = [aws_security_group.deployer.id]
  }
}

# API Gateway
resource "aws_apigatewayv2_api" "deployer" {
  name          = "vice-deployer-api"
  protocol_type = "HTTP"
}

resource "aws_apigatewayv2_integration" "deployer" {
  api_id             = aws_apigatewayv2_api.deployer.id
  integration_type   = "AWS_PROXY"
  integration_uri    = aws_lambda_function.deployer.invoke_arn
}
```

### K8s Authentication in Lambda

Lambda needs credentials to access EKS:
- **Option A**: IAM role with EKS access (recommended)
- **Option B**: Kubeconfig stored in Secrets Manager

```go
// Load kubeconfig from Secrets Manager or use IAM
func getK8sClient() kubernetes.Interface {
    if os.Getenv("AWS_LAMBDA_FUNCTION_NAME") != "" {
        // In Lambda: use IAM role or fetch from Secrets Manager
        return buildClientFromIAM()
    }
    // Standalone: use in-cluster config or local kubeconfig
    return buildClientFromConfig()
}
```

---

## Configuration Hot-Reload

The coordinator must support adding/updating/removing deployer configurations without restart.

### Reload Mechanisms

| Mechanism | Trigger | Use Case |
|-----------|---------|----------|
| **Database polling** | Timer (e.g., every 30s) | Background sync of cluster registry |
| **PostgreSQL LISTEN/NOTIFY** | Real-time DB trigger | Immediate updates when DB changes |
| **API endpoint** | `POST /api/v1/clusters/reload` | Manual trigger after changes |
| **File watcher** | fsnotify on config dir | For file-based cert storage |

**Recommended approach:** Combine database polling with API trigger for immediate reload when needed.

### Implementation: ClusterRegistry

```go
// coordinator/cluster_registry.go
type ClusterRegistry struct {
    mu        sync.RWMutex
    clusters  map[string]*ClusterConfig  // keyed by cluster ID
    tlsCache  map[string]*tls.Config     // cached TLS configs per cluster
    db        *sqlx.DB
}

type ClusterConfig struct {
    ID          string
    Name        string
    DeployerURL string
    Enabled     bool
    Priority    int
    CACert      []byte  // CA cert for verifying deployer
    ClientCert  []byte  // Client cert for authentication
    ClientKey   []byte  // Client private key (if stored in DB)
}

func (r *ClusterRegistry) Reload(ctx context.Context) error {
    // Query database for current clusters
    // Update internal map with RWMutex
    // Rebuild TLS configs for changed/new clusters
}

func (r *ClusterRegistry) GetClient(clusterID string) (*http.Client, error) {
    r.mu.RLock()
    defer r.mu.RUnlock()
    // Return http.Client with appropriate TLS config
}
```

### Dynamic TLS Configuration

Go's `tls.Config` supports dynamic certificate selection via callbacks:

```go
tlsConfig := &tls.Config{
    GetClientCertificate: func(info *tls.CertificateRequestInfo) (*tls.Certificate, error) {
        // Called for each connection - can return different certs dynamically
        return registry.GetClientCert(clusterID)
    },
    RootCAs: registry.GetCACertPool(clusterID),
}
```

This allows certificate updates without recreating HTTP clients.

---

## Certificate Management

Certificate generation/management is the responsibility of whoever deploys a new deployer. Below are recommended approaches.

### Option 1: cert-manager (Recommended)

Since you already use cert-manager, this is the natural choice.

**Setup for each deployer cluster:**

1. **Create an Issuer** (ClusterIssuer for cross-namespace, or Issuer per namespace):

```yaml
# Using a shared CA - create CA secret first, or use existing PKI
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: vice-deployer-ca-issuer
spec:
  ca:
    secretName: vice-deployer-ca  # Contains ca.crt and ca.key
```

2. **Create Certificate for the deployer:**

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: vice-deployer-cert
  namespace: vice-system
spec:
  secretName: vice-deployer-tls
  duration: 8760h    # 1 year
  renewBefore: 720h  # 30 days
  subject:
    organizations:
      - CyVerse
  commonName: vice-deployer.example.com
  dnsNames:
    - vice-deployer.vice-system.svc
    - vice-deployer.vice-system.svc.cluster.local
    - deployer.remote-cluster.example.com  # External DNS if applicable
  issuerRef:
    name: vice-deployer-ca-issuer
    kind: ClusterIssuer
  usages:
    - server auth
```

3. **Create client Certificate for coordinator:**

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: vice-coordinator-client-cert
  namespace: vice-system
spec:
  secretName: vice-coordinator-client-tls
  duration: 8760h
  renewBefore: 720h
  subject:
    organizations:
      - CyVerse
  commonName: vice-coordinator
  issuerRef:
    name: vice-deployer-ca-issuer
    kind: ClusterIssuer
  usages:
    - client auth
```

4. **Mount secrets in deployments:**

```yaml
# Deployer deployment
volumes:
  - name: tls
    secret:
      secretName: vice-deployer-tls
volumeMounts:
  - name: tls
    mountPath: /etc/vice-deployer/tls
    readOnly: true
```

**Sharing CA across clusters:**
- Export the CA cert from the issuing cluster
- Import as a Secret in remote clusters (manually or via GitOps)
- Or use a central PKI (Vault, external CA) that all clusters trust

### Option 2: Manual with OpenSSL

For simple setups or testing:

```bash
# Create CA (once)
openssl genrsa -out ca.key 4096
openssl req -x509 -new -nodes -key ca.key -sha256 -days 3650 \
  -out ca.crt -subj "/CN=VICE Deployer CA/O=CyVerse"

# Create deployer server cert
openssl genrsa -out deployer.key 2048
openssl req -new -key deployer.key -out deployer.csr \
  -subj "/CN=vice-deployer.example.com/O=CyVerse"
openssl x509 -req -in deployer.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out deployer.crt -days 365 -sha256

# Create coordinator client cert
openssl genrsa -out coordinator-client.key 2048
openssl req -new -key coordinator-client.key -out coordinator-client.csr \
  -subj "/CN=vice-coordinator/O=CyVerse"
openssl x509 -req -in coordinator-client.csr -CA ca.crt -CAkey ca.key \
  -CAcreateserial -out coordinator-client.crt -days 365 -sha256
```

### Option 3: HashiCorp Vault PKI

For organizations with Vault infrastructure:

```bash
# Enable PKI secrets engine
vault secrets enable pki
vault secrets tune -max-lease-ttl=87600h pki

# Configure CA
vault write pki/root/generate/internal \
  common_name="VICE Deployer CA" ttl=87600h

# Create role for deployer certs
vault write pki/roles/vice-deployer \
  allowed_domains="example.com,svc.cluster.local" \
  allow_subdomains=true max_ttl=8760h

# Issue certificate
vault write pki/issue/vice-deployer \
  common_name="vice-deployer.example.com"
```

---

## Coordinator Cluster Management API

New endpoints for managing deployer clusters without downtime.

### Endpoints

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/api/v1/clusters` | GET | List all registered clusters |
| `/api/v1/clusters` | POST | Register new cluster with certs |
| `/api/v1/clusters/{id}` | GET | Get cluster details |
| `/api/v1/clusters/{id}` | PUT | Update cluster config/certs |
| `/api/v1/clusters/{id}` | DELETE | Remove cluster |
| `/api/v1/clusters/{id}/enable` | POST | Enable cluster for deployments |
| `/api/v1/clusters/{id}/disable` | POST | Disable cluster (no new deployments) |
| `/api/v1/clusters/reload` | POST | Force reload from database |

### Register/Update Cluster Request

**Without mTLS (AWS Lambda/API Gateway):**
```
POST /api/v1/clusters
Content-Type: application/json

{
  "name": "aws-eks-cluster",
  "deployer_url": "https://abc123.execute-api.us-east-1.amazonaws.com/prod",
  "enabled": true,
  "priority": 50,
  "mtls_enabled": false
}
```

**With mTLS (self-hosted):**
```
POST /api/v1/clusters
Content-Type: application/json

{
  "name": "gpu-cluster-east",
  "deployer_url": "https://deployer.gpu-east.example.com:8443",
  "enabled": true,
  "priority": 50,
  "mtls_enabled": true,
  "ca_cert": "-----BEGIN CERTIFICATE-----\n...",
  "client_cert": "-----BEGIN CERTIFICATE-----\n...",
  "client_key": "-----BEGIN RSA PRIVATE KEY-----\n..."
}
```

**Response:**
```json
{
  "id": "uuid",
  "name": "gpu-cluster-east",
  "deployer_url": "https://deployer.gpu-east.example.com:8443",
  "enabled": true,
  "priority": 50,
  "mtls_enabled": true,
  "status": "registered"
}
```

### Update Certificates Only

```
PUT /api/v1/clusters/{id}
Content-Type: application/json

{
  "ca_cert": "-----BEGIN CERTIFICATE-----\n...",
  "client_cert": "-----BEGIN CERTIFICATE-----\n...",
  "client_key": "-----BEGIN RSA PRIVATE KEY-----\n..."
}
```

### Certificate Storage Options

**Option A: Database storage (simpler)**
- Store PEM-encoded certs in `vice_clusters` table
- Single source of truth
- Easy backup/restore with database
- Private keys encrypted at rest (application-level or DB-level encryption)

**Option B: File-based storage**
- Database stores cluster metadata only
- Certs stored at `/etc/vice-coordinator/clusters/{cluster-id}/`
- API endpoint writes files and triggers reload
- Better for large certs or HSM integration

**Option C: Hybrid**
- Public certs (CA, client cert) in database
- Private keys in Kubernetes Secrets or Vault
- API accepts key reference instead of raw key

### Database Changes

```sql
-- Extended cluster registry with optional cert storage
CREATE TABLE vice_clusters (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name VARCHAR(255) NOT NULL UNIQUE,
    deployer_url VARCHAR(512) NOT NULL,
    enabled BOOLEAN DEFAULT true,
    priority INTEGER DEFAULT 100,
    mtls_enabled BOOLEAN DEFAULT false,    -- Whether to use mTLS for this cluster
    ca_cert TEXT,                          -- PEM-encoded CA certificate (NULL if no mTLS)
    client_cert TEXT,                      -- PEM-encoded client certificate (NULL if no mTLS)
    client_key_encrypted BYTEA,            -- Encrypted client private key (NULL if no mTLS)
    created_at TIMESTAMP DEFAULT NOW(),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- Index for enabled clusters lookup
CREATE INDEX idx_vice_clusters_enabled ON vice_clusters(enabled) WHERE enabled = true;

-- Trigger to update timestamp
CREATE OR REPLACE FUNCTION update_vice_clusters_timestamp()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER vice_clusters_updated
    BEFORE UPDATE ON vice_clusters
    FOR EACH ROW EXECUTE FUNCTION update_vice_clusters_timestamp();
```

---

## Adding a New Deployer: Operational Workflow

### Step 1: Deploy the Deployer Service

In the new cluster:
```bash
# Apply deployer manifests (Helm chart or raw YAML)
kubectl apply -f deployer/k8s/
```

### Step 2: Generate/Obtain Certificates

Using cert-manager:
```bash
kubectl apply -f - <<EOF
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: vice-deployer-cert
  namespace: vice-system
spec:
  secretName: vice-deployer-tls
  issuerRef:
    name: vice-deployer-ca-issuer
    kind: ClusterIssuer
  # ... (see cert-manager section above)
EOF
```

### Step 3: Export Certificates

```bash
# Get the CA cert (same across all clusters)
kubectl get secret vice-deployer-ca -n cert-manager -o jsonpath='{.data.ca\.crt}' | base64 -d > ca.crt

# Get client cert/key for coordinator
kubectl get secret vice-coordinator-client-tls -n vice-system -o jsonpath='{.data.tls\.crt}' | base64 -d > client.crt
kubectl get secret vice-coordinator-client-tls -n vice-system -o jsonpath='{.data.tls\.key}' | base64 -d > client.key
```

### Step 4: Register with Coordinator

```bash
curl -X POST https://coordinator.example.com/api/v1/clusters \
  -H "Content-Type: application/json" \
  -d @- <<EOF
{
  "name": "new-cluster",
  "deployer_url": "https://deployer.new-cluster.example.com:8443",
  "enabled": false,
  "priority": 100,
  "ca_cert": "$(cat ca.crt)",
  "client_cert": "$(cat client.crt)",
  "client_key": "$(cat client.key)"
}
EOF
```

### Step 5: Test Connectivity

```bash
# Coordinator will test connection when cluster is registered
# Check cluster status
curl https://coordinator.example.com/api/v1/clusters/{id}

# Response includes connectivity status
{
  "id": "...",
  "name": "new-cluster",
  "status": "healthy",  # or "unreachable", "auth_failed"
  "last_health_check": "2024-01-15T10:30:00Z"
}
```

### Step 6: Enable for Traffic

```bash
curl -X POST https://coordinator.example.com/api/v1/clusters/{id}/enable
```

```sql
-- Track which cluster has each job (add to existing jobs table)
ALTER TABLE jobs ADD COLUMN cluster_id UUID REFERENCES vice_clusters(id);
```

---

## Implementation Phases

### Phase 1: Prepare Codebase
- Create `vicetypes` package with shared types
- Refactor `incluster` to separate spec generation from K8s operations
- Add database schema changes (nullable cluster_id initially)

### Phase 2: Build Deployer Service
- Create deployer with REST API
- Port K8s resource creation logic from `incluster`
- Implement status/health endpoints
- Deploy deployer to main cluster

### Phase 3: Build Coordinator Logic
- Create `coordinator` package (SpecBuilder, DeployerClient, ClusterSelector)
- Create new launch/exit handlers that use deployer
- Feature flag for gradual rollout

### Phase 4: Parallel Operation & Cutover
- Test with subset of users
- Enable for all traffic
- Deploy second cluster deployer
- Test multi-cluster routing

### Phase 5: Cleanup
- Remove old code paths
- Document new architecture

---

## Design Decisions

| Question | Decision | Rationale |
|----------|----------|-----------|
| What to send to deployer? | Full K8s manifests as JSON | Deployer stays stateless, all logic in coordinator |
| How to coordinate cleanup? | Coordinator looks up cluster, calls deployer DELETE | Database is source of truth |
| How to select clusters? | Database registry + pluggable selector | Start simple, enhance later |
| Monitor remote deployments? | On-demand polling via deployer API | Simple, no persistent connections |
| Where to create ingresses? | Each deployer creates local ingresses | Cluster-local ingress controllers |
| Repository structure? | New `cmd/deployer/` in app-exposer | Easier code sharing (vicetypes, constants) |
| Authentication? | mTLS optional (disabled by default) | AWS API Gateway handles auth; self-hosted can enable |
| Deployer runtime? | Standalone (default) or AWS Lambda mode | Support both self-hosted K8s and AWS EKS |
| Config reload? | DB polling + API trigger for immediate reload | No downtime, immediate when needed |
| Cert management? | cert-manager recommended, manual/Vault supported | Leverage existing infrastructure |
| Cert storage? | Database (certs + encrypted keys) | Single source of truth, easy backup |
| Adding clusters? | REST API to register with certs, enable when ready | Zero downtime onboarding |

---

## Critical Files

1. `incluster/deployments.go:394` - `GetDeployment()` - core spec generation to extract
2. `httphandlers/launch.go:29` - `LaunchAppHandler()` - refactor for coordinator pattern
3. `incluster/incluster.go:179` - `UpsertDeployment()` - split between coordinator/deployer
4. `incluster/volumes.go:149` - `getPersistentVolumes()` - move to spec builder
5. `quota/enforcer.go:69` - `countJobsForUser()` - remove direct K8s dependency

---

## Testing Strategy

- **Unit tests:** Spec builder, deployer client, K8s operations with fake client
- **Integration tests:** Coordinator -> Deployer with fake K8s
- **E2E tests:** Full flow in Kind/Minikube cluster
- **Contract tests:** OpenAPI spec validation

---

## Verification

1. Deploy deployer to test cluster
2. Use coordinator to submit VICE job
3. Verify all K8s resources created correctly
4. Verify status endpoint returns accurate state
5. Test exit/cleanup flow
6. Test with second cluster
