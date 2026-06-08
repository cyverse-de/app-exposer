# Migration plan: operator-side bundle construction

Companion to [operator-side-bundle-construction.md](operator-side-bundle-construction.md).
That doc is the *what* and *why*; this is the *how*, sequenced so the fleet keeps
launching analyses at every step and coverage never drops.

## Guiding constraints

- **No big-bang.** At every commit the system launches analyses correctly. The
  old k8s-object path and the new spec path coexist until the fleet is fully
  cut over.
- **Per-operator opt-in.** A given operator either understands `VICESpec` or it
  doesn't. app-exposer chooses the path per target operator, so operators can
  roll independently even though both binaries ship in one image.
- **Coverage moves with code.** When a builder moves, its tests move with it in
  the same change — never "relocate now, re-test later."
- **Decisions before mechanics.** Lock the spec schema and the versioning story
  (Phase 0) before writing builder code against it.

## Capability negotiation

The operator already reports `CapacityResponse` from its capacity endpoint. Add
a `SpecVersion int` (max supported `VICESpec` version, `0` = spec path
unsupported) to that response, or to a dedicated operator-info endpoint if we'd
rather not overload capacity. The scheduler reads it during operator selection:

- target supports the spec → app-exposer sends `VICESpec`.
- target is spec-unaware (`0`) → app-exposer builds the legacy `AnalysisBundle`.

This is the single switch that lets the two paths coexist and lets operators
migrate one at a time. It is removed in Phase 6.

## Phase 0 — Lock the contract (no behavior change)

**Goal:** agree on `VICESpec` and the versioning rules before any builder moves.

1. **Field-level audit.** *Done* —
   [operator-side-bundle-construction-field-audit.md](operator-side-bundle-construction-field-audit.md)
   enumerates every builder input (J/L/C/K), maps each to a `VICESpec` field,
   operator config, or a constant, and proposes the audit-backed schema. All six
   follow-up items are now closed (audit §8): ticketed inputs (no spec data;
   `TicketInputPathListIdentifier` is dead code), `InputSpec.Multiplicity` (not
   needed), `resourcing` ownership (operator), single-step (one container),
   namespace/secret reconciliation (operator owns all four), and importability
   (cycle-free given the `BuildLabels` split, audit §9). No open schema questions
   remain; this phase reduces to writing the `VICESpec` types and the contract
   doc.
2. **Define `VICESpec`** (and `ContainerSpec`/`ResourceSpec`/`GPUSpec`/
   `InputSpec`) in `operatorclient/` next to the existing types. Add
   `SpecVersion`. No one builds or consumes it yet.
3. **Write the contract doc** —
   [operator-side-bundle-construction-contract.md](operator-side-bundle-construction-contract.md):
   which side owns which field, the versioning policy, and the "operator too old
   → skip" scheduler rule. It also records the one decision deferred to Phase 3
   (GPU vendor matching, now that `GPUSpec` carries no vendor).

**Exit:** `VICESpec` compiles, is reviewed, and the audit checklist is complete.
No runtime change. This is the natural review-with-coworkers checkpoint.

*Status: done — `VICESpec` and friends are defined in
`operatorclient/vicespec.go` (with `Validate()` and round-trip tests), and the
contract doc is written.*

## Phase 1 — Operator can build from a spec (dark, behind capability)

**Goal:** the operator gains the ability to construct k8s objects from
`VICESpec`, exercised only by tests.

1. **Create `vicebuild/`** (importable by `operator/`). Move the pure
   construction code into it: `deployments.go`, `services.go`, `httproutes/`,
   `configmaps.go`, `volumes.go`, `pdb.go`, and helpers (`resourcing`,
   `millicores`, porklock/iRODS mapping). Change their inputs from `model.Job`
   to `VICESpec`. **Move the matching tests in the same change.**
2. **Fold the transforms into construction.** Each builder takes the operator's
   cluster config and sets cluster-correct values directly:
   - hostname/gateway → `o.baseDomain`, `o.gatewayName/Namespace`
   - backend → loading service
   - GPU vendor/model → `o.gpuVendor`, `o.gpuModelMapping`, `GPUSpec`
   - storage class → `o.localStorageClass`
   - vice-proxy args + cluster config secret
   - image refs → `o.imageRewriter`
   - permissions ConfigMap always built
   Port each `Transform*` test into the corresponding builder test, asserting the
   built object already has the right value (no separate transform step).
3. **Thread operator config.** Add to operator config the cluster-specific
   `Init` fields it doesn't already hold: `PorklockImage/Tag`, `ViceProxyImage`,
   `UseCSIDriver`, `FrontendBaseURL`, `ViceDomain`, `VICEBackendNamespace`,
   `ViceNamespace`, `IRODSZone`, `ImagePullSecretName`, `GatewayProvider`,
   `InputPathListIdentifier`, `TicketInputPathListIdentifier`. (`baseDomain`,
   `userSuffix`, `clusterConfigSecret`, `localStorageClass`, GPU fields, gateway
   refs, `imageRewriter` already exist.)
4. **Golden-equivalence test.** For a representative set of jobs, assert that
   `vicebuild` from a `VICESpec` produces objects equivalent to today's
   app-exposer build + operator transforms. This is the safety net for the whole
   migration — it proves the new path matches the old before anything live uses
   it.

**Exit:** operator builds correct objects from `VICESpec` in tests; no
production traffic uses it yet.

## Phase 2 — Operator accepts a spec on the wire (still unused by app-exposer)

**Goal:** the operator's launch endpoint can receive `VICESpec`, build, and apply.

1. Add a spec-accepting launch path in the operator (new endpoint, or
   content-negotiated on the existing one). It validates the spec shape and
   `SpecVersion`, calls `vicebuild`, then reuses the **existing**
   `applyBundle` / egress-policy / labeling code unchanged — only the *source* of
   the objects differs.
2. Report `SpecVersion` in the capacity/info response.
3. Operator integration tests against a fake clientset: POST `VICESpec` →
   correct objects applied, labels intact, egress policy created.

**Exit:** a deployed operator advertises spec support and works when called with
a spec — but app-exposer still sends objects.

## Phase 3 — app-exposer maps `model.Job → VICESpec`

**Goal:** app-exposer can produce a spec; choose the path per operator.

1. **`BuildVICESpec(ctx, job, analysisID)`** in app-exposer (the resolution
   half of the old `incluster`): map `model.Job` → `VICESpec`, including
   resolving `UserLoginIP` once (the only DB-derived field) via the existing
   `apps.GetUserIP` path.
2. **Scheduler decides per target:** spec-aware operator → send `VICESpec` via a
   new `operatorclient` `LaunchSpec`; spec-unaware → keep
   `BuildAnalysisBundle` + `Launch`.
3. **Move GPU selection to the spec.** Replace scheduler use of
   `AnalysisBundle.RequestedGPUVendor/RequestedGPUModels` (which parse the
   Deployment) with reads of `VICESpec.GPU`. Keep the old methods only for the
   legacy path until Phase 6.

**Exit:** app-exposer sends specs to spec-aware operators and objects to the
rest, chosen automatically.

## Phase 4 — Roll the fleet onto the spec path

**Goal:** every operator advertises and uses spec support in production.

1. Deploy the new image fleet-wide (operators begin advertising `SpecVersion`).
2. As each operator reports support, the scheduler automatically routes specs to
   it — no app-exposer change needed per operator.
3. **Monitor:** launch success rate, build-error logs (now operator-side — see
   design-doc risk 5), and the golden-equivalence canary. Watch GPU clusters
   (NVIDIA and AMD) and CSI/iRODS clusters specifically, since those exercised
   the most transforms.

**Exit:** all production operators serve the spec path; the legacy path receives
no traffic.

## Phase 5 — Soak

Leave both paths in place for a defined soak window (a few release cycles).
Keep the legacy `BuildAnalysisBundle` + transforms compiled and tested so a
single operator can fall back if a cluster surfaces a build discrepancy. This is
the cheap insurance interval before deletion.

## Phase 6 — Delete the legacy path

Once no operator has used object-mode for the full soak window:

1. Remove `BuildAnalysisBundle`, the `AnalysisBundle` k8s-object fields, the
   operator's object-accepting launch path, and the now-dead `Transform*`
   functions (their logic lives in `vicebuild`).
2. Remove the per-operator capability switch and the scheduler's dual-path
   branch; `VICESpec` is the only contract.
3. Remove `RequestedGPUVendor/RequestedGPUModels` Deployment-parsing helpers.
4. Trim `Incluster.Init` of the cluster-specific build fields that now live only
   on the operator.

**Exit:** one contract (`VICESpec`), one build path (operator-side), app-exposer
free of per-cluster Kubernetes knowledge.

## Sequencing at a glance

| Phase | Touches | Live traffic on new path | Reversible by |
| --- | --- | --- | --- |
| 0 Lock contract | `operatorclient/` types, docs | no | n/a |
| 1 Operator builds (dark) | new `vicebuild/`, operator config, tests | no | revert package |
| 2 Operator accepts spec | operator endpoint | no | feature off |
| 3 app-exposer maps + routes | app-exposer scheduler, `BuildVICESpec` | spec-aware ops | capability switch |
| 4 Roll fleet | deploy only | all ops | capability switch |
| 5 Soak | none | all ops | capability switch |
| 6 Delete legacy | remove old path | all ops | n/a |

The capability switch (Phases 2–5) is the rollback lever for the entire live
portion: flip an operator back to object-mode and app-exposer resumes building
its objects, no redeploy of app-exposer required.

## Open items to resolve while executing

- **Spec vs. job-subset for the wire type.** Design doc recommends a dedicated
  `VICESpec`. If we want extra de-risking, Phase 1 could first build from a
  `model.Job` subset and tighten to `VICESpec` before Phase 3 — decide at Phase 0.
- **Endpoint shape.** New `/analyses/spec` endpoint vs. content negotiation on
  the existing launch endpoint. New endpoint is simpler to reason about and
  delete later; decide in Phase 2.
- **Where `SpecVersion` is advertised.** Piggyback on capacity response vs. a
  dedicated operator-info endpoint. Decide in Phase 0.
- **iRODS/CSI ownership audit** (design-doc risk 4): confirm the operator can own
  the entire volume-layout decision with no DB lookup before Phase 1 moves the
  volume builders.
