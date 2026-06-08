# The `VICESpec` contract

Phase 0, step 3 of the
[migration plan](operator-side-bundle-construction-migration.md). The
[design doc](operator-side-bundle-construction.md) is the *what & why*; the
[field audit](operator-side-bundle-construction-field-audit.md) is the
authoritative field-to-source mapping. This doc is the **contract**: who owns
each field, how the contract versions, and how the scheduler treats an operator
that is too old to honor it.

The types live in `operatorclient/vicespec.go` (`VICESpec`, `ContainerSpec`,
`ResourceSpec`, `GPUSpec`, `InputSpec`, `MetadataAVU`), next to the
`AnalysisBundle` they will eventually replace. Nothing builds or consumes them
yet (Phase 0 is contract-only).

## Direction of the contract

```
app-exposer  ──── VICESpec (what the analysis is) ────▶  vice-operator
  owns the analysis + the fleet decision                 owns the cluster
```

app-exposer resolves the analysis (DB, apps, quota, permissions), maps
`model.Job → VICESpec`, picks a target operator, and POSTs the spec. The
operator builds every k8s object from the spec, injecting cluster-specific
values at build time, and applies them. The line: **app-exposer owns *what*;
the operator owns *how*.**

## Field ownership

Every value needed to build the k8s objects comes from exactly one of three
owners. The contract is the set of fields where the owner is **app-exposer**
(they ride in `VICESpec`); the other two owners are recorded here so the
boundary is unambiguous.

### Owned by app-exposer — carried in `VICESpec`

These are analysis-intrinsic or DB-derived; the operator cannot compute them.
app-exposer resolves them (running the `model.Job` methods, where that knowledge
lives) so the operator receives primitives and needs no `model.Job` dependency.

| Field | Source | Notes |
| --- | --- | --- |
| `AnalysisID` | `job.ID` | canonical id; the cleanup label key |
| `ExternalID` | `job.InvocationID` | basis for every resource name |
| `JobName` | `job.Name` | becomes the `analysis-name` label; **required, non-empty** |
| `AppID` / `AppName` | `job.AppID` / `job.AppName` | labels |
| `UserID` / `Submitter` | `job.UserID` / `job.Submitter` | labels; operator applies `UserSuffix` to `Submitter` |
| `UserLoginIP` | `Apps.GetUserIP(ctx, job.UserID)` | **the one DB-derived input**; resolved once app-exposer-side |
| `Container.*` | `job.Steps[0].Component.Container` | image/tag/uid/ports/entrypoint/workdir; `Arguments` is resolved `Step.Arguments()` |
| `Environment` | `job.Steps[0].Environment` | |
| `Resources.*` | container resource asks | **raw, not clamped** — see Versioning note on policy ownership |
| `GPU` | `MinGPUs`/`MaxGPUs`/`GPUModels` (or legacy `/dev/nvidia*`) | `nil` ⇒ no GPU; vendor-neutral (see open item) |
| `UserHome` | `job.UserHome` | |
| `OutputDirectory` | resolved `job.OutputDirectory()` | |
| `ExcludeArguments` | resolved `job.ExcludeArguments()` | |
| `Inputs` | all `Steps[].Config.Inputs`, resolved | full list → CSI input volume mappings |
| `InputPathListPaths` | ticketless subset, resolved to `[]string` | input-path-list ConfigMap |
| `FileMetadata` | `job.FileMetadata` | porklock upload metadata triples |

The `subdomain` label is deliberately **not** carried: the operator recomputes
it from `UserID` + `ExternalID` via the shared `common.Subdomain`.

### Owned by the operator — cluster config, never on the wire

The cluster-specific `Incluster.Init` values the builders read today move to
operator config (field audit §5). The operator already holds peers of several
(`baseDomain`, `userSuffix`, `clusterConfigSecret`, `localStorageClass`, GPU
vendor/model fields, gateway refs, `imageRewriter`); the rest are added in
Phase 1 (`PorklockImage/Tag`, `ViceProxyImage`, `UseCSIDriver`,
`FrontendBaseURL`, `ViceDomain`, `IRODSZone`, `InputPathListIdentifier`, …).
The **`resourcing` default/clamp policy** is operator-owned too: the asks ride
raw in `ResourceSpec`, and the operator applies "what this cluster grants." The
**vice-proxy sidecar** is built entirely from operator config — it has *no*
`VICESpec` input at all (audit §4). The four contested namespace/secret values
(VICE namespace, backend/gateway namespace, image-pull secret, cluster-config
secret) are operator-owned; two are already overridden operator-side today
(audit §8.5).

### Static — constants that travel with the builder code

The ~30 `constants` names (file names, volume names, CSI names, port
names/numbers, label keys) are static; they move into `vicebuild/` with the
builders and never cross the wire (audit §6).

## Versioning

The bundle stops being self-describing k8s objects and becomes an explicit
app-exposer↔operator contract, so it must version.

- `VICESpec.SpecVersion` carries the wire-contract version.
  `operatorclient.CurrentVICESpecVersion` (currently **1**) is what this build
  emits and understands.
- **Bump `CurrentVICESpecVersion`** whenever `VICESpec` changes in a way an
  older operator could not faithfully build (new required field, changed
  semantics of an existing field). Purely additive *optional* fields that older
  operators can safely ignore do not require a bump, but when in doubt, bump.
- The operator advertises the max `SpecVersion` it supports (see scheduler
  rule). It **rejects** a spec whose version it cannot build with a clear error.
- Because both binaries ship in one image and release together, the common case
  is lockstep; `SpecVersion` exists only for the window where a newer
  app-exposer talks to an operator that hasn't rolled yet.

`VICESpec` is a **dedicated, minimal type, not a `model.Job` subset.** This
keeps the operator off `model`'s batch-oriented type and its version cadence,
documents exactly the VICE surface, and gives a stable reviewable contract. The
cost — an explicit `model.Job → VICESpec` mapping in app-exposer — is the right
place for that knowledge to live.

## Scheduler rule: "operator too old → skip"

The operator reports its max supported `SpecVersion` in the capacity/info
response (a `SpecVersion int` field; `0` = spec path unsupported). During
operator selection the scheduler treats version like any other fit check:

- operator supports the spec version app-exposer wants to send → send `VICESpec`;
- operator reports `0` / a version too low → **skip it**, exactly as it skips an
  at-capacity or vendor-incompatible operator. During the migration this means
  "fall back to building the legacy `AnalysisBundle` for that operator"; after
  the legacy path is deleted (Phase 6) it means the operator is simply
  ineligible until it rolls.

This is the single switch that lets the object path and the spec path coexist
and lets operators migrate one at a time. It is removed in Phase 6.

**Rollback lever.** A new operator advertises `CurrentVICESpecVersion` by
default. Starting it with `--disable-spec-launch` makes it advertise `0`
instead, so the scheduler routes it legacy bundles without an app-exposer
redeploy; the operator also rejects any spec that reaches `/analyses/spec`
directly with a transient 503 (so a stray spec is retried elsewhere). This is
the per-operator "flip back to object-mode" lever the migration plan calls for.

## Validation

`AnalysisBundle.Validate()` re-walked each built child object asserting its
`analysis-id` label matched, because cleanup keys on that label and app-exposer
built the objects. With construction operator-side the operator **guarantees
that invariant by construction** — it stamps the labels it builds. So
`VICESpec.Validate()` is reduced to the required scalar fields (`AnalysisID`,
`ExternalID`, `JobName`, `Submitter`, `Container.Image`); `SpecVersion`
compatibility is a separate check the receiver makes against the versions it
supports.

## GPU vendor matching — decided

`GPUSpec` carries an explicit, canonical **`Vendor`** field
(`operatorclient.GPUVendorNvidia` / `GPUVendorAMD`) alongside `Count` and
`Models`. The `model.Job` has no vendor field today, so app-exposer defaults it
to nvidia — matching today's behavior, where app-exposer always builds
`nvidia.com/gpu` (`resourcing.go:407,435`) and the operator's
`TransformGPUVendor` rewrites to AMD per cluster.

**Why a field rather than a hardcoded default.** We could have dropped vendor
from the spec and had the scheduler assume nvidia, or subsumed vendor under
model matching. Instead the vendor rides explicitly so multi-vendor scheduling
is real plumbing, not a future rewrite: the day an analysis can express AMD,
only this one field changes — the scheduler, the capacity match
(`CapacityResponse.GPUVendor`), and the operator's build path already read it.
An empty `Vendor` is read as nvidia for backwards compatibility.

**How the scheduler reads it.** `VICESpec.RequestedGPUVendor()` and
`RequestedGPUModels()` are the spec-side counterparts of
`AnalysisBundle.RequestedGPUVendor()` / `RequestedGPUModels()`. They return the
same values the object path produces (vendor defaulting to nvidia, models
verbatim) but read them directly from the spec instead of reverse-engineering
them from a built Deployment. In Phase 3 the scheduler switches to these for
spec-aware operators; the existing `vendorCompatible` / `modelCompatible` logic
and `CapacityResponse` matching are unchanged.

This keeps today's behavior exactly (vendor-neutral GPU analyses default to
nvidia and so still won't land on AMD clusters) while leaving a clean seam:
when AMD analyses become expressible, set `GPU.Vendor = GPUVendorAMD` and the
rest of the path already does the right thing.
