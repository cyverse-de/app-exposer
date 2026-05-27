# Image Cache — Cron Mode for EKS Auto Mode + ECR Pull-Through Cache

## Context

The original image cache design (see
`2026-03-17-image-cache-design.md`) ships one DaemonSet per cached image
so each node's containerd has the image warm before a VICE pod schedules.
That works on self-managed clusters where nodes are long-lived.

On **EKS Auto Mode**, AWS manages the node pool and recycles nodes
aggressively (default ~14-day max lifetime, plus consolidation churn).
A node-local cache barely pays back its cost before the node is replaced.
Worse, every new node has to re-pull every image, and we are paying
DaemonSet pod overhead per-image-per-node continuously, for cache
populations that only matter at the moment a VICE pod actually starts.

The supported AWS pattern for warming images in Auto Mode is **ECR
pull-through cache**: ECR proxies pulls from an upstream registry (Harbor,
Docker Hub, k8s.gcr.io) and stores the result. EKS Auto Mode nodes are
configured to resolve pulls through ECR transparently, so a pod that
references `harbor.cyverse.org/de/vice-proxy:latest` ends up pulling the
ECR-cached copy with no image-reference rewriting needed at the pod
level.

To keep ECR's copy fresh we need vice-operator to *periodically* pull
each image so the corresponding repository in ECR gets refreshed before
its layers expire. CronJobs are the right K8s primitive for that.

## Approach

Add a second `ImageCacheManager` implementation
(`CronJobImageCacheManager`) selected by a `--image-cache-mode` flag:

- `daemonset` (default) — existing DaemonSet behavior, unchanged.
- `cron` — one CronJob per cached image, firing on a globally configured
  schedule (`--image-cache-schedule`, default `0 2 * * *` UTC).

The vice-operator HTTP API is unchanged. Both implementations satisfy the
same interface; handlers don't branch.

### CronJob structure

For each cached image:

```yaml
apiVersion: batch/v1
kind: CronJob
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
  schedule: "0 2 * * *"
  concurrencyPolicy: Forbid
  successfulJobsHistoryLimit: 1
  failedJobsHistoryLimit: 1
  jobTemplate:
    spec:
      backoffLimit: 0
      template:
        metadata:
          labels:
            managed-by: vice-operator
            purpose: image-cache
            image-cache-id: <slug>
        spec:
          restartPolicy: Never
          imagePullSecrets:
            - name: vice-image-pull-secret
          containers:
            - name: pull
              image: harbor.cyverse.org/de/vice-proxy:latest
              command: ["true"]
              imagePullPolicy: Always
              resources:
                requests: {cpu: 1m, memory: 64Mi}
                limits:   {cpu: 10m, memory: 64Mi}
          tolerations:
            - {key: analysis, operator: Exists}
            - {key: gpu, operator: Equal, value: "true", effect: NoSchedule}
```

Notes:

- **Single container, no pause sidecar.** A Job pod doesn't need a
  long-running main container the way a DaemonSet pod does.
- **`backoffLimit: 0`.** A failed pull is not worth retrying within the
  same scheduling tick; the next cron firing is the retry.
- **History limits = 1.** We keep one most-recent success and one
  most-recent failure so the status API can report the latest outcome
  without listing Jobs.
- **`ConcurrencyPolicy: Forbid`.** Never overlap pulls of the same image.
- **No image-reference rewriting.** The original image string goes into
  the Job. Node-level containerd config (provisioned by Auto Mode) routes
  the pull through the ECR pull-through cache repo transparently.

### Refresh semantics

`POST /image-cache/refresh` in cron mode creates a one-off Job from the
CronJob's job template (with `GenerateName: image-cache-<slug>-r-` and
`TTLSecondsAfterFinished: 300`). The CronJob continues firing on its
normal schedule afterwards. This preserves "refresh means re-pull now"
across both modes.

The refresh Job carries an OwnerReference back to its CronJob so K8s
garbage-collects orphans when the CronJob is deleted.

### Status mapping

CronJob state collapses to the same `(ready, desired, status)` shape that
DaemonSet mode returns, so the API response schema is identical:

| CronJob state                  | ready | desired | status               |
|--------------------------------|-------|---------|----------------------|
| Suspended                      | 0     | 0       | `error`              |
| Never run yet                  | 0     | 1       | `pulling`            |
| Last scheduled run succeeded   | 1     | 1       | `ready`              |
| Last scheduled run failed      | 0     | 1       | `cached-with-errors` |

"Last scheduled run succeeded" is computed by comparing
`Status.LastScheduleTime` and `Status.LastSuccessfulTime` — no Jobs API
fan-out needed.

### Deletion

`DELETE /image-cache/:id` in cron mode deletes only the CronJob. **It
does not evict the image from ECR.** Image eviction is handled by ECR
lifecycle policies, configured outside vice-operator (per repository,
typically by age and pull-recency). This matches the design intent: the
operator's job is to *populate* the cache; ECR is the storage system.

## Configuration

Two new vice-operator CLI flags:

- `--image-cache-mode` (string, default `"daemonset"`). Accepts
  `daemonset` or `cron`. Invalid value is a fatal startup error.
- `--image-cache-schedule` (string, default `"0 2 * * *"`). Standard
  5-field cron expression in UTC, applied to every cached image. Parsed
  at startup with `robfig/cron/v3`; an invalid expression is a fatal
  startup error (better than letting K8s reject CronJobs at every
  ensure call).

All other image-cache configuration is unchanged — `--image-pull-secret`,
`--namespace`, etc. work the same way in both modes.

## RBAC

Cron mode requires the vice-operator ClusterRole to permit CronJob and
Job operations. Add to the `batch` apiGroup:

```yaml
- apiGroups: ["batch"]
  resources: ["cronjobs", "jobs"]
  verbs: ["get", "list", "watch", "create", "update", "delete"]
```

This is a deployment-manifest change in the cluster's vice-operator
install, not a code change in app-exposer. Roll it out before flipping
the mode flag.

## Code layout

After this change, the `operator/` package has three image-cache files:

- `imagecache.go` — request/response types, the `ImageCacheManager`
  interface, shared helpers (`slugifyImage`, `validateImageRef`,
  `deriveCacheStatus`), constants.
- `imagecache_daemonset.go` — `DaemonSetImageCacheManager` (the original
  implementation, renamed).
- `imagecache_cronjob.go` — `CronJobImageCacheManager`.

Tests are split the same way: `imagecache_test.go` holds shared-helper
and handler tests; `imagecache_daemonset_test.go` and
`imagecache_cronjob_test.go` hold backend-specific tests.

`cmd/vice-operator/startup.go` gains a `buildImageCache` helper that
inspects the mode flag and returns the matching implementation.

## Out of scope

- ECR repository creation and pull-through cache rule provisioning
  (handled by Terraform/IaC outside this codebase).
- ECR lifecycle policies (also IaC).
- Per-image schedule overrides — the global flag is sufficient for the
  expected usage pattern (warm a small fixed set of VICE base images
  once per day).
- Switching modes at runtime — mode is fixed at process startup. A mode
  change requires a vice-operator restart, which is acceptable since
  mode changes happen at most once per cluster lifecycle.

## Verification

See the implementation plan for the full test matrix. End-to-end
verification at deploy time on an EKS Auto Mode cluster:

1. Provision an ECR pull-through cache rule for Harbor (or whatever
   upstream registries the VICE base images live on).
2. Deploy vice-operator with `--image-cache-mode=cron`.
3. `PUT /image-cache` with a VICE base image; wait for the cron to
   fire.
4. Confirm the image appears in the matching ECR repository
   (`aws ecr describe-images` or the AWS console).
5. `POST /image-cache/refresh` triggers an immediate Job; confirm the
   Job completes and ECR's `imagePushedAt` advances.
