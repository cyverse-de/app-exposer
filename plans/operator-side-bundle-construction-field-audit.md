# Field-level audit: inputs to VICE k8s-object construction

Phase 0, step 1 of the
[migration plan](operator-side-bundle-construction-migration.md). This enumerates
**every** input the `incluster` builders read, so the `VICESpec` in the
[design doc](operator-side-bundle-construction.md) can be locked with nothing
analysis-intrinsic dropped.

Inputs sort into four kinds:
- **J — `model.Job` value/method** → must surface in `VICESpec`.
- **L — label input** (consumed by `jobInfo.JobLabels`) → surface in `VICESpec`;
  one is DB-derived.
- **C — `Incluster.Init` config** → moves to operator config (or already there).
- **K — `constants` package** → static; moves with the builder code, not on the wire.

Source files audited: `configmaps.go`, `services.go`, `pdb.go`,
`deployments.go`, `transfers.go`, `volumes.go`, `httproutes/{common,traefik}.go`,
`resourcing/resourcing.go`, `jobinfo/main.go`.

## Design principle this audit settles

The builders lean heavily on **computed `model.Job` methods** —
`OutputDirectory()`, `ExcludeArguments()`, `Step.Arguments()`,
`StepInput.IRODSPath()`, `FileMetadata.Argument()`, `Container.WorkingDirectory()`.
These encode DE semantics (iRODS base paths, `NowDate`/`DirectoryName`,
backwards-compat image detection, param ordering) that the operator has no
business re-deriving.

**Decision: `VICESpec` carries *resolved* values, not raw `model.Job` internals.**
app-exposer (where the model methods live) computes them; the operator receives
primitives. The single exception is **resource asks**, which stay raw because
clamping to cluster limits is a cluster policy the operator owns (see §4). This
is what keeps the operator free of a `model.Job` dependency — the strongest
argument for a dedicated `VICESpec` over shipping the job.

## 1. Identity & labels (J + L)

Every builder calls `jobInfo.JobLabels` (`jobinfo/main.go:24`), which stamps a
fixed label set on every resource. Its inputs:

| Label | Source field | Notes |
| --- | --- | --- |
| `external-id` | `job.InvocationID` | |
| `analysis-id` | `job.ID` | == bundle `AnalysisID`; cleanup key |
| `app-name` | `job.AppName` | via `common.LabelValueString` |
| `app-id` | `job.AppID` | |
| `username` | `job.Submitter` | via `common.LabelValueString` |
| `user-id` | `job.UserID` | |
| `analysis-name` | `job.Name` | truncated to 63 runes; **must not be empty** |
| `app-type` | — | constant `"interactive"` |
| `subdomain` | `common.Subdomain(job.UserID, job.InvocationID)` | pure fn; operator can recompute |
| `login-ip` | **`Apps.GetUserIP(ctx, job.UserID)`** | **DB-derived — the one (C)-class input** |

Plus naming/selector uses outside labels: `job.InvocationID` is the basis for
every resource name (`vice-<InvocationID>`, PVC names, route name) and the
`external-id` selector.

→ `VICESpec` fields: `AnalysisID` (=`job.ID`), `ExternalID` (=`InvocationID`),
`JobName` (=`Name`), `AppID`, `AppName`, `UserID`, `Submitter`, and resolved
`UserLoginIP`. `subdomain` need not be carried — the operator computes it from
`UserID`+`ExternalID` via the shared `common.Subdomain`.

## 2. The interactive container (J)

All under `job.Steps[0].Component.Container` unless noted.

| Input | Read at | → VICESpec |
| --- | --- | --- |
| `Image.Name` | deployments.go:200 | `Container.Image` |
| `Image.Tag` | deployments.go:201 | `Container.Tag` |
| `UID` | deployments.go:209/485, volumes.go:214-215 | `Container.UID` |
| `Ports[].ContainerPort` | deployments.go:25/220 | `Container.Ports []int` |
| `EntryPoint` | deployments.go:228-230 | `Container.EntryPoint` |
| `WorkingDir` / `WorkingDirectory()` | deployments.go:139/235-236 | `Container.WorkingDir` (resolved, default `/de-app-work`) |
| `Steps[0].Arguments()` | deployments.go:239-240 | `Container.Arguments []string` (resolved: `Executable()`+sorted params) |
| `Steps[0].Environment` | deployments.go:158 | `Environment map[string]string` |

Note `Steps[0].Arguments()` (`step.go:78`) folds in backwards-compat
image detection and param sorting — resolve app-exposer-side.

## 3. Data movement & file transfer (J)

| Input | Read at | → VICESpec |
| --- | --- | --- |
| `OutputDirectory()` | transfers.go:57, volumes.go:102 | `OutputDirectory string` (resolved) |
| `UserHome` | volumes.go:114-115 | `UserHome string` |
| `ExcludeArguments()` | configmaps.go:26 | `ExcludeArguments []string` (resolved) |
| `FilterInputsWithoutTickets()` | configmaps.go:105, transfers.go:89 | drives input-path-list ConfigMap + whether porklock input staging runs |
| all `Steps[].Config.Inputs` | volumes.go:60 (`getInputPathMappings`) | CSI input volume mappings — **uses ALL inputs, not just ticketless** |
| `StepInput.IRODSPath()` | configmaps.go:106, volumes.go:61 | resolved per-input path |
| `StepInput.Type` | volumes.go:64-68 | branches fileinput / multifileselector / folderinput |
| `FileMetadata` + `FileMetadata.Argument()` | transfers.go:61-62 | porklock metadata args |

**Subtlety, resolved (audit §8 items 1 & 2 — chased down):** the CSI path
(`getInputPathMappings`) consumes the **full** input list with `Type` and
resolved `IRODSPath`, while the input-path-list ConfigMap consumes only the
**ticketless** subset. But **ticket status never branches anything the operator
builds** — it is an iRODS access-control concern consumed by porklock/CSI at
transfer time, not in k8s objects. (`FilterInputsWithTickets()` is never called
anywhere in app-exposer; `TicketInputPathListIdentifier` is dead config — see §8.)

So rather than carry `Ticket` on the wire, app-exposer **resolves both views**:
the full input list for CSI mapping, and the ticketless path list for the
ConfigMap, as a ready `[]string`. `Multiplicity` is not needed separately —
`IRODSPath()` already appends a trailing `/` for collections, and `Type` drives
the CSI resource-type branch:

```go
type InputSpec struct {
    IRODSPath string // resolved StepInput.IRODSPath() (trailing "/" = collection)
    Type      string // fileinput | multifileselector | folderinput
}
```

→ on `VICESpec`: `Inputs []InputSpec` (all inputs, CSI mapping) +
`InputPathListPaths []string` (resolved ticketless subset, input-path-list
ConfigMap) + `FileMetadata []MetadataAVU{Attr,Value,Unit}` for the porklock
upload command. No `Ticket`/`HasTicket` anywhere — ticket handling is entirely
upstream of k8s-object construction.

## 4. Resource requirements (J) — the raw-vs-resolved exception

`resourcing.Requirements`, `GPUEnabled`, `GPUModelsRequested`,
`SharedMemoryAmount` read these off the container:

| Input | Read at | Meaning |
| --- | --- | --- |
| `MinCPUCores` / `MaxCPUCores` | resourcing.go:285/304 | CPU request / limit ask |
| `MinMemoryLimit` / `MemoryLimit` | resourcing.go:322/340 | memory request / limit ask |
| `MinDiskSpace` | resourcing.go:358, volumes.go:235 | disk ask (max across steps) |
| `MinGPUs` / `MaxGPUs` | resourcing.go:196 | GPU count ask |
| `GPUModels` | resourcing.go:388 | canonical model names |
| `Devices[].HostPath == /dev/nvidia*` | resourcing.go:199-200 | **legacy** GPU detection |
| `Devices[].HostPath == /dev/shm` + `ContainerPath` | resourcing.go:371-373 | shared-memory request (as quantity) |

`resourcing.Requirements` also applies **package-level default/clamp values**
(global setters configured at app-exposer startup). Those defaults are a
**cluster policy** ("what this cluster grants"), distinct from the analysis ask
("what the user wants"). Per the design principle, the asks ride in the spec raw
and the **operator owns the clamping defaults**:

```go
type ResourceSpec struct {
    MinCPUCores  float32
    MaxCPUCores  float32
    MinMemoryBytes int64
    MaxMemoryBytes int64
    MinDiskBytes int64
    SharedMemoryBytes *int64 // nil when no /dev/shm device
}
type GPUSpec struct { // nil on VICESpec when GPURequested is false
    Vendor string   // canonical GPUVendorNvidia | GPUVendorAMD; empty defaults to nvidia
    Count  int64
    Models []string // canonical, e.g. "NVIDIA-A10G"
}
```

Resolve legacy `/dev/nvidia` and modern `MinGPUs/MaxGPUs` into a single
`GPU *GPUSpec` (nil = none) app-exposer-side, so the operator never inspects
`Devices`. Likewise resolve `/dev/shm` into `SharedMemoryBytes`.

`GPUSpec.Vendor` is carried explicitly even though `model.Job` has no vendor
field (app-exposer defaults it to nvidia). This was a deliberate call to make
multi-vendor scheduling real plumbing rather than a future scheduler rewrite —
see the [contract doc](operator-side-bundle-construction-contract.md)'s GPU
section. The scheduler reads it via `VICESpec.RequestedGPUVendor()`.

**Decision (audit §8 item 4 — chased down): move the `resourcing` defaults to
the operator.** Investigation confirms these are genuinely cluster policy, and
the move is cheap because the `resourcing` package relocates *with* the builders
into `vicebuild/` — the clamping logic is not duplicated, it travels intact.
Findings:

- `resourcing/resourcing.go` holds **16 package-level defaults** — 7 for the
  analysis container (CPU req/limit, mem req/limit, storage req, two
  `do*Limit` toggles) and 9 for the vice-proxy sidecar — set via setters from
  **CLI flags in `cmd/app-exposer/main.go:215-230`** (e.g.
  `--default-cpu-resource-request` `1000m`, `--default-memory-resource-limit`
  `8Gi`). These are startup flags, not koanf/env keys, and represent
  "what this cluster grants," consistent with the per-cluster deployment model.
- `Requirements()` logic is "use the analysis ask if non-zero, else the default;
  apply limits only when the `do*` toggle is on." So the **asks** (`ResourceSpec`)
  ride in the spec raw; the **defaults/toggles** become operator config; the
  operator runs the same clamp.
- `VICEProxyRequirements()` **ignores the analysis entirely** — the vice-proxy
  sidecar's resources come 100% from cluster defaults. So the vice-proxy
  container is built fully operator-side with operator config and needs **no
  spec input at all**.

Consequence for the spec: `ResourceSpec` carries only the analysis asks below;
nothing about vice-proxy resources or default/clamp values crosses the wire.

## 5. `Incluster.Init` config → operator config (C)

Every config value the builders read, and whether the operator already holds an
equivalent (from the operator-config audit):

| `Init` field | Read at | Operator status |
| --- | --- | --- |
| `PorklockImage` | deployments.go:46/102/326 | **add** |
| `PorklockTag` | deployments.go:46/102/326 | **add** |
| `ViceProxyImage` | deployments.go:254 | **add** |
| `UseCSIDriver` | deployments.go:147, volumes.go:150/278/405 | **add** (cluster capability) |
| `FrontendBaseURL` | deployments.go:171 | **add** |
| `ViceDomain` | bundle.go:61 → httproutes | present (`baseDomain`) |
| `VICEBackendNamespace` | bundle.go:59 → httproutes (DENamespace) | present (`gatewayNamespace`) — reconcile |
| `ViceNamespace` | bundle.go:60, deployments.go:225/541 | operator owns (`o.namespace`; already overrides via `normalizeNamespaces` — §8.5) |
| `IRODSZone` | volumes.go:37/125 | **add** |
| `UserSuffix` | configmaps.go:71 | present (`userSuffix`) |
| `ImagePullSecretName` | deployments.go:378 | operator owns (`-image-pull-secret` / `EnsureImagePullSecret` — §8.5) |
| `ClusterConfigSecretName` | deployments.go:307 | present (`clusterConfigSecret`) |
| `GatewayProvider` | bundle.go:57 | implicit (operator owns its gateway) — make explicit |
| `InputPathListIdentifier` | configmaps.go:125 | **add** |
| `TicketInputPathListIdentifier` | — | **dead config; remove** (§8.1) |

Plus the `resourcing` package-level defaults (§4) and the storage class
(`TransformWorkingDirStorageClass` already uses operator `localStorageClass`).
The namespace and secret rows are settled in §8.5: the operator owns all of them
(two already override app-exposer's value today), so none cross the wire.

## 6. Constants (K)

The builders pull ~30 names from `constants` — file names (`ExcludesFileName`,
`PermissionsFileName`, `InputPathListFileName`), volume names
(`InputPathListVolumeName`, `ExcludesVolumeName`, `PermissionsVolumeName`,
`PorklockConfigVolumeName`, `SharedMemoryVolumeName`), CSI names
(`CSIDriverName`, `CSIDriverStorageClassName`, `CSIDriver*MountPath`,
`CSIDriverDataVolume*Prefix`), port names/numbers (`FileTransfersPort*`,
`VICEProxyPort*`), label keys, `ShmDevice`, `VICEGatewayName`. These are static
and travel **with the builder code** into `vicebuild/`; none belong on the wire.
The `constants` package is already importable by `operator/`.

## 7. Consolidated `VICESpec` (audit-backed)

```go
type VICESpec struct {
    SpecVersion int

    // Identity / labels (§1)
    AnalysisID  AnalysisID // job.ID
    ExternalID  ExternalID // job.InvocationID
    JobName     string     // job.Name (label "analysis-name"; required, non-empty)
    AppID       string
    AppName     string
    UserID      string
    Submitter   string
    UserLoginIP string     // resolved via Apps.GetUserIP — the only DB-derived input

    // Container (§2)
    Container   ContainerSpec
    Environment map[string]string

    // Resources (§4)
    Resources ResourceSpec
    GPU       *GPUSpec // nil = none

    // Data movement (§3)
    UserHome           string
    OutputDirectory    string
    ExcludeArguments   []string
    Inputs             []InputSpec   // all inputs — CSI volume mappings
    InputPathListPaths []string      // resolved ticketless subset — input-path-list ConfigMap
    FileMetadata       []MetadataAVU
}

type ContainerSpec struct {
    Image      string
    Tag        string
    UID        int
    Ports      []int
    EntryPoint string
    WorkingDir string   // resolved (default /de-app-work)
    Arguments  []string // resolved Step.Arguments()
}

type MetadataAVU struct{ Attribute, Value, Unit string }
// InputSpec, ResourceSpec, GPUSpec: see §3, §4.
```

Everything the builders read maps to a field above, to operator config (§5), or
to a constant that moves with the code (§6). The lone input that cannot be
derived cluster-side is `UserLoginIP`, threaded as a resolved value — confirming
the design doc's central claim with no remaining unknowns at the field level.

## 8. Gaps / items to confirm before locking the schema

**Resolved:**

1. ~~**Ticketed inputs.**~~ *Resolved (§3).* Ticket status never branches
   anything the operator builds. `FilterInputsWithTickets()` is never called in
   app-exposer; `TicketInputPathListIdentifier` is **dead config** — defined on
   the `Incluster` struct (`incluster.go:43`), set from
   `tickets_path_list.file_identifier` (`app.go:116`), referenced only in
   `deployments_test.go:24`, never read by a builder. Tickets are an iRODS
   access-control concern consumed by porklock/CSI at transfer time. The spec
   carries no ticket data. **Cleanup opportunity (independent of this project):**
   remove `TicketInputPathListIdentifier` and its flag as dead code.
2. ~~**`InputSpec.Multiplicity`.**~~ *Resolved (§3).* Not needed —
   `IRODSPath()` already encodes collection-ness via a trailing `/`, and `Type`
   drives the CSI branch. `InputSpec` is just `{IRODSPath, Type}`.
4. ~~**`resourcing` defaults ownership.**~~ *Resolved (§4): move them to the
   operator.* They are cluster policy, the package relocates with the builders so
   no logic is duplicated, and `VICEProxyRequirements` needs no spec input at all.

3. ~~**Multi-step assumption.**~~ *Resolved: `VICESpec` models one container.*
   VICE is single-step by invariant, not by accident: an explicit comment
   (`incluster/analyses.go:10-12`, "VICE analyses only have a single step in the
   database"), `ConvertToJob` hardcoding `Steps: []model.Step{step}`
   (`cmd/vicetools/convert.go:183`), and tests asserting `require.Len(job.Steps,
   1)` (`convert_test.go`) all confirm it. Every builder already indexes
   `Steps[0]`; the two step-iterating helpers
   (`getInputPathMappings`, `getPersistentVolumeCapacity`) loop over a length-1
   slice. And because app-exposer resolves inputs (`Inputs`,
   `InputPathListPaths`) and disk capacity (`Resources.MinDiskBytes`) into flat
   spec fields, **the operator never iterates steps at all.** Contrast: `batch/`
   genuinely iterates multi-step (`batch.go:77/257/909`) — that's the HPC path,
   not VICE.
5. ~~**Namespace/secret name reconciliation.**~~ *Resolved: the operator owns all
   four; none go in the spec.* Two are **already** overridden operator-side today,
   so dropping them is zero-risk:
   - **VICE namespace** — `normalizeNamespaces` (`operator/resources.go:56-86`)
     unconditionally rewrites every resource's namespace to `o.namespace` before
     apply. App-exposer's `ViceNamespace` is already dead weight.
   - **Backend/gateway namespace** — `TransformGatewayNamespace`
     (`operator/transforms.go`) unconditionally overwrites the HTTPRoute
     `parentRef`. App-exposer's `VICEBackendNamespace` is already overridden.
   - **Image pull secret** — operator already ensures it
     (`EnsureImagePullSecret`, `-image-pull-secret`); on construction-move the
     operator sets `Deployment.ImagePullSecrets` from its own name (today
     app-exposer sets it on a separate path — a latent mismatch this removes).
   - **Cluster config secret** — operator already holds `clusterConfigSecret` and
     ensures the `EnvFrom`; it sets it at build time.
6. ~~**`common` helper importability.**~~ *Resolved: clean, after one refactor.*
   `common`, `constants`, `resourcing` import into the operator without cycles
   (operator already uses `common`/`constants`; `resourcing` depends only on
   those two; operator does not import `incluster`, and vice versa). The one snag:
   `incluster/httproutes` imports `incluster/jobinfo`, which pulls in `apps` (DB)
   — but **only** because the route builder calls `JobLabels`. Once labels are
   resolved app-exposer-side and passed in, the moved builder takes the label map
   as input and drops the `jobinfo` import; `jobinfo`/`apps` stay in app-exposer
   with the DB-coupled `GetUserIP`. See §9.

## 9. Package layout and the label split (from §8.6)

The import audit yields a clean target layout. The current direction is
acyclic — `operator/` does not import `incluster/`, and `incluster/` does not
import `operator/` — so a new `vicebuild/` imported by `operator/` introduces no
cycle. The only thing standing in the way is how labels are produced.

**Today:** every builder calls `jobInfo.JobLabels(ctx, job)`, which both
assembles a fixed label map *and* makes the DB call `Apps.GetUserIP`
(`jobinfo/main.go:24`). The label map assembly is pure (`common.LabelValueString`
+ `common.Subdomain` over job fields); only `GetUserIP` touches the DB. Because
the interface and its DB-coupled implementation live in the same
`incluster/jobinfo` package, importing the route builder drags `apps` along.

**Target:** split the two halves along the line that already exists in the data —
the DB call vs. the pure assembly.

- **app-exposer keeps** `jobinfo`/`apps` and resolves `UserLoginIP` (the one DB
  value) into the `VICESpec`, exactly as §1 requires.
- **`vicebuild/` gets a pure `BuildLabels(spec) map[string]string`** — the body
  of `JobLabels` minus the `GetUserIP` call, reading the resolved spec fields
  (`AnalysisID`, `ExternalID`, `AppID`, `AppName`, `UserID`, `Submitter`,
  `JobName`, `UserLoginIP`) and using `common.LabelValueString`/`common.Subdomain`.
  Every moved builder (Deployment, Service, HTTPRoute, ConfigMaps, PV/PVC, PDB)
  calls this instead of an injected `JobInfo`.

This removes the `httproutes → jobinfo → apps` edge, so `vicebuild/` depends only
on `common`, `constants`, `resourcing`, `model/v10`, and the k8s/gateway
libraries — all cleanly importable by `operator/`. Proposed layout:

```
vicebuild/                 # operator-importable; no apps/db
  labels.go                # BuildLabels(spec) — pure, from jobinfo.JobLabels
  deployments.go services.go configmaps.go volumes.go pdb.go
  httproutes/              # moved from incluster/, jobinfo dependency removed
resourcing/                # unchanged; relocate defaults/setters to operator main
operator/                  # imports vicebuild/, builds from VICESpec
incluster/                 # keeps jobinfo/apps + the model.Job → VICESpec mapping
```

`incluster/jobinfo`'s `JobInfo` interface can stay where it is — once the builders
no longer depend on it, nothing in `vicebuild/`/`operator/` imports it, so no
separate interface-extraction package is needed.
