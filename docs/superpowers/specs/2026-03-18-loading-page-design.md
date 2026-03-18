# Vice-operator Loading Page — Design Spec

## Context

VICE analyses currently rely on Sonora (the DE frontend) to host a loading page
that polls app-exposer for Kubernetes resource status. This creates cross-cluster
dependencies — Sonora must reach into remote clusters via app-exposer to check
pod/container state. As VICE moves to a multi-cluster architecture, this becomes
a scalability and isolation problem.

Vice-operator already runs per-cluster and has direct access to K8s resource
status. Moving the loading page into vice-operator makes analyses more
self-contained, eliminates cross-cluster status polling, and simplifies both
Sonora and the overall request flow.

## Approach

Vice-operator acts as the **initial routing target** for every VICE analysis.
When a user visits their analysis URL, they hit vice-operator's loading page
instead of the (not-yet-ready) analysis. Once the analysis is ready,
vice-operator swaps the HTTPRoute/Ingress backend to point at the real analysis
service. The user's browser redirects to the same URL, which now serves the live
app (through vice-proxy and Keycloak).

## Route lifecycle

### 1. Bundle received

App-exposer builds the AnalysisBundle as usual, with the HTTPRoute/Ingress
backend pointing at the analysis service. The `subdomain` label is already set
on all resources by app-exposer's `JobLabels()` (in `incluster/jobinfo/main.go`),
generated via `common.Subdomain(userID, invocationID)`.

Vice-operator receives the bundle and:

1. Transforms the HTTPRoute/Ingress backend to point at vice-operator's loading
   page K8s Service (alongside existing routing-type and GPU transforms).
2. Applies all resources to the cluster. The analysis Service is created
   normally — it just isn't routed to yet.

### 2. Loading state

When a user visits the analysis URL (e.g., `https://a1234abcd.cyverse.run`):

1. The HTTPRoute/Ingress routes the request to vice-operator's loading page
   server.
2. Vice-operator extracts the subdomain from the `Host` header.
3. Vice-operator lists Deployments with label `subdomain={subdomain}` to get
   the `analysis-id` from the resource labels.
4. Vice-operator renders a server-side HTML loading page with the app name and
   analysis-id.
5. Inline JavaScript polls `GET /loading/status` every 5 seconds (relative
   path, same host — no CORS issues since vice-operator is the backend for
   this subdomain).

### 3. Ready state (route swap)

When the `/loading/status` handler detects readiness (deployment has ready
replicas, service exists, routing resource exists):

1. Vice-operator updates the HTTPRoute or Ingress backend to point at the
   analysis Service (routing-type aware — handles gateway, nginx, tailscale).
2. The status response returns `ready: true`.
3. The loading page JavaScript does `window.location.href = window.location.href`
   to trigger a full page navigation to the same URL.
4. The request now hits the real app. Vice-proxy redirects the unauthenticated
   user to Keycloak for login, then proxies to the analysis container.

### 4. Cleanup

No cleanup needed — the route now points at the analysis service, and
vice-operator no longer receives requests for that subdomain. The `subdomain`
label remains on resources for operational/debugging use.

## Loading page UI

Server-rendered HTML via Go `html/template`, embedded with `embed.FS`.

### Layout

- **Header**: "Launching: {appName}"
- **Progress indicator**: Simple progress bar with three high-level stages:
  - "Deploying..." — resources being created, pods pending
  - "Starting..." — pods running, containers initializing
  - "Almost ready..." — all containers ready, waiting for app to respond
- **Error state**: If pods enter CrashLoopBackOff or a container has
  `restartCount > 2` with an error state, display "Failed to start. Please
  contact support." with a support link. A client-side timeout in the polling
  JS (configurable via a template variable, default 10 minutes from first
  poll) also triggers the error state if the analysis never reaches readiness.
  The timeout is passed to vice-operator via `--loading-timeout` flag
  (default `10m`), which injects it into the template as a JS variable.
- **Details toggle**: Expandable section showing pod phase, container states,
  and restart counts. Hidden by default, useful for debugging.

### Implementation

- Single `loading.html` template in `operator/templates/`, embedded via
  `embed.FS`.
- Inline `<script>` for polling — vanilla JS, no build pipeline.
- Template receives: app name (from `app-name` label on the Deployment),
  analysis-id (from `analysis-id` label), and loading timeout in milliseconds.
  All three values come from the same K8s Deployment lookup used for subdomain
  resolution.
- Polling endpoint is a relative path (`/loading/status`) since vice-operator
  serves both the HTML and the status API on the analysis subdomain.

## Vice-operator changes

### New loading page server

Vice-operator listens on a second HTTP port (e.g., 8080, configurable via
`--loading-port` flag) dedicated to serving loading pages. This cleanly
separates loading page traffic (routed via HTTPRoute/Ingress on analysis
subdomains) from the operator API (port 60001, called by app-exposer).

**Endpoints on loading page port (host-based routing):**

- `GET /` — Renders the loading page HTML. Extracts subdomain from `Host`
  header, looks up analysis-id via K8s label query, renders template.
- `GET /loading/status` — Returns JSON status for the analysis. The readiness
  check is: deployment has at least one ready replica AND the analysis Service
  exists. Note: this intentionally omits the "routing resource exists" check
  from `HandleURLReady`, since the route always exists during loading (it just
  points at vice-operator). If readiness conditions are met, performs the route
  swap before responding. The swap is idempotent so repeated GETs are safe —
  this is an internal API only reachable while the route points at
  vice-operator.

  Response schema:

  ```json
  {
    "ready": false,
    "stage": "starting",
    "error": "",
    "pods": [
      {
        "name": "analysis-abc-xyz",
        "phase": "Running",
        "ready": false,
        "restartCount": 0,
        "containerStatuses": [
          {
            "name": "analysis",
            "state": "waiting",
            "reason": "ContainerCreating",
            "ready": false,
            "restartCount": 0
          }
        ]
      }
    ]
  }
  ```

  The `stage` field is one of: `"deploying"` (no pods yet or pods pending),
  `"starting"` (pods running, containers initializing), `"almost-ready"` (all
  containers ready, waiting for app to respond), `"ready"` (swap completed),
  or `"error"` (failure detected). The `error` field contains a human-readable
  message when `stage` is `"error"`, empty otherwise.

  The `pods` and `containerStatuses` fields require richer detail than the
  existing `PodInfo` struct in `handlers.go` (which only has Name, Phase,
  Ready). A new `LoadingStatusResponse` type will be defined in
  `operator/loading.go` with the schema above, querying the K8s API directly
  for full pod/container status (via `corev1.Pod.Status.ContainerStatuses`).
  This is new serialization logic, not a reuse of the existing `PodInfo` type.

### Subdomain → analysis-id resolution

No in-memory cache. Every request resolves the subdomain by listing Deployments
with the `subdomain` label. This is correct across multiple vice-operator
replicas and avoids cache consistency issues. The load is negligible — a single
K8s API call per request, with requests coming every 5 seconds per
actively-loading analysis.

### Route swap logic

New file: `operator/routeswap.go`

The swap updates the HTTPRoute or Ingress backend service reference based on
the configured `--routing-type`:

- **gateway**: Update `HTTPRoute.spec.rules[].backendRefs[].name` to the
  analysis Service name.
- **nginx**: Update `Ingress.spec.rules[].http.paths[].backend.service.name`
  to the analysis Service name.
- **tailscale**: Same as nginx (different annotations, same backend structure).

The analysis Service name is obtained from the Service resource with the
matching `analysis-id` label — it was created during bundle application and is
already running in the cluster.

### Manual swap trigger

New endpoint on the API port (60001):

- `POST /analyses/:analysis-id/swap-route` — Manually triggers the route swap
  for an analysis, regardless of readiness state. Useful for operational
  scripts, admin tools, and cleaning up orphaned loading routes.

### Bundle transform

In `HandleLaunch`, after the existing routing-type and GPU transforms but
before calling `applyBundle` (matching the existing transform pattern):

1. Rewrite the HTTPRoute/Ingress backend to point at vice-operator's loading
   page K8s Service on the loading page port.

This follows the established pattern where all bundle transforms happen in
`HandleLaunch` before resource application, not inside `applyBundle` itself.

The `subdomain` label is already present on all bundle resources, set by
app-exposer's `JobLabels()`. Vice-operator does not need to add it.

### Loading page K8s Service

Vice-operator requires a K8s Service to be deployed alongside it that routes to
its loading page port. This Service is created as part of vice-operator's
deployment manifests (Helm chart / static manifests), not dynamically by
vice-operator itself.

Example Service:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: vice-operator-loading
  namespace: vice-apps
spec:
  selector:
    app: vice-operator
  ports:
    - port: 80
      targetPort: 8080
      protocol: TCP
```

The HTTPRoute/Ingress backend transform rewrites the backend service name to
`vice-operator-loading` (configurable via `--loading-service-name` flag,
default `vice-operator-loading`) and the port to 80.

### New files

| File | Purpose |
|------|---------|
| `operator/loading.go` | Loading page HTTP handlers and server setup |
| `operator/routeswap.go` | Routing-type-aware route swap logic |
| `operator/templates/loading.html` | Embedded HTML template |

### Modified files

| File | Change |
|------|--------|
| `operator/handlers.go` | Call backend rewrite transform in `HandleLaunch`; add `HandleSwapRoute` handler |
| `operator/transforms.go` | Backend rewrite transform (loading page service swap) |
| `cmd/vice-operator/main.go` | Add `--loading-port`, `--loading-service-name`, `--loading-timeout` flags |
| `cmd/vice-operator/app.go` | Create second `echo.Echo` instance for loading pages; `main.go` starts both servers — the API server blocks on the main goroutine, the loading page server runs in a separate goroutine. Both participate in graceful shutdown via shared context cancellation. |

## App-exposer changes

None. App-exposer builds bundles as usual. Vice-operator handles the backend
transform.

## Sonora changes

### Removals

- `src/pages/vice/[accessUrl].js` — loading page route
- `src/components/vice/loading/` — all loading page components (index.js,
  LoadingAnimation.js, Toolbar.js, DetailsContent.js, ContactSupportDialog.js,
  util.js, ids.js, styles.js, vice_loading.json)
- `src/serviceFacades/vice/loading.js` — API calls for loading status
- `public/static/locales/en/vice-loading.json` — localization strings

### Modifications

- `src/components/analyses/utils.js` — Update `openInteractiveUrl` to navigate
  directly to the analysis URL instead of wrapping it in `/vice/[encodedUrl]`.

## Vice-proxy changes

None.

## Edge cases

### User closes browser during loading

The analysis continues deploying. The HTTPRoute/Ingress stays pointing at
vice-operator until someone visits the URL again. On the next visit, the status
check detects readiness and swaps the route immediately. The manual swap
endpoint (`POST /analyses/:analysis-id/swap-route`) can also be used to resolve
this.

### Analysis fails to start

The loading page displays an error message. The route stays pointing at
vice-operator, which is fine — the user sees the error page. When the analysis
is eventually deleted (via `HandleExit`), all resources including the
HTTPRoute/Ingress are cleaned up normally.

### Multiple vice-operator replicas

No shared state. Every request resolves subdomain → analysis-id via K8s label
query. Route swaps are idempotent K8s updates — if two replicas both swap, the
second write is a no-op. The manual swap endpoint is also safe across replicas.

### Unknown subdomain

If a request arrives on the loading page port with a `Host` header that doesn't
match any Deployment's `subdomain` label, the loading page server returns a
simple 404 page: "Analysis not found." This can happen if an analysis was
deleted while the route still pointed at vice-operator, or if the URL is
invalid.

### Health checks

K8s liveness/readiness probes for vice-operator should target the API port
(60001), not the loading page port. The loading page port serves host-based
content and should not be used for health checks.

### Orphaned loading routes

If an analysis becomes ready but nobody visits the URL, the route stays
pointing at vice-operator. The manual swap endpoint allows operational scripts
to sweep for and resolve these. A future enhancement could add a periodic sweep,
but this is not required for the initial implementation.
