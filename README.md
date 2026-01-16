app-exposer
===========

`app-exposer` is a service for the CyVerse Discovery Environment that provides a CRUD API for managing VICE analyses.

## Architecture

The app-exposer repository contains two services for multi-cluster VICE deployments:

### app-exposer (Coordinator)

The main service that runs in the primary cluster. It handles:
- Job submissions from the apps service
- Database operations (user info, analysis IDs, login IPs)
- Quota validation via NATS/QMS
- Building complete Kubernetes resource specifications
- Selecting target clusters for deployment
- Sending specs to deployers via REST API
- Tracking deployment state and publishing status updates

### vice-deployer (Deployer)

A stateless service that runs in each cluster (including the main cluster). It handles:
- Accepting deployment specs via REST API
- Creating Kubernetes resources (Deployments, Services, Ingresses, ConfigMaps, PVs, PVCs, PDBs)
- Deleting resources on cleanup
- Reporting deployment status on demand

The deployer can run as:
- **Standalone mode**: Long-running HTTP server for self-hosted Kubernetes clusters
- **Lambda mode**: AWS Lambda function behind API Gateway for AWS-hosted clusters

---

## Development

### Prerequisites

* `just` - A command runner. See [just](https://github.com/casey/just)
* `go` (1.25+) - The Go programming language. See [Go](https://go.dev)
* `swag` - Swagger documentation generator. See [swag](https://github.com/swaggo/swag)
* `docker` - For building container images

### Building Locally

Build all services:
```bash
just build
```

Build individual services:
```bash
just app-exposer      # Build coordinator
just deployer         # Build deployer (standalone)
just deployer-lambda  # Build deployer with Lambda support
just workflow-builder # Build workflow builder
```

Output binaries are placed in `bin/`:
- `bin/app-exposer`
- `bin/vice-deployer`
- `bin/vice-deployer-lambda` (requires AWS SDK dependencies)
- `bin/workflow-builder`

### Running Tests

```bash
just test              # Run all tests
just test-coordinator  # Test coordinator package
just test-deployer     # Test deployer package
just test-vicetypes    # Test shared types
```

### Generating Documentation

```bash
just docs  # Generate Swagger documentation
```

### Cleaning

```bash
just clean
```

---

## Docker Images

### Building Images

Build all images:
```bash
just docker-all
```

Build individual images:
```bash
# Coordinator (default target)
docker build -t app-exposer:latest .
docker build --target app-exposer -t app-exposer:latest .

# Deployer (standalone)
docker build --target deployer -t vice-deployer:latest .

# Deployer (Lambda)
docker build --target deployer-lambda -t vice-deployer-lambda:latest .
```

### Image Sizes

| Image | Size | Description |
|-------|------|-------------|
| app-exposer | ~86MB | Coordinator service |
| deployer | ~47MB | Standalone deployer |
| deployer-lambda | ~varies | AWS Lambda deployer |

---

## Deploying app-exposer (Coordinator)

### Kubernetes Deployment

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: app-exposer
spec:
  replicas: 1
  selector:
    matchLabels:
      app: app-exposer
  template:
    metadata:
      labels:
        app: app-exposer
    spec:
      containers:
      - name: app-exposer
        image: harbor.cyverse.org/de/app-exposer:latest
        ports:
        - containerPort: 60000
        args:
        - --config=/etc/cyverse/de/configs/service.yml
        volumeMounts:
        - name: config
          mountPath: /etc/cyverse/de/configs
        - name: nats-creds
          mountPath: /etc/nats/creds
      volumes:
      - name: config
        configMap:
          name: app-exposer-config
      - name: nats-creds
        secret:
          secretName: nats-services-creds
```

### Configuration

Use `example-config.yml` as a reference. Key configuration options:

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `/etc/cyverse/de/configs/service.yml` | Path to config file |
| `--port` | `60000` | HTTP port |
| `--vice-namespace` | `vice-apps` | Namespace for VICE deployments |
| `--disable-vice-proxy-auth` | `false` | Disable auth in vice-proxy sidecars |

---

## Deploying vice-deployer

### Standalone Mode (Kubernetes)

Deploy the deployer to each Kubernetes cluster where VICE apps will run:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vice-deployer
  namespace: vice-system
spec:
  replicas: 1
  selector:
    matchLabels:
      app: vice-deployer
  template:
    metadata:
      labels:
        app: vice-deployer
    spec:
      serviceAccountName: vice-deployer
      containers:
      - name: vice-deployer
        image: harbor.cyverse.org/de/vice-deployer:latest
        ports:
        - containerPort: 8080
        args:
        - --namespace=vice-apps
        - --port=8080
        - --log-level=info
---
apiVersion: v1
kind: Service
metadata:
  name: vice-deployer
  namespace: vice-system
spec:
  selector:
    app: vice-deployer
  ports:
  - port: 8080
    targetPort: 8080
```

### Standalone Mode with mTLS

For secure communication between coordinator and deployer:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: vice-deployer
spec:
  template:
    spec:
      containers:
      - name: vice-deployer
        args:
        - --namespace=vice-apps
        - --port=8443
        - --mtls=true
        - --tls-cert=/etc/certs/tls.crt
        - --tls-key=/etc/certs/tls.key
        - --client-ca=/etc/certs/ca.crt
        volumeMounts:
        - name: certs
          mountPath: /etc/certs
          readOnly: true
      volumes:
      - name: certs
        secret:
          secretName: vice-deployer-certs
```

### RBAC Requirements

The deployer needs permissions to manage VICE resources:

```yaml
apiVersion: v1
kind: ServiceAccount
metadata:
  name: vice-deployer
  namespace: vice-system
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: vice-deployer
rules:
- apiGroups: [""]
  resources: ["configmaps", "services", "persistentvolumes", "persistentvolumeclaims", "pods", "pods/log"]
  verbs: ["get", "list", "watch", "create", "update", "delete"]
- apiGroups: ["apps"]
  resources: ["deployments"]
  verbs: ["get", "list", "watch", "create", "update", "delete"]
- apiGroups: ["networking.k8s.io"]
  resources: ["ingresses"]
  verbs: ["get", "list", "watch", "create", "update", "delete"]
- apiGroups: ["policy"]
  resources: ["poddisruptionbudgets"]
  verbs: ["get", "list", "watch", "create", "update", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: vice-deployer
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: vice-deployer
subjects:
- kind: ServiceAccount
  name: vice-deployer
  namespace: vice-system
```

### Lambda Mode (AWS)

For deploying VICE apps to AWS EKS clusters via Lambda:

1. **Build the Lambda image:**
   ```bash
   docker build --target deployer-lambda -t vice-deployer-lambda:latest .
   ```

2. **Push to ECR:**
   ```bash
   aws ecr get-login-password --region us-east-1 | docker login --username AWS --password-stdin <account>.dkr.ecr.us-east-1.amazonaws.com
   docker tag vice-deployer-lambda:latest <account>.dkr.ecr.us-east-1.amazonaws.com/vice-deployer:latest
   docker push <account>.dkr.ecr.us-east-1.amazonaws.com/vice-deployer:latest
   ```

3. **Create Lambda function:**
   ```bash
   aws lambda create-function \
     --function-name vice-deployer \
     --package-type Image \
     --code ImageUri=<account>.dkr.ecr.us-east-1.amazonaws.com/vice-deployer:latest \
     --role arn:aws:iam::<account>:role/vice-deployer-lambda-role \
     --timeout 30 \
     --memory-size 256
   ```

4. **Configure API Gateway** to route requests to the Lambda function.

5. **Store kubeconfig in Secrets Manager:**
   ```bash
   aws secretsmanager create-secret \
     --name vice-deployer/kubeconfig \
     --secret-string "$(cat ~/.kube/config)"
   ```

### Deployer Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--mode` | `standalone` | Run mode: `standalone` or `lambda` |
| `--port` | `8080` | HTTP port (standalone only) |
| `--namespace` | `vice-apps` | Default K8s namespace |
| `--kubeconfig` | `` | Path to kubeconfig (uses in-cluster by default) |
| `--mtls` | `false` | Enable mTLS for incoming connections |
| `--tls-cert` | `` | TLS certificate path (required if mtls enabled) |
| `--tls-key` | `` | TLS private key path (required if mtls enabled) |
| `--client-ca` | `` | Client CA certificate path (required if mtls enabled) |
| `--log-level` | `info` | Log level: debug, info, warn, error |

---

## API Reference

### Deployer API Endpoints

| Method | Endpoint | Description |
|--------|----------|-------------|
| `POST` | `/api/v1/deployments` | Create deployment from spec |
| `DELETE` | `/api/v1/deployments/{external_id}` | Delete all resources for deployment |
| `GET` | `/api/v1/deployments/{external_id}/status` | Get deployment status |
| `GET` | `/api/v1/deployments/{external_id}/url-ready` | Check if deployment is ready |
| `GET` | `/api/v1/deployments/{external_id}/logs` | Get pod logs |
| `GET` | `/api/v1/health` | Health check |

### Cluster Management API (Coordinator)

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/v1/clusters` | List all registered clusters |
| `POST` | `/api/v1/clusters` | Register a new cluster |
| `GET` | `/api/v1/clusters/{id}` | Get cluster details |
| `PUT` | `/api/v1/clusters/{id}` | Update cluster configuration |
| `DELETE` | `/api/v1/clusters/{id}` | Remove cluster from registry |
| `POST` | `/api/v1/clusters/{id}/enable` | Enable cluster for deployments |
| `POST` | `/api/v1/clusters/{id}/disable` | Disable cluster |
| `POST` | `/api/v1/clusters/reload` | Force reload cluster configs |

---

## Database Schema

The multi-cluster feature requires database migrations. Apply these to your DE database:

```sql
-- See migrations/000046_vice_clusters.up.sql
CREATE TABLE IF NOT EXISTS vice_clusters (
    id uuid NOT NULL DEFAULT uuid_generate_v1(),
    name text NOT NULL UNIQUE,
    deployer_url text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    priority integer NOT NULL DEFAULT 100,
    mtls_enabled boolean NOT NULL DEFAULT false,
    ca_cert text,
    client_cert text,
    client_key_encrypted bytea,
    created_at timestamp NOT NULL DEFAULT now(),
    updated_at timestamp NOT NULL DEFAULT now(),
    PRIMARY KEY (id)
);

-- Track which cluster runs each job
ALTER TABLE jobs ADD COLUMN IF NOT EXISTS cluster_id uuid REFERENCES vice_clusters(id);
```

---

## Certificate Management

For mTLS between coordinator and deployers, you have several options:

### Option 1: cert-manager (Recommended)

Use cert-manager with a self-signed or internal CA:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: vice-deployer-server
spec:
  secretName: vice-deployer-certs
  issuerRef:
    name: internal-ca
    kind: ClusterIssuer
  dnsNames:
  - vice-deployer.vice-system.svc
  - vice-deployer.cluster-2.example.com
```

### Option 2: Manual Certificates

Generate certificates using OpenSSL:

```bash
# Generate CA
openssl genrsa -out ca.key 4096
openssl req -x509 -new -nodes -key ca.key -sha256 -days 3650 -out ca.crt

# Generate server certificate for deployer
openssl genrsa -out server.key 2048
openssl req -new -key server.key -out server.csr
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out server.crt -days 365

# Generate client certificate for coordinator
openssl genrsa -out client.key 2048
openssl req -new -key client.key -out client.csr
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out client.crt -days 365
```

---

## Troubleshooting

### Deployer can't connect to Kubernetes API

- Verify service account has correct RBAC permissions
- Check if running in-cluster or needs explicit kubeconfig
- For Lambda mode, ensure Secrets Manager contains valid kubeconfig

### mTLS connection failures

- Verify certificates are not expired
- Ensure client certificate is signed by the CA the server trusts
- Check that hostnames in certificates match actual endpoints

### Deployments not appearing

- Check deployer logs for errors
- Verify namespace exists and deployer has permissions
- Check if resource quotas are blocking creation

---

## Legacy Documentation

### Initializing Swagger docs

```bash
swag init --parseDependency -g app.go -d cmd/app-exposer/,httphandlers/,common/,incluster/,coordinator/,deployer/,vicetypes/
```

### API Documentation (OpenAPI)

The API documentation is in `api.yml`. View locally with redoc:

```bash
npm install -g redoc-cli
redoc-cli serve -w api.yml
```
