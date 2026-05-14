# Deployment

## VICE OAuth callback relay

VICE apps authenticate users through Keycloak. Because Keycloak only honors `*`
as a *trailing* wildcard in Valid Redirect URIs, per-app VICE subdomains
(`https://<hash>.cyverse.run:4343/`) cannot be matched. Instead, `vice-proxy`
sends a fixed per-operator callback URL as the `redirect_uri` and carries the
real app URL in an HMAC-signed `state` blob; `vice-operator` exposes a
`/auth/callback` endpoint that relays the authorization code back to the app's
own subdomain.

The main `app-exposer` binary needs **no configuration changes** for this. The
configuration surface is `vice-operator`, Keycloak, and — indirectly —
`vice-proxy`.

### vice-operator — startup configuration

| Setting | Flag / env | Notes |
|---|---|---|
| Operator public base URL | `--public-url` (flag only) | e.g. `https://vice-operator-qa.cyverse.org`. Must be browser-reachable — Keycloak redirects users here. |
| State signing secret | `--state-hmac-secret` **or** `STATE_HMAC_SECRET` env | Prefer the env var — flag values are visible in `/proc` and `ps`. Must be high-entropy and **stable across restarts**; store it in a Kubernetes Secret, do not auto-generate. |

When VICE-proxy auth is enabled and either value is missing, the operator logs
a warning and `vice-proxy` pods will fail to start.

### Cluster-config Secret (written by vice-operator)

From the two settings above, `vice-operator` derives and writes two keys into
the existing cluster-config Secret (`--cluster-config-secret`):

- `OPERATOR_CALLBACK_URL` = `<--public-url>` + `/auth/callback`
- `STATE_HMAC_SECRET` = the state signing secret

### vice-proxy — no manual configuration

`vice-proxy` reads `OPERATOR_CALLBACK_URL` and `STATE_HMAC_SECRET` from the
cluster-config Secret via `EnvFrom` automatically — no deployment-manifest
change is needed for `vice-proxy` itself. Note that `vice-proxy` now *requires*
both values when auth is enabled and will crash-loop without them.

### Keycloak — manual, out-of-band

On the OAuth client `vice-proxy` authenticates against:

- **Remove** the non-functional wildcard `https://*.cyverse.run:4343/*` Valid
  Redirect URI.
- **Add** one static entry per operator: `<--public-url>/auth/callback` — e.g.
  `https://vice-operator-qa.cyverse.org/auth/callback`.

### Operational notes

- The operator's `/auth/callback` route must be reachable **unauthenticated**
  through whatever ingress/Gateway fronts the operator. The handler is
  registered outside the auth middleware; the ingress just needs to expose it.
- **Secret rotation is not seamless.** Changing `STATE_HMAC_SECRET` requires an
  operator restart (to rewrite the cluster-config Secret) *and* a restart of
  running `vice-proxy` pods (the env var is read at pod start). In-flight logins
  break during the rollover.
- Each operator needs its own `--public-url` and its own Keycloak redirect-URI
  entry. The `STATE_HMAC_SECRET` is shared between an operator and the
  `vice-proxy` pods it launches.
