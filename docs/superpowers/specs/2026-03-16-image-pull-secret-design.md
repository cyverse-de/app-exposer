# Ensure image pull secret on vice-operator startup

## Context

VICE deployments reference an image pull secret (`vice-image-pull-secret`) via
`ImagePullSecrets` in the pod spec so Kubernetes can pull container images from
private registries. Currently this secret must be pre-created manually. This
feature adds flags to vice-operator so it can ensure the secret exists with the
correct registry credentials on startup, matching the pattern already
established by `EnsureClusterConfigSecret`.

## Approach

Approach A: a dedicated `EnsureImagePullSecret` function in `operator/secrets.go`,
separate from `EnsureClusterConfigSecret`. The two secrets have different types
(`Opaque` vs `kubernetes.io/dockerconfigjson`) and different data shapes, so a
shared abstraction would be premature. We can generalize later if more secrets
of the same type are needed.

## New CLI flags

Added to `cmd/vice-operator/main.go`:

| Flag | Default | Description |
|------|---------|-------------|
| `--image-pull-secret` | `vice-image-pull-secret` | K8s secret name |
| `--registry-server` | `""` | Docker registry server (e.g. `harbor.cyverse.org`) |
| `--registry-username` | `""` | Registry username |
| `--registry-password` | `""` | Registry password |

**Validation:** If any of `--registry-server`, `--registry-username`, or
`--registry-password` is provided, all three are required. If none are provided,
image pull secret management is skipped entirely (opt-in behavior).

## New function

```go
func EnsureImagePullSecret(
    ctx context.Context,
    clientset kubernetes.Interface,
    namespace, secretName, server, username, password string,
) error
```

In `operator/secrets.go`, alongside `EnsureClusterConfigSecret`.

### Logic

1. Build the `dockerconfigjson` payload:
   `{"auths":{"<server>":{"username":"<user>","password":"<pass>","auth":"<base64(user:pass)>"}}}`
2. `Get` the secret by name in the namespace.
3. If not found: `Create` a new secret with type `kubernetes.io/dockerconfigjson`
   and the built payload under the `.dockerconfigjson` key.
4. If found: compare the `.dockerconfigjson` key value.
   - If identical, done (log at debug level).
   - If different or missing, `Update` with the correct value (log at info level).
5. Return any K8s API errors, wrapped with context (e.g. "creating Secret X").

### dockerconfigjson format

```json
{
  "auths": {
    "harbor.cyverse.org": {
      "username": "robot$vice",
      "password": "secret-token",
      "auth": "cm9ib3QkdmljZTpzZWNyZXQtdG9rZW4="
    }
  }
}
```

The `auth` field is `base64(username:password)`. The optional `email` field is
intentionally omitted since Kubernetes does not require it.

## Startup wiring

In `cmd/vice-operator/main.go`, after the existing `EnsureClusterConfigSecret`
call and before creating the operator:

```go
if registryServer != "" {
    if err := operator.EnsureImagePullSecret(ctx, clientset, namespace,
        imagePullSecret, registryServer, registryUsername, registryPassword); err != nil {
        log.Fatalf("failed to ensure image pull secret: %v", err)
    }
}
```

The existing 30-second context timeout covers both secret operations.

## RBAC

Already handled. The `secrets` resource was added to the vice-operator
ClusterRole in the previous commit.

## Tests

Table-driven tests for `EnsureImagePullSecret` in `operator/secrets_test.go`:

| Case | Expected behavior |
|------|-------------------|
| Secret doesn't exist | Creates with correct dockerconfigjson and type |
| Secret exists with correct value | No update (idempotent) |
| Secret exists with wrong credentials | Updates to correct value |
| Secret exists with nil Data map | Adds dockerconfigjson key |
| Verify secret type | Must be `kubernetes.io/dockerconfigjson` |

## Files changed

- `operator/secrets.go` -- add `EnsureImagePullSecret` and `buildDockerConfigJSON` helper
- `operator/secrets_test.go` -- add table-driven tests
- `cmd/vice-operator/main.go` -- add flags, validation, conditional call at startup

## Files unchanged (reference only)

- `incluster/deployments.go:392-403` -- how image pull secrets are referenced in pod specs
- `operator/resources.go:100-114` -- existing upsert pattern
