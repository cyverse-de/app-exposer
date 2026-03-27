app-exposer
===========

`app-exposer` is a service for the CyVerse Discovery Environment that provides a CRUD API for managing VICE analyses.

This repository also contains `vice-operator`, a lightweight K8s operator that receives pre-built resource bundles from app-exposer and applies them to a local cluster. See [vice-operator](#vice-operator) below.

# Development

## Prerequisites

* `just` - A command runner, similar to but different from Make. See [just](https://github.com/casey/just) for more info.
* `go` - The Go programming language. See [Go](https://go.dev) for more info.
* `swag` - A Swagger 2.0 documentation generator for Go. See [swag](https://github.com/swaggo/swag) for more info.

## Build

You will need [just](https://github.com/casey/just) installed, along with the Go programming language and the bash shell.

To build `app-exposer` alone, run:
```bash
just app-exposer
```

To build `vice-operator` alone, run:
```bash
just vice-operator
```

To build `workflow-builder` alone, run:
```bash
just workflow-builder
```

To build everything, run:
```bash
just
```

To clean your local repo, run:
```bash
just clean
```

Uses Go modules, so make sure you have a generally up-to-date version of Go installed locally.

## Tests

Run all tests:
```bash
just test
```

Run operator-specific tests:
```bash
just test-operator
just test-operatorclient
```

The API documentation is written using the OpenAPI 3 specification and is located in `api.yml`. You can use redoc to view the documentation locally:

Install:
```npm install -g redoc-cli```

Run:
```redoc-cli serve -w api.yml```

For configuration, use `example-config.yml` as a reference. You'll need to either port-forward to or run `job-status-listener` locally and reference the correct port in the config.

## Command-Line Flags (app-exposer)

### Authentication Control

`--disable-vice-proxy-auth` (default: `false`)

Disables authentication in the vice-proxy sidecar containers for VICE applications. When set to `true`, the `--disable-auth` flag is passed to vice-proxy, allowing unauthenticated access to VICE applications. This is intended for development, testing, or scenarios where authentication is handled elsewhere. In production environments, this should remain `false` (the default) to enforce authentication via Keycloak.

## Initializing the Swagger docs with `swag`

The command used to initialize the `./docs` directory with the `swag` tool was

```bash
swag init -g app.go -d cmd/app-exposer/,httphandlers/
```

The General Info file for swag is `cmd/app-exposer/main.go`.

---

# vice-operator

`vice-operator` is a minimal Kubernetes operator that receives pre-built resource bundles from `app-exposer` and applies them to its local cluster. It enables multi-cluster VICE deployments where app-exposer centrally manages analysis lifecycle while operators handle resource application on remote clusters.

## Architecture

```
  Terrain/UI
      │
  app-exposer (QA cluster)
  ├─ DB, NATS, Permissions, Quota
  ├─ Builds K8s resources from model.Job
  ├─ Serializes into AnalysisBundle
  └─ Sends bundle to operator(s)
      │                    │
  vice-operator (QA)  vice-operator (local)
  ├─ Apply resources   ├─ Apply resources
  ├─ nginx Ingress     ├─ Transform → Tailscale Ingress
  └─ K8s API only      └─ K8s API only
```

The operator has **no external dependencies** — no database, NATS, apps service, permissions, or quota. It only needs a K8s client and an HTTP server.

App-exposer builds all K8s resource objects (Deployment, Service, Ingress, ConfigMaps, PVs, PVCs, PodDisruptionBudget) using its existing builder functions, serializes them into an `AnalysisBundle`, and sends the bundle to the operator via HTTP. The operator applies the resources to its local cluster, transforming routing as needed.

## Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--kubeconfig` | `""` (in-cluster) | Path to kubeconfig file. Empty uses in-cluster config. |
| `--namespace` | `vice-apps` | K8s namespace where VICE resources are created. |
| `--port` | `60001` | HTTP listen port. |
| `--routing-type` | `nginx` | Ingress routing type: `nginx` or `tailscale`. |
| `--ingress-class` | `nginx` | Ingress class name to set on Ingress resources. |
| `--max-analyses` | `50` | Maximum concurrent VICE analyses allowed on this cluster. |
| `--node-label-selector` | `""` | K8s label selector to filter schedulable nodes for capacity calculation. |
| `--log-level` | `info` | Log level (`debug`, `info`, `warn`, `error`, `fatal`). |

## Running Locally

```bash
just vice-operator

./bin/vice-operator \
  --kubeconfig=$HOME/.kube/local-admin.conf \
  --namespace=vice-apps \
  --port=60001 \
  --routing-type=tailscale \
  --ingress-class=tailscale \
  --max-analyses=10 \
  --log-level=debug
```

## API Endpoints

All per-analysis endpoints use `:analysis-id` (the job UUID from the DE database, available as the `analysis-id` label on all K8s resources).

| Method | Path | Description |
|--------|------|-------------|
| GET | `/` | Health check / greeting |
| GET | `/capacity` | Current cluster capacity and resource usage |
| POST | `/analyses` | Receive an AnalysisBundle, transform routing, apply all resources. Returns `409 Conflict` if at capacity. |
| DELETE | `/analyses/:analysis-id` | Delete all resources for an analysis by label |
| POST | `/analyses/:analysis-id/save-and-exit` | Trigger file upload via sidecar, then delete resources |
| GET | `/analyses/:analysis-id/status` | Resource status (deployments, pods, services, ingresses) |
| GET | `/analyses/:analysis-id/url-ready` | Check if deployment, service, and ingress are all ready |
| POST | `/analyses/:analysis-id/download-input-files` | Trigger file-transfer sidecar to download inputs |
| POST | `/analyses/:analysis-id/save-output-files` | Trigger file-transfer sidecar to upload outputs |
| GET | `/analyses/:analysis-id/pods` | Pod info for the analysis |
| GET | `/analyses/:analysis-id/logs` | Container logs (last 5 minutes) |
| GET | `/listing` | List all VICE resources in the namespace |

## Routing Transformation

When the bundle's Ingress uses a different routing type than the operator's cluster, the operator transforms the Ingress in memory before applying it:

- **nginx → nginx**: No transformation (pass through). If the ingress class differs, it is updated.
- **nginx → tailscale**: All `nginx.ingress.kubernetes.io/*` annotations are removed and the `IngressClassName` is set to the configured tailscale class.

This is a pure in-memory data transformation with no K8s API calls.

## Deploying to Kubernetes

A sample manifest is provided at `k8s/vice-operator.yml`. It includes a Deployment, Service, ServiceAccount, ClusterRole, and ClusterRoleBinding.

```bash
kubectl apply -f k8s/vice-operator.yml
```

Edit the Deployment args to match your cluster's configuration (routing type, ingress class, max analyses, etc.). For a Tailscale-routed cluster:

```yaml
args:
  - "--namespace=vice-apps"
  - "--port=60001"
  - "--routing-type=tailscale"
  - "--ingress-class=tailscale"
  - "--max-analyses=10"
```

The operator image is built from the same Dockerfile as app-exposer — the `vice-operator` binary is included alongside `app-exposer` in the container image. Override the entrypoint with `command: ["/vice-operator"]`.

## Configuring app-exposer to Use Operators

Add an `operators` list under the `vice` section in app-exposer's `jobservices.yml` config file. Operators are listed in priority order — the scheduler tries them sequentially and sends to the first one with available capacity.

```yaml
vice:
  operators:
    - name: "qa"
      url: "http://vice-operator.vice-apps.svc.cluster.local:60001"
    - name: "local-cluster"
      url: "https://vice-operator.local.ts.net:60001"
```

When no operators are configured, app-exposer applies resources directly to its local cluster as before (backward compatible).

### Scheduling Behavior

1. For each operator in config order, app-exposer queries `GET /capacity`.
2. If the operator has room (`runningAnalyses < maxAnalyses`), app-exposer sends `POST /analyses` with the bundle.
3. If the operator returns `409 Conflict` (race condition — filled between capacity check and launch), the next operator is tried.
4. If all operators are exhausted, the launch returns an error.

The operator that accepted the analysis is recorded in the `operator_name` column on the `jobs` table. Subsequent lifecycle operations (exit, save-and-exit) are routed to the correct operator using this column.

### Database Migration

The `operator_name` column must exist on the `jobs` table. The migration is at `de-database/migrations/000046_operator_name.up.sql`.
