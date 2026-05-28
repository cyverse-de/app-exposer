# Image Cache — Manual-Mirror Mode for direct-push ECR

## Context

The cron-mode design (see `2026-05-27-image-cache-cron-mode-ecr.md`)
assumed AWS ECR could be configured as a pull-through cache fronting our
self-hosted Harbor at `harbor.cyverse.org`. AWS does not support
self-hosted registries as pull-through upstreams; the supported list is
fixed (`ecr`, `ecr-public`, `docker-hub`, `quay`, `k8s`,
`github-container-registry`, `azure-container-registry`,
`gitlab-container-registry`, `chainguard`). `create-pull-through-cache-rule`
rejects `harbor.cyverse.org` with `UnsupportedUpstreamRegistryException`.

Our adopted path is direct push: an out-of-band mirror job pushes VICE
images into per-cluster ECR repos. The repos are created by
`deployments/ansible/scripts/setup-ecr-repos.sh`; the images themselves
are mirrored by `mirror-images-to-ecr.sh`. EKS Auto Mode nodes pull from
those ECR repos directly — no pull-through, no node-local DaemonSet cache.

What's missing in vice-operator: analysis bundles arriving at
`POST /vice/launch` still reference the original Harbor coordinates
(`harbor.cyverse.org/de/vice-proxy:latest`). The operator needs to rewrite
those image strings at launch time to the mirrored ECR coordinates so K8s
ends up pulling from ECR, not Harbor.

Cron mode is the closest existing mode but doesn't rewrite bundle images
— it manages CronJobs, not launch-time refs. Rather than retrofit cron
mode (which we want to keep as-is in case ECR adds Harbor support later),
we add a third mode dedicated to this workflow: `manual-mirror`.

## Approach

A third value for `--image-cache-mode`. The backing implementation
contributes two things:

- A **read-only `ImageCacheManager`** that exposes the mapping through
  the existing `GET /image-cache` and `GET /image-cache/:id` endpoints,
  so admins can introspect what's configured. The mutating endpoints
  (`PUT`, `DELETE`, refresh) return **400** with a message pointing at
  `--repos-file`, since the operator can't update the mapping at runtime.

- An **`ImageRewriter`** that runs as one more bundle transform in
  `HandleLaunch`, alongside the existing `TransformGPUModels` /
  `TransformGPUVendor` / etc. It walks the deployment's containers and
  init-containers and swaps image refs that have a mapping.

## Configuration

Two flags are now relevant to mode selection:

| Flag | Required when | Notes |
|---|---|---|
| `--image-cache-mode` | always | `daemonset` (default), `cron`, or `manual-mirror`. |
| `--repos-file` | mode = `manual-mirror` | Path to a JSON file mapping upstream image refs to mirrored refs. Setting this in any other mode is a startup error. |

The mapping file is parsed once at startup; the operator must be
restarted to pick up changes. This matches the K8s ConfigMap reload
pattern most clusters already use.

## Mapping file format

A flat JSON object: keys are upstream image refs as bundles will reference
them, values are fully-qualified mirrored refs as K8s should ultimately
pull. Both keys and values are validated against the same
`imageRefPattern` regex used for `PUT /image-cache` inputs (rejects
whitespace, shell-unsafe characters, and refs over 1024 chars).

```json
{
  "harbor.cyverse.org/de/vice-proxy:latest":
    "123456789012.dkr.ecr.us-east-1.amazonaws.com/de/vice-proxy:latest",
  "harbor.cyverse.org/de/porklock:latest":
    "123456789012.dkr.ecr.us-east-1.amazonaws.com/de/porklock:latest",
  "harbor.cyverse.org/de/vice-default-backend:qa":
    "123456789012.dkr.ecr.us-east-1.amazonaws.com/de/vice-default-backend:qa"
}
```

The operator stays registry-agnostic — values are fully qualified, so the
same code works with ECR, GHCR, or any future mirror target. Empty maps
are rejected at startup; empty keys or values are rejected at startup.

## Behavior at launch time

When `HandleLaunch` processes an `AnalysisBundle`:

1. Existing transforms run as today (hostname rewriting, gateway namespace
   substitution, vice-proxy args, GPU model mapping, GPU vendor).
2. If the operator has an `ImageRewriter` (only in manual-mirror mode), a
   new `TransformImageRefs` walks the bundle's
   `Deployment.Spec.Template.Spec.InitContainers[*].Image` and
   `Containers[*].Image`, substituting each image that has a mapping.
3. `applyBundle` runs unchanged — by the time it creates the Deployment,
   the image fields already carry the mirrored coordinates.

**Unmapped images pass through unchanged.** This deliberately avoids
making the operator a policy gate: if a bundle includes some new image we
haven't mirrored yet, the launch proceeds and K8s reports the usual
`ImagePullBackOff` if the upstream is unreachable from the cluster.
Adding pre-flight rejection would force every image change to be paired
with a mapping update before deploys could succeed, which is more
friction than it's worth for the failure modes we actually see.

The transform logs the substitution count at info level when it's
non-zero, so the absence of a log line confirms no rewriting happened —
useful when debugging "did the new image take effect" questions.

## Cache API in manual-mirror mode

| Endpoint | Behavior |
|---|---|
| `GET /image-cache` | Returns one `ImageCacheStatus` per mapping entry. `Image` is the upstream key (what bundles reference), `ID` is its slug, `Status` is `"ready"`, `Ready=Desired=1`. |
| `GET /image-cache/:id` | Reverse-lookup by slug. 404 if the slug doesn't match any entry. |
| `PUT /image-cache` | 400. Body: `"image cache is externally managed in this mode; update the --repos-file and restart instead"`. |
| `DELETE /image-cache` | 400, same body. |
| `DELETE /image-cache/:id` | 400, same body. |
| `POST /image-cache/refresh` | 400, same body. |

The handler short-circuits on `o.imageCache.ReadOnly()` before reaching
the manager, so callers get a single 400 rather than the bulk handler's
207 Multi-Status with per-image errors.

## Code layout

| File | Role |
|---|---|
| `operator/imagecache.go` | `ImageCacheManager` interface gains `ReadOnly()`; new `ImageRewriter` interface and `ErrCacheReadOnly` sentinel. |
| `operator/imagecache_daemonset.go` | `ReadOnly()` returns `false`. |
| `operator/imagecache_cronjob.go` | `ReadOnly()` returns `false`. |
| `operator/imagecache_manual_mirror.go` | `ManualMirrorImageCacheManager` (implements both interfaces) and `LoadImageMirrorMap`. |
| `operator/transform_images.go` | `TransformImageRefs(deployment, rewriter)` walks containers/init-containers. |
| `operator/imagecache_handlers.go` | Mutating handlers pre-check `ReadOnly()` and bail with 400. `HandleGetCachedImage` recognizes the manual-mirror not-found sentinel for the 404 path. |
| `operator/handlers.go` | New `imageRewriter` field on `Operator`; `HandleLaunch` calls `TransformImageRefs` when set. |
| `cmd/vice-operator/main.go` | New `--repos-file` flag. |
| `cmd/vice-operator/startup.go` | `buildImageCache` extended to return an `ImageRewriter`; manual-mirror branch validates the file at startup. |

## Out of scope

- **Hot reload** of the mapping file. Restart the operator pod to pick
  up changes. Standard ConfigMap-mount conventions cover the rollout.
- **Per-cluster mappings.** A single file per operator. If multiple
  clusters need different mappings, run separate operator deployments
  with separate `--repos-file` paths.
- **Mirror-state checks.** The operator trusts that the file's values
  exist in the target registry. If a mirrored ref is wrong or missing,
  the launch will proceed and the analysis pod will land in
  `ImagePullBackOff` — the same failure mode as a typo'd image in the
  bundle today.

## Verification

End-to-end on the `ua-ai-sandboxes` cluster:

1. Run `setup-ecr-repos.sh` and `mirror-images-to-ecr.sh` (deployments
   repo) to populate the per-cluster ECR repos.
2. Render a `repos.json` mapping each upstream Harbor ref to its mirrored
   ECR ref.
3. Start vice-operator with
   `--image-cache-mode=manual-mirror --repos-file=/etc/vice/repos.json`.
4. Submit an analysis whose bundle's deployment references a Harbor image
   in the map. Confirm the resulting `Deployment` in K8s shows the ECR
   coordinates and the operator log emits
   `rewrote N image ref(s) for analysis <id>`.
5. Submit an analysis with an unmapped image. Confirm the deployment is
   created with the upstream ref unchanged and no rewrite log line.
6. `curl GET /image-cache` and confirm the response lists the mapping
   entries. Attempt `PUT /image-cache` and confirm 400 with the
   externally-managed message.
