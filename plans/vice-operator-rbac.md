# vice-operator RBAC permissions

vice-operator is a per-cluster service that owns the lifecycle of VICE
interactive-analysis pods. It applies bundles received from `app-exposer`,
operates per-analysis network policies, runs an image-cache daemon set,
and (as of PR #148) runs a leader-elected pod informer that pushes status
updates to `job-status-listener`.

Permissions split into two bindings:

- A **ClusterRole** for the three resource types it must reach outside its
  own namespace.
- A **Role** in the vice namespace (default: `vice-apps`) for everything
  else.

Both are bound to the `vice-operator` ServiceAccount in the vice namespace
via a ClusterRoleBinding and a RoleBinding respectively.

## ClusterRole (cluster-scoped)

| API group | Resources | Verbs | Why |
|---|---|---|---|
| `""` (core) | `persistentvolumes` | get, list, create, update, delete | PVs are cluster-scoped objects but lifecycled per analysis. `applyBundle` upserts PVs that ship in the analysis bundle (notably iRODS CSI volumes with `Retain` reclaim policy that PVC deletion alone won't clean up); `deleteAnalysisResources` lists by analysis-id label and deletes them. See `operator/resources.go:111-118, 235-244`. |
| `""` (core) | `nodes` | list | `CapacityCalculator.Calculate` lists all nodes in the cluster to sum allocatable CPU, memory, and GPU resources, then subtracts current usage to decide whether to accept new launches. See `operator/capacity.go:56`. |
| `""` (core) | `services` | get | One specific cross-namespace read at startup: `GET default/kubernetes` to discover the API server's ClusterIP. The first octet of that IP is used to derive a conservative `/8` CIDR that is added to the **`Except:`** list of every analysis's egress allow-rule — i.e. this read is used to **block** analyses from reaching kube-apiserver and other in-cluster ClusterIPs, not to allow it. See `operator/networkpolicy.go:24-41, 286-300`. Can be replaced by passing `--service-cidr` explicitly if you want to avoid the cluster-wide grant. |

## Role (bound in the vice namespace)

| API group | Resources | Verbs | Why |
|---|---|---|---|
| `""` (core) | `pods` | get, list | Listing pods by analysis-id label drives `/listing`, `/status`, save-and-exit checks, and the loading-page redirect. See `operator/handlers_status.go:72,190,304`, `operator/loading.go:272`, `operator/handlers.go:389`. |
| `""` (core) | `pods/log` | get | `HandleLogs` streams container logs via `Pods(ns).GetLogs(name)`. See `operator/handlers_status.go:319`. |
| `""` (core) | `services` | get, list, create, update, delete | Bundle Services are upserted on launch and deleted on exit; status/permissions/loading handlers list them by analysis-id label; the file-transfer code resolves the sidecar's in-cluster DNS name through them. See `operator/resources.go:135-139, 199-208`, `operator/transfers.go:177`. |
| `""` (core) | `configmaps` | get, list, update, create, delete | Bundle ConfigMaps are upserted/deleted with the analysis; `handlers_permissions.go` lists and updates the per-analysis allowed-users ConfigMap that vice-proxy watches for ACL changes. See `operator/handlers_permissions.go:30,132`, `operator/resources.go:102-109, 247-256`. |
| `""` (core) | `secrets` | get, create, update | `EnsureClusterConfigSecret` provisions/refreshes the per-cluster Secret consumed by vice-proxy via `envFrom` (Keycloak URLs, VICE_BASE_URL, state HMAC, etc.). See `operator/secrets.go:48-120`. Delete is not required and not used. |
| `""` (core) | `persistentvolumeclaims` | get, list, create, update, delete | Bundle PVCs (per-analysis working-dir PVC and iRODS CSI claim) upserted on launch, deleted on exit. See `operator/resources.go:120-127, 223-232`. |
| `apps` | `deployments` | get, list, watch, create, update, delete | The analysis itself is a Deployment, upserted by `applyBundle` and deleted by `deleteAnalysisResources`. The status-publisher informer (PR #148) watches Deployments with `app-type=interactive` to detect Available→Running transitions. List drives capacity, status, loading, and the new file-transfer sidecar check added during the save-and-exit fix. See `operator/statusinformer.go:164`, `operator/resources.go:129-133, 211-220`, `operator/transfers.go:281`. |
| `apps` | `daemonsets` | get, list, create, update, delete | `ImageCacheManager` creates per-image DaemonSets to pre-pull container images onto vice-labeled nodes so analysis pods start without an `ImagePullBackOff`. See `operator/imagecache.go:262,305,344,396,412`. |
| `networking.k8s.io` | `networkpolicies` | get, list, create, update, delete | Namespace-wide baseline policies are upserted at startup (`vice-operator-egress`, `vice-default-deny-egress`, `vice-default-deny-ingress`). A per-analysis egress allow-policy is upserted on launch and deleted with the rest of the bundle. `HandleRegenerateNetworkPolicies` re-issues both. See `operator/networkpolicy.go:146-257`, `operator/handlers.go:285,450-463`, `operator/resources.go:259-268`. |
| `policy` | `poddisruptionbudgets` | get, list, create, update, delete | Bundle PDB upserted on launch / deleted on exit. Prevents node-drain from evicting long-running interactive pods. See `operator/resources.go:147-151, 175-184`. |
| `gateway.networking.k8s.io` | `gateways` | get, create | `EnsureGateway` runs once at operator startup to create the namespace's Gateway if it isn't already present. The operator never updates or deletes Gateways. See `operator/gateway.go:18-63`. |
| `gateway.networking.k8s.io` | `httproutes` | get, list, create, update, delete | Per-analysis HTTPRoutes upserted on launch / deleted on exit; the API-route is upserted at startup; route-swap reads and updates them; status/listing handlers list them. See `operator/gateway.go:217,298`, `operator/routeswap.go:47-87`, `operator/resources.go:141-145, 187-196`. |
| `traefik.io` | `middlewares` | get, create | `EnsureCORSMiddleware` runs once at startup via the dynamic REST client to provision the CORS middleware referenced by the API HTTPRoute. The operator never updates or deletes middlewares. See `operator/gateway.go:122-164`. |
| `coordination.k8s.io` | `leases` | get, create, update | Backing object for the status-publisher's leader election (`k8s.io/client-go/tools/leaderelection` with `LeaseLock`). The leader renews via Update; candidates acquire via Create or Update on takeover. See `operator/statusinformer.go:87-103`. |

## Notes on minimization

The current Ansible RBAC (`deployments/ansible/roles/vice-operator/tasks/main.yml`) grants several verbs the code never exercises. They are not security-critical to drop, but if you want to tighten:

| Resource | Granted but unused |
|---|---|
| ClusterRole `persistentvolumes` | `watch` |
| ClusterRole `nodes` | `get`, `watch` (only `list` is called) |
| Role `secrets` | `list`, `watch`, `delete` |
| Role `gateways` | `update`, `delete`, `list`, `watch` |
| Role `middlewares` | `update`, `delete`, `list`, `watch` |
| Role `leases` | (no `watch` declared and none needed — the leader-election library polls via Get rather than Watch) |

Everything else in the existing Ansible is actually used by the code paths above. If you ever swap the leader-election implementation to `informerResourceLock`, you'll need to add `watch` on `leases`.

## How the bindings are wired

- **ServiceAccount** `vice-operator` in the vice namespace (`vice_ns`, default `vice-apps`).
- **ClusterRoleBinding** `vice-operator-{{ vice_ns }}` → ClusterRole `vice-operator` → SA above.
- **RoleBinding** `vice-operator` in the vice namespace → Role `vice-operator` → SA above.

Each cluster runs its own vice-operator with its own SA, so a multi-cluster
deployment ends up with one binding pair per cluster — never a single
binding that grants the operator access across clusters.
