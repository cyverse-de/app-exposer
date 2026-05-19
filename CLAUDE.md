# app-exposer

A Go service (Echo) that manages VICE (Visual Interactive Computing Environment) analyses for the CyVerse Discovery Environment, plus a `vice-operator` that runs in each managed cluster and applies pre-built k8s resource bundles handed to it by app-exposer. See `README.md` for human-facing build/deploy detail and `api.yml` for the full REST API.

## Build, test, lint

`just` is the entry point (see `Justfile`).

```
just                     # build all 11 binaries
just app-exposer         # single binary; same pattern for the others
just test                # all package tests (or individual targets, e.g. just test-operator)
just docs                # regenerate Swagger for app-exposer (writes to docs/)
just operator-docs       # regenerate Swagger for vice-operator (writes to operatordocs/)
just build-image         # docker build
golangci-lint run        # lint; config in .golangci.yml
```

`just operator-docs` uses custom `[[,]]` template delimiters — kubebuilder annotations on Gateway API types contain literal `{{ }}`, which break the default Swagger generator. Don't "fix" the delimiters back.

## Binaries (`cmd/`)

| Subdir | What it is |
| --- | --- |
| `app-exposer/` | Main Echo REST server |
| `vice-operator/` | In-cluster operator that receives bundles from app-exposer |
| `vice-operator-tool/` | Admin CLI against vice-operator |
| `vice-operator-token/` | Fetches a Keycloak OAuth token for curl/debug |
| `workflow-builder/` | Argo Workflows YAML generator |
| `vicetools/` | Shared code used by the `vice-*` CLIs |
| `vice-export`, `vice-import`, `vice-launch`, `vice-list`, `vice-bundle`, `vice-userid` | VICE CLI utilities |

A single Docker image ships both `app-exposer` and `vice-operator`.

## Top-level packages

- `adapter/` — JEX adapter for batch/Argo job submission
- `apps/` — internal client to the `apps` Clojure service
- `batch/` — Argo Workflows job builder
- `common/` — shared logger, error responses, label helpers, `FixUsername`
- `constants/` — k8s label/annotation constants
- `db/` — sqlx-backed DB access (operators table, analyses, …)
- `httphandlers/` — Echo handlers, split per feature (e.g. `launch.go`, `exit.go`)
- `imageinfo/` — Harbor image-info queries
- `incluster/` — k8s-native VICE launch logic (Deployments, Services, Ingresses)
- `instantlaunches/` — quick-launch saved configs
- `millicores/` — CPU quantity helpers
- `natsconn/` — NATS connection helpers
- `operator/` — vice-operator server-side logic (capacity calc, gateway/loading pages, status informer)
- `operatorclient/` — HTTP client app-exposer uses to talk to vice-operator
- `outcluster/` — **legacy** HTCondor path (Services/Endpoints/Ingresses for non-k8s apps); only touch if the task explicitly calls for it
- `permissions/` — DE permissions service integration
- `quota/` — QMS-based quota enforcement
- `reconciler/` — DB-reconciliation loop (operators table, status updates)
- `reporting/` — k8s resource reporting types
- `resourcing/` — request/limit defaults
- `types/` — `Router` interface (Echo-compatible)
- `k8s/` — currently empty placeholder

## Echo routes (cmd/app-exposer/app.go)

Top-level groups — see `api.yml` and `docs/` for the full surface:

- `/`, `/docs/*`, `/backchannel-logout`
- `/batch`
- `/vice`, `/vice/admin`, `/vice/listing/*`
- `/service`, `/endpoint`, `/routes` (outcluster)
- `/instantlaunches`

Middleware: `otelecho`, Echo's Logger, a custom error handler, plus `swaggerauth` / `viceusersauth` for OAuth.

## Config

Loaded via **koanf**, not a typed struct. Sources in order: file (`/etc/de/app-exposer/config.yml` or `-config`) → env (`DE_*` prefix) → flags.

- Template with all keys: `configs/default.yml`
- Access pattern: `cfg.String("k8s.frontend.base")`, `cfg.Bool(...)`, etc. There is no compile-time check that a key exists, so typos are runtime errors.
- Kubeconfig: `~/.kube/config` by default; setting the `CLUSTER` env var switches to in-cluster config.
- Important namespace flags: `--namespace` (default `default`, used for outcluster resources) and `--vice-namespace` (default `vice-apps`, where VICE pods run).
- Local-dev TLS certs and a sample service listing live in `local-config/`.

## Logging

`logrus`, initialized in `common/`:

```go
common.Log = logrus.WithFields(logrus.Fields{
    "service": "app-exposer", "art-id": "app-exposer", "group": "org.cyverse",
})
```

Every package does `var log = common.Log`. Caller reporting is on. Level is set via `-log-level` (default `warn`).

## Testing

- Standard `testing.T`, table-driven
- `testify/assert` + `testify/require`
- k8s mocks via `k8s.io/client-go/kubernetes/fake`
- DB mocks via `DATA-DOG/go-sqlmock`
- No ginkgo / BDD framework
- Single package: `go test ./operator/...` or the matching `just test-*` target

## Conventions and gotchas

- **Usernames carry a suffix** (e.g. `@iplantcollaborative.org`); normalize with `common.FixUsername` before comparing or persisting.
- **DELETEs are idempotent** — deleting a missing resource is success, not 404.
- **DB calls require a `Tx`** — never operate outside a transaction, and thread `context.Context` end-to-end (use `*Context` variants like `ExecContext`/`QueryRowContext`).
- **Sanitize DB errors** in HTTP responses (map `sql.ErrNoRows` → 404, others → 500; log the real error server-side).
- **No CRDs defined here** — vice-operator uses the upstream k8s Gateway API (`sigs.k8s.io/gateway-api`).
- **Two Swagger doc trees**: `docs/` for app-exposer, `operatordocs/` for vice-operator (instance name `operator`); regenerate with `just docs` / `just operator-docs`.
- **`outcluster/` is legacy HTCondor support** — avoid modernizing it unless the task asks.
- **Files over ~300 lines** should be split by entity/feature (`launch.go`, `exit.go`, …) — follow the existing pattern in `httphandlers/`.

## Related services

- `apps` (Clojure) — app catalog and job submission; calls `POST /vice/launch` and `POST /vice/{uuid}/save-and-exit`.
- `terrain` (Clojure) — API gateway; calls app-exposer for VICE management.

## Pointers

- `README.md` — human-facing build/run/deploy
- `api.yml` — OpenAPI spec
- `plans/` — design docs and future-work notes; worth skimming before large changes
- `Justfile` — authoritative list of build/test/doc commands
