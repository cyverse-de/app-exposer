# Image Pull Secret Management Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `EnsureImagePullSecret` to vice-operator so it creates/updates a `kubernetes.io/dockerconfigjson` secret on startup, eliminating manual secret creation for private registries.

**Architecture:** A new `EnsureImagePullSecret` function in `operator/secrets.go` follows the same upsert pattern as `EnsureClusterConfigSecret`. A `buildDockerConfigJSON` helper builds the JSON payload. Four new CLI flags control it, with all-or-nothing validation for the three registry credential flags.

**Tech Stack:** Go, `k8s.io/client-go`, `k8s.io/client-go/kubernetes/fake` for tests, `encoding/json` + `encoding/base64` for dockerconfigjson construction.

**Spec:** `docs/superpowers/specs/2026-03-16-image-pull-secret-design.md`

---

## Chunk 1: Implementation

### Task 1: Add `buildDockerConfigJSON` helper and tests

**Files:**
- Modify: `operator/secrets.go` — add `buildDockerConfigJSON` function
- Modify: `operator/secrets_test.go` — add `TestBuildDockerConfigJSON`

- [ ] **Step 1: Write the failing test for `buildDockerConfigJSON`**

Add to `operator/secrets_test.go`:

```go
func TestBuildDockerConfigJSON(t *testing.T) {
	result, err := buildDockerConfigJSON("harbor.cyverse.org", "robot$vice", "secret-token")
	require.NoError(t, err)

	// Verify it's valid JSON with the expected structure.
	var parsed struct {
		Auths map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Auth     string `json:"auth"`
		} `json:"auths"`
	}
	err = json.Unmarshal(result, &parsed)
	require.NoError(t, err)

	entry, ok := parsed.Auths["harbor.cyverse.org"]
	require.True(t, ok, "should have entry for harbor.cyverse.org")
	assert.Equal(t, "robot$vice", entry.Username)
	assert.Equal(t, "secret-token", entry.Password)

	// Verify auth is base64(username:password).
	decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
	require.NoError(t, err)
	assert.Equal(t, "robot$vice:secret-token", string(decoded))
}
```

Add `"encoding/base64"` and `"encoding/json"` to the test file imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./operator/ -run TestBuildDockerConfigJSON -v`
Expected: compilation error — `buildDockerConfigJSON` undefined.

- [ ] **Step 3: Implement `buildDockerConfigJSON`**

Add to `operator/secrets.go`, before `EnsureClusterConfigSecret`. Add `"encoding/base64"` and `"encoding/json"` to imports.

```go
// dockerConfigJSON represents the structure of a Docker config.json file
// used by kubernetes.io/dockerconfigjson secrets.
type dockerConfigJSON struct {
	Auths map[string]dockerConfigAuth `json:"auths"`
}

// dockerConfigAuth holds credentials for a single Docker registry.
type dockerConfigAuth struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Auth     string `json:"auth"`
}

// buildDockerConfigJSON constructs the JSON payload for a
// kubernetes.io/dockerconfigjson secret with a single registry entry.
func buildDockerConfigJSON(server, username, password string) ([]byte, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	cfg := dockerConfigJSON{
		Auths: map[string]dockerConfigAuth{
			server: {
				Username: username,
				Password: password,
				Auth:     auth,
			},
		},
	}
	return json.Marshal(cfg)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./operator/ -run TestBuildDockerConfigJSON -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add operator/secrets.go operator/secrets_test.go
git commit -m "Add buildDockerConfigJSON helper for dockerconfigjson secrets"
```

---

### Task 2: Add `EnsureImagePullSecret` and tests

**Files:**
- Modify: `operator/secrets.go` — add `EnsureImagePullSecret` function
- Modify: `operator/secrets_test.go` — add `TestEnsureImagePullSecret`

- [ ] **Step 1: Write the failing test for `EnsureImagePullSecret`**

Add to `operator/secrets_test.go`. The `dockerconfigjson` data key is `.dockerconfigjson` (note the leading dot — this is the K8s convention for this secret type).

```go
func TestEnsureImagePullSecret(t *testing.T) {
	const (
		ns         = "vice-apps"
		secretName = "vice-image-pull-secret"
		server     = "harbor.cyverse.org"
		username   = "robot$vice"
		password   = "secret-token"
	)

	// Pre-build the expected payload for comparison.
	wantData, err := buildDockerConfigJSON(server, username, password)
	require.NoError(t, err)

	dockerKey := apiv1.DockerConfigJsonKey // ".dockerconfigjson"

	tests := []struct {
		name     string
		existing *apiv1.Secret // nil means no pre-existing secret
	}{
		{
			name:     "creates secret when missing",
			existing: nil,
		},
		{
			name: "no update when credentials match",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Type:       apiv1.SecretTypeDockerConfigJson,
				Data:       map[string][]byte{dockerKey: wantData},
			},
		},
		{
			name: "updates when credentials differ",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Type:       apiv1.SecretTypeDockerConfigJson,
				Data:       map[string][]byte{dockerKey: []byte(`{"auths":{"old.registry":{}}}`)},
			},
		},
		{
			name: "handles existing secret with nil Data map",
			existing: &apiv1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: ns},
				Type:       apiv1.SecretTypeDockerConfigJson,
				Data:       nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var clientset *fake.Clientset
			if tt.existing != nil {
				clientset = fake.NewSimpleClientset(tt.existing)
			} else {
				clientset = fake.NewSimpleClientset()
			}

			err := EnsureImagePullSecret(context.Background(), clientset, ns, secretName, server, username, password)
			require.NoError(t, err)

			// Verify the secret exists with correct type and data.
			secret, err := clientset.CoreV1().Secrets(ns).Get(context.Background(), secretName, metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, apiv1.SecretTypeDockerConfigJson, secret.Type)
			assert.JSONEq(t, string(wantData), string(secret.Data[dockerKey]))
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./operator/ -run TestEnsureImagePullSecret -v`
Expected: compilation error — `EnsureImagePullSecret` undefined.

- [ ] **Step 3: Implement `EnsureImagePullSecret`**

Add to `operator/secrets.go`, after `EnsureClusterConfigSecret`:

```go
// EnsureImagePullSecret ensures that the named dockerconfigjson Secret exists
// in the given namespace with credentials for the specified registry. It
// creates the secret if missing and updates it if the credentials differ.
func EnsureImagePullSecret(ctx context.Context, clientset kubernetes.Interface, namespace, secretName, server, username, password string) error {
	client := clientset.CoreV1().Secrets(namespace)
	dockerKey := apiv1.DockerConfigJsonKey // ".dockerconfigjson"

	wantData, err := buildDockerConfigJSON(server, username, password)
	if err != nil {
		return fmt.Errorf("building dockerconfigjson for Secret %s: %w", secretName, err)
	}

	existing, err := client.Get(ctx, secretName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Infof("creating image pull Secret %s for registry %s", secretName, server)
		_, err = client.Create(ctx, &apiv1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: namespace,
			},
			Type: apiv1.SecretTypeDockerConfigJson,
			Data: map[string][]byte{
				dockerKey: wantData,
			},
		}, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("creating Secret %s: %w", secretName, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking for existing Secret %s: %w", secretName, err)
	}

	// Secret exists — check whether credentials need updating.
	if string(existing.Data[dockerKey]) == string(wantData) {
		log.Debugf("image pull Secret %s already has correct credentials", secretName)
		return nil
	}

	log.Infof("updating image pull Secret %s credentials for registry %s", secretName, server)
	if existing.Data == nil {
		existing.Data = make(map[string][]byte)
	}
	existing.Data[dockerKey] = wantData

	_, err = client.Update(ctx, existing, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("updating Secret %s: %w", secretName, err)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./operator/ -run TestEnsureImagePullSecret -v`
Expected: PASS

- [ ] **Step 5: Run full test suite**

Run: `go test ./...`
Expected: all PASS

- [ ] **Step 6: Commit**

```bash
git add operator/secrets.go operator/secrets_test.go
git commit -m "Add EnsureImagePullSecret for dockerconfigjson secrets"
```

---

### Task 3: Add CLI flags and wire into startup

**Files:**
- Modify: `cmd/vice-operator/main.go` — add flags, validation, call `EnsureImagePullSecret`

- [ ] **Step 1: Add flag variables to the `var` block (after `clusterConfigSecret`)**

```go
		imagePullSecret  string
		registryServer   string
		registryUsername string
		registryPassword string
```

- [ ] **Step 2: Add flag registrations (after the `cluster-config-secret` flag)**

```go
	flag.StringVar(&imagePullSecret, "image-pull-secret", "vice-image-pull-secret", "Name of the K8s image pull Secret")
	flag.StringVar(&registryServer, "registry-server", "", "Docker registry server (e.g. harbor.cyverse.org)")
	flag.StringVar(&registryUsername, "registry-username", "", "Docker registry username")
	flag.StringVar(&registryPassword, "registry-password", "", "Docker registry password")
```

- [ ] **Step 3: Add validation (after the `vice-base-url` validation block)**

```go
	// Validate registry flags: all three required together, or none.
	registryFlagsSet := registryServer != "" || registryUsername != "" || registryPassword != ""
	if registryFlagsSet && (registryServer == "" || registryUsername == "" || registryPassword == "") {
		log.Fatal("--registry-server, --registry-username, and --registry-password must all be provided together")
	}
```

- [ ] **Step 4: Add the conditional `EnsureImagePullSecret` call (after `EnsureClusterConfigSecret`)**

```go
	// Ensure the image pull secret exists so pods can pull from private registries.
	if registryServer != "" {
		if err := operator.EnsureImagePullSecret(ctx, clientset, namespace, imagePullSecret, registryServer, registryUsername, registryPassword); err != nil {
			log.Fatalf("failed to ensure image pull secret: %v", err)
		}
	}
```

- [ ] **Step 5: Build and run full test suite**

Run: `go build ./... && go test ./...`
Expected: all builds and tests pass.

- [ ] **Step 6: Commit**

```bash
git add cmd/vice-operator/main.go
git commit -m "Add --image-pull-secret and --registry-* flags to vice-operator"
```

---

### Task 4: Update local deployment manifest

**Files:**
- Modify: `k8s/vice-operator-local.yml` — add registry flags to args (optional, for local testing)

- [ ] **Step 1: Add registry args to the deployment container args**

After the `--vice-base-url` arg, add:

```yaml
            - "--registry-server=harbor.cyverse.org"
            - "--registry-username=robot$vice"
            - "--registry-password=REPLACE_ME"
```

Note: The password should be replaced with the actual value before applying. This is a local-only manifest not used in production.

- [ ] **Step 2: Commit**

```bash
git add k8s/vice-operator-local.yml
git commit -m "Add registry flags to local vice-operator deployment"
```

## Verification

After all tasks:

- `go build ./...` — compiles cleanly
- `go test ./...` — all tests pass
- `goimports -w operator/secrets.go operator/secrets_test.go cmd/vice-operator/main.go` — formatted
- `golangci-lint run ./...` — no warnings
- Manual: deploy to local cluster, verify secret is created with correct type and data
