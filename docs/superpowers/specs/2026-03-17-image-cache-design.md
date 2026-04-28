# Image Cache via DaemonSets — Design Spec

## Context

VICE deployments are slow to start because container images must be pulled from
the registry at launch time. Pre-caching images onto cluster nodes eliminates
this delay. Vice-operator is the right place for this because it already manages
per-cluster K8s resources and is fully stateless.

## Approach

One DaemonSet per cached image. Each DaemonSet has an init container that
references the target image (causing K8s to pull it onto every node) and a main
container running `registry.k8s.io/pause:3.10`. The DaemonSets are the state —
vice-operator discovers them via label selectors and never stores anything
in-memory.

### Init container behavior on distroless/scratch images

The init container uses `command: ["true"]` to exit immediately after the image
is pulled. The `true` command is resolved via PATH, which works on more images
than a hardcoded `/bin/true`.

Images that lack `true` entirely (distroless, scratch-based) will cause the init
container to fail with CrashLoopBackOff. **This is expected and by design** —
K8s pulls the image before attempting to run the command, so the caching
objective is achieved regardless. The DaemonSet pods for these images will show
init container errors and restart backoff events in the node's event log. This is
cosmetic noise and does not affect the cached image's availability.

This behavior must be documented in:
- Code comments on `buildCacheDaemonSet`
- Swagger endpoint descriptions for PUT /image-cache
- This spec (here)

## DaemonSet structure

For each cached image (e.g. `harbor.cyverse.org/de/vice-proxy:latest`):

```yaml
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: image-cache-<slug>
  namespace: vice-apps
  labels:
    managed-by: vice-operator
    purpose: image-cache
    image-cache-id: <slug>
  annotations:
    de.cyverse.org/cached-image: "harbor.cyverse.org/de/vice-proxy:latest"
spec:
  selector:
    matchLabels:
      managed-by: vice-operator
      purpose: image-cache
      image-cache-id: <slug>
  template:
    metadata:
      labels:
        managed-by: vice-operator
        purpose: image-cache
        image-cache-id: <slug>
    spec:
      imagePullSecrets:
        - name: vice-image-pull-secret
      initContainers:
        - name: pull
          image: harbor.cyverse.org/de/vice-proxy:latest
          command: ["true"]
          imagePullPolicy: Always
          resources:
            requests:
              cpu: 1m
              memory: 1Mi
            limits:
              cpu: 1m
              memory: 1Mi
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.10
          imagePullPolicy: IfNotPresent
          resources:
            requests:
              cpu: 1m
              memory: 1Mi
            limits:
              cpu: 1m
              memory: 1Mi
      tolerations:
        - key: "analysis"
          operator: "Exists"
        - key: "gpu"
          operator: "Equal"
          value: "true"
          effect: "NoSchedule"
```

**Init container imagePullPolicy:** `Always` so that mutable tags like `:latest`
or `:qa` are re-pulled when the pod restarts. The pause container uses
`IfNotPresent` since it never changes.

**Tolerations:** The intent is to cache images on all eligible nodes, including
tainted analysis and GPU nodes. No `nodeSelector` is used — the DaemonSet
schedules everywhere, tolerations just prevent taints from blocking it.

### DaemonSet naming

The name is `image-cache-` + a slug derived from the image reference. The slug:
- Replaces `/`, `:`, `.`, `@` with `-`
- Lowercases everything
- Truncates to fit within K8s 253-char name limit (after prefix)
- Appends first 12 characters of SHA-256 hex digest of the full image ref

This produces names like `image-cache-harbor-cyverse-org-de-vice-proxy-a1b2c3d4e5f6`.
Twelve hex characters (48 bits) gives a birthday-paradox collision probability of
50% at ~16 million images, which is effectively zero for our use case.

The full image reference is stored in the `de.cyverse.org/cached-image`
annotation for reliable reverse lookup.

## API endpoints

All endpoints require basic auth (existing middleware). All are documented with
Swagger annotations and visible in the Swagger UI.

### PUT /image-cache — bulk add/update cached images

**Request:**
```json
{
  "images": [
    "harbor.cyverse.org/de/vice-proxy:latest",
    "harbor.cyverse.org/de/porklock:latest"
  ]
}
```

**Response (200 — all succeeded):**
```json
{
  "results": [
    {"image": "harbor.cyverse.org/de/vice-proxy:latest", "status": "ok"},
    {"image": "harbor.cyverse.org/de/porklock:latest", "status": "ok"}
  ]
}
```

**Response (207 — partial success):**
```json
{
  "results": [
    {"image": "harbor.cyverse.org/de/vice-proxy:latest", "status": "ok"},
    {"image": "bad-image!!!", "status": "error", "error": "invalid image reference"}
  ]
}
```

**Response (400):** Malformed request body.

Logic: For each image, validate the reference, compute the slug, then upsert the
DaemonSet (Get → IsNotFound → Create, else compare annotation and Update if
different).

### GET /image-cache — list all cached images

**Response (200):**
```json
{
  "images": [
    {
      "image": "harbor.cyverse.org/de/vice-proxy:latest",
      "id": "harbor-cyverse-org-de-vice-proxy-a1b2c3d4e5f6",
      "ready": 3,
      "desired": 3,
      "status": "ready"
    },
    {
      "image": "harbor.cyverse.org/de/porklock:latest",
      "id": "harbor-cyverse-org-de-porklock-d4e5f6a7b8c9",
      "ready": 2,
      "desired": 3,
      "status": "pulling"
    }
  ]
}
```

Status values (evaluated in order, first match wins):
1. `"error"` — `desiredNumberScheduled == 0` (no nodes match the DaemonSet's
   scheduling constraints)
2. `"ready"` — `numberReady == desiredNumberScheduled` (all pods running)
3. `"cached-with-errors"` — `numberReady == 0` and `desiredNumberScheduled > 0`
   (image is pulled on nodes but init container failed — expected for
   distroless/scratch images that lack `true`; the image is still cached)
4. `"pulling"` — `numberReady < desiredNumberScheduled` (some pods still
   starting or pulling the image)

### GET /image-cache/:id — single image status

The `:id` parameter is the slug returned in the `id` field of list responses
(e.g. `harbor-cyverse-org-de-vice-proxy-a1b2c3d4e5f6`), not the full image
reference.

**Response (200):** Single `ImageCacheStatus` object (same shape as list items).

**Response (404):** No cache DaemonSet with that ID.

### DELETE /image-cache/:id — remove single cached image

The `:id` parameter is the slug, same as GET.

**Response (200):** Image removed (or already absent — idempotent).

### DELETE /image-cache — bulk remove cached images

**Request:** Same `ImageCacheRequest` body as PUT, with image references (not
slugs). The server computes slugs internally for deletion.

```json
{
  "images": [
    "harbor.cyverse.org/de/vice-proxy:latest"
  ]
}
```

**Response:** Same per-image result format as PUT. Deleting a non-existent image
returns `"status": "ok"` (idempotent).

**Note on DELETE with body:** Some HTTP clients and proxies drop the request body
on DELETE. This API is intended for scripts and curl, where this is not an issue.
Browser-based clients should use the single-image DELETE /:id endpoint instead.

## Types

```go
// ImageCacheRequest is the request body for bulk cache operations.
type ImageCacheRequest struct {
    Images []string `json:"images"`
}

// ImageCacheResult reports the outcome of a single image cache operation.
type ImageCacheResult struct {
    Image  string `json:"image"`
    Status string `json:"status"`          // "ok" or "error"
    Error  string `json:"error,omitempty"` // present only when status is "error"
}

// ImageCacheBulkResponse is the response for bulk cache operations.
type ImageCacheBulkResponse struct {
    Results []ImageCacheResult `json:"results"`
}

// ImageCacheStatus describes the cache state of a single image.
type ImageCacheStatus struct {
    Image   string `json:"image"`
    ID      string `json:"id"`
    Ready   int32  `json:"ready"`
    Desired int32  `json:"desired"`
    Status  string `json:"status"` // "ready", "pulling", "cached-with-errors", "error"
}

// ImageCacheListResponse is the response for listing cached images.
type ImageCacheListResponse struct {
    Images []ImageCacheStatus `json:"images"`
}
```

## Code structure

### New files

**`operator/imagecache.go`** — ImageCacheManager and business logic:
- `ImageCacheManager` struct: `clientset`, `namespace`, `imagePullSecretName`,
  tolerations
- `slugifyImage(image string) string` — deterministic slug with SHA-256 suffix
- `buildCacheDaemonSet(image, slug string) *appsv1.DaemonSet` — construct spec
- `EnsureImageCached(ctx, image) error` — upsert DaemonSet
- `RemoveCachedImage(ctx, image) error` — delete by slug (idempotent)
- `ListCachedImages(ctx) ([]ImageCacheStatus, error)` — list labeled DaemonSets
- `GetCachedImageStatus(ctx, id) (*ImageCacheStatus, error)` — single status

**`operator/imagecache_test.go`** — table-driven tests with fake clientset:
- Slug generation (uniqueness, special characters, long names)
- DaemonSet creation, idempotent re-creation, deletion
- Listing and status derivation
- Image reference validation

### Modified files

**`operator/handlers.go`** — add image cache handler methods with Swagger
annotations. Handlers are methods on `*Operator` that delegate to
`ImageCacheManager`. Add `ImageCacheManager` field to the `Operator` struct.

Handler methods:
- `HandleCacheImages` (PUT /image-cache)
- `HandleRemoveCachedImages` (DELETE /image-cache)
- `HandleListCachedImages` (GET /image-cache)
- `HandleGetCachedImage` (GET /image-cache/:id)
- `HandleDeleteCachedImage` (DELETE /image-cache/:id)

**`cmd/vice-operator/main.go`** — pass the existing `--image-pull-secret` flag
value (`imagePullSecret` variable) to `NewImageCacheManager`. Pass the resulting
manager to `NewOperator` (which gains a new parameter for it).

**`cmd/vice-operator/app.go`** — register image cache routes under the existing
auth group. `NewApp` already receives `*operator.Operator`, which now contains
the `ImageCacheManager`, so no signature change is needed.

**`k8s/vice-operator-local.yml`** — add `daemonsets` to the ClusterRole's `apps`
API group resources.

**`operatordocs/`** — regenerated by `swag init` after adding Swagger
annotations.

## Prerequisites

- **Image pull secret:** The `--registry-server`, `--registry-username`, and
  `--registry-password` flags must be configured for caching private registry
  images. Without them, the image pull secret referenced by cache DaemonSets
  won't exist, and K8s will fail to pull private images (pods stay in
  ImagePullBackOff). Public images work without registry credentials.

## Error handling

- **Bulk operations:** Each image processed independently. 200 if all succeed,
  207 if partial, 400 if request body is malformed.
- **Image validation:** Reject empty strings, strings with spaces, and obviously
  invalid references before creating DaemonSets.
- **DELETE idempotency:** Deleting a non-existent cache entry succeeds silently,
  consistent with the project's DELETE idempotency guideline.
- **K8s API errors:** Wrapped with context (`fmt.Errorf` + `%w`) and returned
  as per-image errors in bulk responses.

## RBAC changes

Add `daemonsets` to the `apps` API group in the ClusterRole:

```yaml
  - apiGroups: ["apps"]
    resources: ["deployments", "daemonsets"]
    verbs: ["get", "list", "watch", "create", "update", "delete"]
```

## Tolerations

Default tolerations match the operator's own deployment tolerations:
- `analysis: Exists` — cache on analysis nodes
- `gpu: Equal "true", NoSchedule` — cache on GPU nodes

The intent is to cache images everywhere, including tainted nodes. No
`nodeSelector` or `nodeAffinity` is used.

Toleration configuration is deferred to a future change. For now, these defaults
are hardcoded in `buildCacheDaemonSet`. A `--cache-tolerations` flag or ConfigMap
can be added later.

## Testing

Table-driven tests in `operator/imagecache_test.go` using `fake.Clientset`:

| Test case | Behavior |
|-----------|----------|
| Cache new image | Creates DaemonSet with correct labels/annotations |
| Cache existing image | No-op (idempotent) |
| Cache with different tag | Updates DaemonSet |
| Remove cached image | Deletes DaemonSet |
| Remove non-existent image | Succeeds silently |
| List cached images | Returns all labeled DaemonSets with status |
| Status ready vs pulling | Correct status derivation from DaemonSet status |
| Status error conditions | 0 desired or all unavailable |
| Slug uniqueness | Different images produce different slugs |
| Slug determinism | Same image always produces same slug |
| Invalid image reference | Returns validation error |
| DaemonSet spec correctness | Init container, pause, resources, tolerations, pull secret |

## Files changed summary

| File | Change |
|------|--------|
| `operator/imagecache.go` | New — ImageCacheManager, slug, DaemonSet builder, CRUD |
| `operator/imagecache_test.go` | New — table-driven tests |
| `operator/handlers.go` | Add ImageCacheManager to Operator, add handler methods |
| `cmd/vice-operator/main.go` | Wire ImageCacheManager with existing imagePullSecret flag |
| `cmd/vice-operator/app.go` | Register image cache routes under auth group |
| `k8s/vice-operator-local.yml` | Add daemonsets to ClusterRole |
| `operatordocs/` | Regenerated Swagger docs |
