# Operator-side bundle construction: a cluster-agnostic analysis bundle

## Context

Today app-exposer builds **fully-formed Kubernetes objects** and ships them to
vice-operator, which applies them to its local cluster. The wire contract is
`operatorclient.AnalysisBundle` (`operatorclient/types.go:27`):

```go
type AnalysisBundle struct {
    AnalysisID             AnalysisID
    Deployment             *appsv1.Deployment
    Service                *apiv1.Service
    HTTPRoute              *gatewayv1.HTTPRoute
    ConfigMaps             []*apiv1.ConfigMap
    PersistentVolumes      []*apiv1.PersistentVolume
    PersistentVolumeClaims []*apiv1.PersistentVolumeClaim
    PodDisruptionBudget    *policyv1.PodDisruptionBudget
}
```

Every field except `AnalysisID` is a raw k8s API type. App-exposer's
`incluster.BuildAnalysisBundle` (`incluster/bundle.go:14`) assembles them from a
`model.Job` plus a dozen cluster-config values held on the `Incluster` struct.

The problem this design addresses: **app-exposer has to know the shape of every
target cluster's Kubernetes environment.** It bakes in the Gateway API version,
the GPU vendor's resource names and node-label scheme, the storage class, the
image registry, the gateway reference, the iRODS CSI layout, and so on. As the
fleet grows to clusters that differ in these dimensions (the `multi-cluster`
work), app-exposer becomes a chokepoint: a cluster that runs a newer Gateway API
version, a different GPU label convention, or AMD instead of NVIDIA forces a
change in app-exposer even though nothing about the *analysis* changed.

The codebase is already telling us where the seam belongs. vice-operator runs a
**transform layer** at launch (`operator/handlers.go:257`) that rewrites the
received objects with cluster-local values *after* app-exposer built them with
cluster-agnostic placeholders:

| Transform | What it patches in |
| --- | --- |
| `TransformHostnames` | HTTPRoute hostname → cluster `baseDomain` |
| `TransformGatewayNamespace` | HTTPRoute `parentRef` → cluster gateway |
| `TransformBackendToLoadingService` | backend → loading-page service |
| `TransformGPUModels` | canonical GPU model → cluster node-label values |
| `TransformGPUVendor` | `nvidia.com/gpu` ↔ `amd.com/gpu`, affinity key |
| `TransformWorkingDirStorageClass` | working-dir PVC → cluster storage class |
| `TransformViceProxyArgs` | per-analysis vice-proxy args + cluster config secret |
| `TransformImageRefs` | upstream image → mirrored ref (manual-mirror mode) |
| `EnsurePermissionsConfigMap` | seed permissions ConfigMap |

This is a "build it wrong, then fix it" dance. Each transform exists *only*
because app-exposer set a value it couldn't actually know. If the operator built
the objects, every one of these would collapse into setting the right value the
first time.

Two more facts make the operator the natural home for construction:

- **vice-operator is already a full k8s resource builder**, not a dumb applier.
  It constructs NetworkPolicies (`operator/networkpolicy.go`), Gateways and
  HTTPRoutes (`operator/gateway.go`), the loading-page Service
  (`operator/loadingservice.go`), Secrets (`operator/secrets.go`), and image
  caches (`operator/imagecache_*.go`) from scratch. It has the clientset, the
  Gateway client, and all cluster-specific config.
- **vice-operator has zero external dependencies** — no DB, no apps service, no
  permissions service, no quota service. It is purely an in-cluster component.
  That constraint is the one real design force (see "The label/IP dependency").

## Goal

Replace the k8s-object `AnalysisBundle` with a **higher-level, cluster-agnostic
analysis spec**. app-exposer sends *what the analysis is*; vice-operator decides
*how to realize it on this cluster* and applies the result.

### Non-goals

- Changing the batch/Argo (`adapter/`, `batch/`) path. This is VICE-only.
- Changing the legacy `outcluster/` HTCondor path.
- Changing the operator's own self-managed resources (gateway, loading service,
  image cache, egress policy) — those are already operator-built and stay.
- Moving permissions or quota enforcement into the operator. Those run in
  app-exposer *before* launch and are not part of bundle construction.

## Why this is feasible

The inputs to today's builders sort into three buckets:

**(A) Analysis-intrinsic** — everything comes off `model.Job`: container image,
ports, UID, entrypoint, working dir, args, environment, resource requests, GPU
requests, input/output paths, `UserHome`, `Submitter`, invocation ID, exclude
arguments, app metadata. This is already a higher-level description; it just
needs a wire shape.

**(B) Cluster-specific config** — the `Incluster.Init` fields the builders read
(`incluster/incluster.go`): `PorklockImage`/`PorklockTag`, `ViceProxyImage`,
`UseCSIDriver`, `FrontendBaseURL`, `ViceDomain`, `VICEBackendNamespace`,
`ViceNamespace`, `IRODSZone`, `UserSuffix`, `ImagePullSecretName`,
`ClusterConfigSecretName`, `GatewayProvider`, `InputPathListIdentifier`. These
*are* cluster-specific, so they belong operator-side. The operator already holds
peers of several of them (`baseDomain`, `userSuffix`, `clusterConfigSecret`,
`localStorageClass`, GPU fields, gateway refs, `imageRewriter`).

**(C) app-exposer infrastructure** — exactly one thing: the user's login IP,
fetched from the jobs DB via `jobInfo.JobLabels` → `apps.GetUserIP`
(`apps/apps.go:133`) and stamped into resource labels. This is the only build
input the operator cannot get on its own.

So there is no hard blocker. (A) becomes the bundle. (B) moves to operator
config. (C) is threaded through the bundle as a resolved value.

## Design

### Division of responsibility

```
app-exposer                                vice-operator
-----------                                -------------
- resolve analysis (DB, apps, quota,       - receive VICESpec
  permissions)                             - build all k8s objects from spec
- enforce quota / permissions              - inject cluster-specific values at
- pick a target operator (scheduler)         build time (the old transforms,
- build VICESpec from model.Job              now construction)
- resolve DB-derived values (user IP)      - apply via upsert
- POST VICESpec to chosen operator         - build egress NetworkPolicy (already)
                                           - capacity check (already)
```

The line is clean: **app-exposer owns the analysis and the fleet decision; the
operator owns the cluster.**

### The wire contract: `VICESpec`

Replace `AnalysisBundle` with a spec describing the analysis. The shape below is
the initial sketch; the **field-level audit**
([operator-side-bundle-construction-field-audit.md](operator-side-bundle-construction-field-audit.md))
hardens it against every input the builders actually read and is the
authoritative schema. Two things the audit settled that this sketch did not:
the spec carries **resolved** values (computed app-exposer-side, where the
`model.Job` methods live) rather than raw job internals — that is what keeps the
operator free of a `model.Job` dependency — and **raw resource asks** ride on the
wire while the `resourcing` clamp/default policy moves operator-side (see the
audit's §4 open decision).

```go
// VICESpec is the cluster-agnostic description of a VICE analysis that
// app-exposer sends to a vice-operator. The operator turns this into the
// concrete k8s objects for its cluster.
type VICESpec struct {
    AnalysisID  AnalysisID        // analysis UUID (canonical id, drives labels)
    ExternalID  ExternalID        // a.k.a. InvocationID
    SpecVersion int               // wire-contract version; see "Versioning"

    // Identity / ownership
    UserID    string
    Submitter string              // raw username (operator applies UserSuffix)
    UserHome  string

    // The interactive container (model.Job.Steps[0].Component.Container)
    Container ContainerSpec

    // Environment for the analysis container
    Environment map[string]string

    // Resource requests, already normalized to canonical units
    Resources ResourceSpec        // CPU millicores, memory bytes, min disk
    GPU       *GPUSpec            // nil when no GPU requested

    // Data movement
    Inputs          []InputSpec    // FilterInputsWithoutTickets
    OutputDirectory string
    ExcludeArguments []string

    // App metadata (labels / display)
    AppID   string
    AppName string
    JobName string

    // Resolved values app-exposer must supply because the operator cannot
    // (DB-derived). See "The label/IP dependency".
    UserLoginIP string
}

type ContainerSpec struct {
    Image       string
    Tag         string
    Ports       []int
    UID         int64
    EntryPoint  string
    WorkingDir  string
    Arguments   []string
    MinDiskSpace int64
}

type ResourceSpec struct {
    CPUMillicores int64
    MemoryBytes   int64
    MinDiskBytes  int64
}

// GPUSpec is canonical and vendor-neutral. The operator maps Vendor +
// Models onto its own resource names and node-label scheme — exactly what
// TransformGPUVendor / TransformGPUModels do today.
type GPUSpec struct {
    Vendor string   // "nvidia" | "amd" (canonical)
    Models []string // canonical GFD names, e.g. "NVIDIA-A10G"
}

type InputSpec struct {
    IRODSPath string // resolved StepInput.IRODSPath() (trailing "/" = collection)
    Type      string // fileinput | multifileselector | folderinput
}
// VICESpec also carries InputPathListPaths []string — the resolved ticketless
// subset for the input-path-list ConfigMap. Ticket status itself never crosses
// the wire (audit §3/§8). The authoritative schema is the field audit.
```

The operator's builders consume `VICESpec` instead of `model.Job` and produce
the same Deployment / Service / HTTPRoute / ConfigMaps / PVs / PVCs / PDB it
applies today — but with cluster-correct values baked in, so the transform layer
is gone.

### What happens to the transforms

Each transform becomes a build-time decision in the operator, not a
post-hoc patch:

| Today (transform on received object) | After (build-time in operator) |
| --- | --- |
| `TransformHostnames` | HTTPRoute built with `o.baseDomain` directly |
| `TransformGatewayNamespace` | `parentRef` built with `o.gatewayName/Namespace` |
| `TransformBackendToLoadingService` | backend built pointing at loading svc |
| `TransformGPUModels` / `TransformGPUVendor` | affinity + resource names built from `o.gpuVendor` / `o.gpuModelMapping` and `GPUSpec` |
| `TransformWorkingDirStorageClass` | PVC built with `o.localStorageClass` |
| `TransformViceProxyArgs` | vice-proxy container built with the args + secret |
| `TransformImageRefs` | image refs resolved through `o.imageRewriter` at build |
| `EnsurePermissionsConfigMap` | permissions ConfigMap always built |

The transform package and its tests do not disappear so much as **migrate into
the builders** — the logic is the same, only its position in the pipeline moves
from "after deserialize" to "during construct."

### The label/IP dependency

`jobInfo.JobLabels` decorates every resource with labels, one of which is the
user's login IP from the jobs DB. The operator has no DB and must not get one.

**Decision: app-exposer resolves it once and puts it in the spec
(`VICESpec.UserLoginIP`).** app-exposer already calls this during the build, so
this is a move, not new work. The operator applies the same label set it does
today, sourcing the IP from the spec instead of a DB call. If a future label
needs another DB-derived value, it follows the same path: resolve in
app-exposer, carry in the spec.

(Alternative considered: give the operator a narrow read endpoint to fetch user
IP. Rejected — it reintroduces an app-exposer→operator runtime coupling and a
new auth surface for one string. Threading the value is strictly simpler and
keeps the operator dependency-free.)

### Scheduler / operator-selection impact

This is the subtlety most likely to bite. app-exposer's scheduler picks a target
operator partly by GPU fit, and it does so today by **inspecting the built
Deployment**:

- `AnalysisBundle.RequestedGPUVendor()` reads container resource requests
  (`operatorclient/types.go:105`)
- `AnalysisBundle.RequestedGPUModels()` reads node-affinity match expressions
  (`operatorclient/types.go:139`)

With no Deployment in the bundle, these must read from `VICESpec.GPU` instead —
which is strictly easier and more direct (the data is explicit rather than
reverse-engineered from k8s objects). `CapacityResponse.GPUVendor` /
`SupportedGPUModels` matching is unchanged; only the *requested* side moves from
"parse the Deployment" to "read the spec field." Net simplification.

### Validation

`AnalysisBundle.Validate()` today walks each child object asserting the
analysis-id label matches (so cleanup can find them). With construction
operator-side, **the operator guarantees that invariant by construction** — it
stamps the labels it builds. Validation shifts to spec-shape checks
(required fields: AnalysisID, image, submitter, …) on receipt, plus the
operator's existing capacity gate.

### Code / package movement

Both binaries ship from one Go module and one Docker image, so this is internal
restructuring, not a cross-repo split:

- The `incluster/` builders that are pure k8s construction —
  `deployments.go`, `services.go`, `httproutes/`, `configmaps.go`,
  `volumes.go`, `pdb.go` — plus their helpers (`resourcing`, `millicores`,
  porklock/iRODS volume-mapping) move to (or are shared with) the operator. A
  reasonable home is a new `vicebuild/` package importable by `operator/`. The
  import audit confirms this is cycle-free: `operator/` does not import
  `incluster/` (nor vice versa), and `common`/`constants`/`resourcing` are clean
  leaf dependencies (field audit §9).
- One decoupling is required: today every builder calls `jobInfo.JobLabels`,
  which both assembles labels (pure) *and* calls the DB (`Apps.GetUserIP`). Split
  it — app-exposer resolves `UserLoginIP` into the spec (as §1 requires), and
  `vicebuild/` gets a pure `BuildLabels(spec)` (the assembly minus the DB call).
  That removes the `httproutes → jobinfo → apps` edge so `vicebuild/` carries no
  DB/apps dependency. The `resourcing` defaults/setters relocate to the
  operator's `main` (field audit §4, §9).
- app-exposer keeps the *resolution* half of `incluster/`: DB access (`jobinfo`/
  `apps`), quota/permissions clients, the scheduler, and a new small
  `BuildVICESpec` that maps `model.Job` → `VICESpec`.
- `model.Job` (from `cyverse-de/model/v10`) is importable by both, but the
  operator should depend on `VICESpec`, **not** `model.Job` — see Versioning.

## Versioning and rollout

The bundle stops being "k8s objects" (self-describing, schema-stable) and
becomes an explicit app-exposer↔operator contract. That contract must version.

- Add `SpecVersion` to `VICESpec`. The operator rejects versions it doesn't
  understand with a clear error, and the scheduler can treat "operator too old
  for this spec version" as "skip this operator," same as a capacity miss.
- Because both binaries ship in the same image and are released together, the
  common case is lockstep. `SpecVersion` exists for the window where a newer
  app-exposer talks to an operator that hasn't rolled yet.

**Recommended: define `VICESpec` as a dedicated, minimal type** rather than
shipping a slice of `model.Job`. Reasons: a stable, reviewable contract; the
operator does not get coupled to the full batch-oriented job model and its
version cadence; and the spec documents exactly the VICE surface. The cost is
an explicit `model.Job → VICESpec` mapping in app-exposer, which is
straightforward and is the right place for that knowledge to live.

(Intermediate option, if we want to de-risk in stages: first ship the relevant
`model.Job` subset as the wire type to prove the operator-builds path end to
end, then tighten to `VICESpec`. Noted as a possibility for the migration plan;
not the recommended end state.)

## Risks and open questions

1. **Test surface moves with the code.** The builder tests
   (`incluster/*_test.go`) move to the operator. The transform tests fold into
   builder tests. This is mechanical but sizable; the migration plan must keep
   coverage continuous, not "big-bang then re-test."

2. **`VICESpec` completeness.** Addressed by the field-level audit
   ([operator-side-bundle-construction-field-audit.md](operator-side-bundle-construction-field-audit.md)),
   which maps every builder input to a spec field, operator config, or a
   constant. All six follow-up items are now resolved (audit §8): ticketed inputs
   carry no spec data (and `TicketInputPathListIdentifier` is dead config);
   `InputSpec` needs no `Multiplicity`; the `resourcing` defaults move to the
   operator; `VICESpec` models a single interactive container (VICE is single-step
   by invariant); the operator owns all four contested namespace/secret values
   (two already override app-exposer's today); and `vicebuild/` imports cleanly
   into the operator once label assembly is split from the DB call (§9 of the
   audit). No open schema questions remain.

3. **Config duplication during transition.** Several `Init` fields must exist on
   *both* sides until the cutover completes (app-exposer still builds for
   not-yet-migrated operators). The migration plan needs a flag or per-operator
   capability to choose "send objects" vs "send spec."

4. **iRODS / CSI volume layout is genuinely cluster-specific** and already half
   operator-side (`TransformWorkingDirStorageClass`, `localStorageClass`). The
   PV/PVC builders read `IRODSZone` and `UseCSIDriver` from app-exposer today;
   confirm the operator is the right owner of *all* of that (it almost certainly
   is — storage topology is a cluster property) and that nothing in the iRODS
   path needs DB lookups.

5. **Observability of failures shifts.** A malformed analysis currently fails in
   app-exposer where the operator/admin tooling and logs are richest. After the
   move, build failures happen operator-side; the operator's error responses and
   logs must carry enough context (probable cause) for triage from app-exposer's
   launch path.

## Summary

This is a refactor along a seam the codebase already cut. vice-operator is
already a cluster-aware k8s builder with no external dependencies and an existing
transform layer whose entire reason for being is to fix up values app-exposer
couldn't know. Moving construction into the operator collapses that transform
layer into construction, removes app-exposer's knowledge of per-cluster
Kubernetes specifics, and makes the fleet genuinely heterogeneous-cluster-ready.
The only input that cannot move (the DB-derived user login IP) is threaded
through the spec. The main work is mechanical (relocate builders, migrate config,
map `model.Job → VICESpec`) and the main decisions are the spec schema and the
contract versioning strategy.
