# Image Cache Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add DaemonSet-based image caching to vice-operator with a bulk REST API, so container images are pre-pulled onto cluster nodes for faster VICE deployment startups.

**Architecture:** An `ImageCacheManager` struct in `operator/imagecache.go` handles all DaemonSet CRUD. Each cached image gets its own DaemonSet with an init container (target image, `command: ["true"]`) and a `pause` main container. HTTP handlers on `*Operator` delegate to the manager. All state lives in K8s — the operator is fully stateless.

**Tech Stack:** Go, `k8s.io/client-go`, `k8s.io/client-go/kubernetes/fake` for tests, `crypto/sha256` for slug hashing, Echo v4 for HTTP handlers, swaggo for Swagger docs.

**Spec:** `docs/superpowers/specs/2026-03-17-image-cache-design.md`

---

## Chunk 1: Core logic (imagecache.go + tests)

### Task 1: Add slugifyImage and validation helpers with tests

**Files:**
- Create: `operator/imagecache.go`
- Create: `operator/imagecache_test.go`

- [ ] **Step 1: Write failing tests for slugifyImage and validateImageRef**

Add to `operator/imagecache_test.go`:

```go
package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSlugifyImage(t *testing.T) {
	tests := []struct {
		name  string
		image string
	}{
		{name: "full registry path with tag", image: "harbor.cyverse.org/de/vice-proxy:latest"},
		{name: "simple image with tag", image: "nginx:1.25"},
		{name: "image with digest", image: "harbor.cyverse.org/de/app@sha256:abc123"},
		{name: "deeply nested path", image: "registry.example.com/org/team/project/image:v1.2.3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			slug := slugifyImage(tt.image)

			// Must be a valid K8s name component (lowercase, no special chars except -).
			assert.Regexp(t, `^[a-z0-9][a-z0-9-]*[a-z0-9]$`, slug, "slug must be a valid K8s name")

			// Total DaemonSet name must fit K8s 253-char limit.
			fullName := "image-cache-" + slug
			assert.LessOrEqual(t, len(fullName), 253, "full DaemonSet name must fit K8s limit")
		})
	}
}

func TestSlugifyImageDeterminism(t *testing.T) {
	image := "harbor.cyverse.org/de/vice-proxy:latest"
	assert.Equal(t, slugifyImage(image), slugifyImage(image), "same image must produce same slug")
}

func TestSlugifyImageUniqueness(t *testing.T) {
	images := []string{
		"harbor.cyverse.org/de/vice-proxy:latest",
		"harbor.cyverse.org/de/vice-proxy:qa",
		"harbor.cyverse.org/de/porklock:latest",
		"other.registry.io/de/vice-proxy:latest",
	}

	slugs := make(map[string]string)
	for _, img := range images {
		slug := slugifyImage(img)
		if existing, ok := slugs[slug]; ok {
			t.Errorf("slug collision: %q and %q both produce %q", existing, img, slug)
		}
		slugs[slug] = img
	}
}

func TestValidateImageRef(t *testing.T) {
	tests := []struct {
		name    string
		image   string
		wantErr bool
	}{
		{name: "valid full ref", image: "harbor.cyverse.org/de/vice-proxy:latest", wantErr: false},
		{name: "valid simple", image: "nginx:1.25", wantErr: false},
		{name: "valid no tag", image: "harbor.cyverse.org/de/vice-proxy", wantErr: false},
		{name: "empty string", image: "", wantErr: true},
		{name: "spaces", image: "bad image", wantErr: true},
		{name: "only special chars", image: "!!!", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateImageRef(tt.image)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./operator/ -run "TestSlugify|TestValidateImageRef" -v`
Expected: compilation error — functions undefined.

- [ ] **Step 3: Implement slugifyImage, validateImageRef, and types**

Create `operator/imagecache.go`:

```go
package operator

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	"k8s.io/client-go/kubernetes"
)

// NOTE: Additional imports (appsv1, apiv1, resource, metav1, apierrors, labels)
// are added in later tasks as the functions that need them are implemented.

const (
	// Labels and annotations used to identify image cache DaemonSets.
	labelManagedBy    = "managed-by"
	labelPurpose      = "purpose"
	labelImageCacheID = "image-cache-id"
	annotationImage   = "de.cyverse.org/cached-image"

	valueManagedBy = "vice-operator"
	valuePurpose   = "image-cache"

	// dsNamePrefix is prepended to every cache DaemonSet name.
	dsNamePrefix = "image-cache-"

	// slugHashLen is the number of hex characters from the SHA-256 hash
	// appended to slugs for uniqueness (12 hex chars = 48 bits).
	slugHashLen = 12

	// pauseImage is the minimal container that runs as the main container
	// in cache DaemonSets.
	pauseImage = "registry.k8s.io/pause:3.10"
)

// imageRefPattern is a basic validation pattern for container image references.
// It requires at least one alphanumeric character and no spaces.
var imageRefPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]*$`)

// ImageCacheRequest is the request body for bulk cache operations.
type ImageCacheRequest struct {
	Images []string `json:"images"`
}

// ImageCacheResult reports the outcome of a single image cache operation.
type ImageCacheResult struct {
	Image  string `json:"image"`
	Status string `json:"status"`          // "ok" or "error"
	Error  string `json:"error,omitempty"` // present only when status is "error"
}

// ImageCacheBulkResponse is the response for bulk cache operations.
type ImageCacheBulkResponse struct {
	Results []ImageCacheResult `json:"results"`
}

// ImageCacheStatus describes the cache state of a single image.
type ImageCacheStatus struct {
	Image   string `json:"image"`
	ID      string `json:"id"`
	Ready   int32  `json:"ready"`
	Desired int32  `json:"desired"`
	Status  string `json:"status"` // "ready", "pulling", "cached-with-errors", "error"
}

// ImageCacheListResponse is the response for listing cached images.
type ImageCacheListResponse struct {
	Images []ImageCacheStatus `json:"images"`
}

// ImageCacheManager manages image cache DaemonSets in a namespace.
type ImageCacheManager struct {
	clientset           kubernetes.Interface
	namespace           string
	imagePullSecretName string
}

// NewImageCacheManager creates a new ImageCacheManager.
func NewImageCacheManager(clientset kubernetes.Interface, namespace, imagePullSecretName string) *ImageCacheManager {
	return &ImageCacheManager{
		clientset:           clientset,
		namespace:           namespace,
		imagePullSecretName: imagePullSecretName,
	}
}

// validateImageRef checks that an image reference is minimally valid.
func validateImageRef(image string) error {
	if image == "" {
		return fmt.Errorf("image reference must not be empty")
	}
	if !imageRefPattern.MatchString(image) {
		return fmt.Errorf("invalid image reference: %q", image)
	}
	return nil
}

// slugifyImage produces a deterministic, K8s-safe name component from an image
// reference. It lowercases, replaces special characters with dashes, truncates
// to fit the K8s 253-char name limit, and appends a 12-char SHA-256 hash suffix
// for uniqueness.
func slugifyImage(image string) string {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(image)))[:slugHashLen]

	slug := strings.ToLower(image)
	replacer := strings.NewReplacer("/", "-", ":", "-", ".", "-", "@", "-")
	slug = replacer.Replace(slug)

	// Remove leading/trailing dashes and collapse consecutive dashes.
	slug = collapseRuns(slug, '-')
	slug = strings.Trim(slug, "-")

	// Truncate so dsNamePrefix + slug + "-" + hash fits in 253 chars.
	maxSlugLen := 253 - len(dsNamePrefix) - 1 - slugHashLen
	if len(slug) > maxSlugLen {
		slug = slug[:maxSlugLen]
		slug = strings.TrimRight(slug, "-")
	}

	return slug + "-" + hash
}

// collapseRuns replaces consecutive occurrences of c with a single c.
func collapseRuns(s string, c byte) string {
	var b strings.Builder
	b.Grow(len(s))
	prev := byte(0)
	for i := 0; i < len(s); i++ {
		if s[i] == c && prev == c {
			continue
		}
		b.WriteByte(s[i])
		prev = s[i]
	}
	return b.String()
}
```

Note: The imports include types we'll use in later tasks. The compiler won't
complain since the types reference them.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./operator/ -run "TestSlugify|TestValidateImageRef" -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add operator/imagecache.go operator/imagecache_test.go
git commit -m "Add image cache slug generation, validation, and types"
```

---

### Task 2: Add buildCacheDaemonSet and test

**Files:**
- Modify: `operator/imagecache.go`
- Modify: `operator/imagecache_test.go`

- [ ] **Step 1: Write failing test for buildCacheDaemonSet**

Add to `operator/imagecache_test.go`:

```go
func TestBuildCacheDaemonSet(t *testing.T) {
	mgr := NewImageCacheManager(nil, "vice-apps", "vice-image-pull-secret")
	image := "harbor.cyverse.org/de/vice-proxy:latest"
	slug := slugifyImage(image)

	ds := mgr.buildCacheDaemonSet(image, slug)

	// Metadata.
	assert.Equal(t, dsNamePrefix+slug, ds.Name)
	assert.Equal(t, "vice-apps", ds.Namespace)
	assert.Equal(t, valueManagedBy, ds.Labels[labelManagedBy])
	assert.Equal(t, valuePurpose, ds.Labels[labelPurpose])
	assert.Equal(t, slug, ds.Labels[labelImageCacheID])
	assert.Equal(t, image, ds.Annotations[annotationImage])

	// Selector must match pod template labels.
	assert.Equal(t, ds.Spec.Selector.MatchLabels, ds.Spec.Template.Labels)

	// Init container pulls the target image.
	require.Len(t, ds.Spec.Template.Spec.InitContainers, 1)
	init := ds.Spec.Template.Spec.InitContainers[0]
	assert.Equal(t, image, init.Image)
	assert.Equal(t, []string{"true"}, init.Command)
	assert.Equal(t, apiv1.PullAlways, init.ImagePullPolicy)

	// Main container is pause.
	require.Len(t, ds.Spec.Template.Spec.Containers, 1)
	main := ds.Spec.Template.Spec.Containers[0]
	assert.Equal(t, pauseImage, main.Image)
	assert.Equal(t, apiv1.PullIfNotPresent, main.ImagePullPolicy)

	// Image pull secret.
	require.Len(t, ds.Spec.Template.Spec.ImagePullSecrets, 1)
	assert.Equal(t, "vice-image-pull-secret", ds.Spec.Template.Spec.ImagePullSecrets[0].Name)

	// Tolerations.
	assert.Len(t, ds.Spec.Template.Spec.Tolerations, 2)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./operator/ -run TestBuildCacheDaemonSet -v`
Expected: compilation error — `buildCacheDaemonSet` undefined.

- [ ] **Step 3: Implement buildCacheDaemonSet**

Add to `operator/imagecache.go`:

```go
// buildCacheDaemonSet constructs a DaemonSet that pre-pulls the given image
// onto every node. The init container references the target image and runs
// "true" to exit immediately; K8s pulls the image before running the command.
//
// For distroless or scratch-based images that lack "true", the init container
// will fail with CrashLoopBackOff. This is expected — the image is still pulled
// and cached on each node. The status API reports these as "cached-with-errors".
func (m *ImageCacheManager) buildCacheDaemonSet(image, slug string) *appsv1.DaemonSet {
	labels := map[string]string{
		labelManagedBy:    valueManagedBy,
		labelPurpose:      valuePurpose,
		labelImageCacheID: slug,
	}

	minResources := apiv1.ResourceList{
		apiv1.ResourceCPU:    resource.MustParse("1m"),
		apiv1.ResourceMemory: resource.MustParse("1Mi"),
	}

	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dsNamePrefix + slug,
			Namespace: m.namespace,
			Labels:    labels,
			Annotations: map[string]string{
				annotationImage: image,
			},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: apiv1.PodSpec{
					ImagePullSecrets: []apiv1.LocalObjectReference{
						{Name: m.imagePullSecretName},
					},
					InitContainers: []apiv1.Container{
						{
							Name:            "pull",
							Image:           image,
							Command:         []string{"true"},
							ImagePullPolicy: apiv1.PullAlways,
							Resources: apiv1.ResourceRequirements{
								Requests: minResources,
								Limits:   minResources,
							},
						},
					},
					Containers: []apiv1.Container{
						{
							Name:            "pause",
							Image:           pauseImage,
							ImagePullPolicy: apiv1.PullIfNotPresent,
							Resources: apiv1.ResourceRequirements{
								Requests: minResources,
								Limits:   minResources,
							},
						},
					},
					Tolerations: []apiv1.Toleration{
						{
							Key:      "analysis",
							Operator: apiv1.TolerationOpExists,
						},
						{
							Key:      "gpu",
							Operator: apiv1.TolerationOpEqual,
							Value:    "true",
							Effect:   apiv1.TaintEffectNoSchedule,
						},
					},
				},
			},
		},
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./operator/ -run TestBuildCacheDaemonSet -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add operator/imagecache.go operator/imagecache_test.go
git commit -m "Add buildCacheDaemonSet for image cache DaemonSet construction"
```

---

### Task 3: Add EnsureImageCached, RemoveCachedImage, and tests

**Files:**
- Modify: `operator/imagecache.go`
- Modify: `operator/imagecache_test.go`

- [ ] **Step 1: Write failing tests**

Add to `operator/imagecache_test.go`:

```go
import (
	"context"
	// ... add to existing imports:
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEnsureImageCached(t *testing.T) {
	const (
		ns     = "vice-apps"
		secret = "vice-image-pull-secret"
		image  = "harbor.cyverse.org/de/vice-proxy:latest"
	)
	slug := slugifyImage(image)
	dsName := dsNamePrefix + slug

	tests := []struct {
		name     string
		existing *appsv1.DaemonSet // nil = no pre-existing DS
	}{
		{
			name:     "creates DaemonSet when missing",
			existing: nil,
		},
		{
			name: "no-op when image matches",
			existing: NewImageCacheManager(nil, ns, secret).buildCacheDaemonSet(image, slug),
		},
		{
			name: "updates when annotation differs",
			existing: func() *appsv1.DaemonSet {
				ds := NewImageCacheManager(nil, ns, secret).buildCacheDaemonSet("old-image:v1", slug)
				ds.Name = dsName
				ds.Annotations[annotationImage] = "old-image:v1"
				return ds
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cs *fake.Clientset
			if tt.existing != nil {
				cs = fake.NewSimpleClientset(tt.existing)
			} else {
				cs = fake.NewSimpleClientset()
			}

			mgr := NewImageCacheManager(cs, ns, secret)
			err := mgr.EnsureImageCached(context.Background(), image)
			require.NoError(t, err)

			// Verify DaemonSet exists with correct annotation.
			ds, err := cs.AppsV1().DaemonSets(ns).Get(context.Background(), dsName, metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, image, ds.Annotations[annotationImage])
		})
	}
}

func TestRemoveCachedImage(t *testing.T) {
	const (
		ns     = "vice-apps"
		secret = "vice-image-pull-secret"
		image  = "harbor.cyverse.org/de/vice-proxy:latest"
	)

	tests := []struct {
		name     string
		existing bool
	}{
		{name: "deletes existing DaemonSet", existing: true},
		{name: "succeeds silently when missing", existing: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cs *fake.Clientset
			mgr := NewImageCacheManager(nil, ns, secret)
			if tt.existing {
				slug := slugifyImage(image)
				ds := mgr.buildCacheDaemonSet(image, slug)
				cs = fake.NewSimpleClientset(ds)
			} else {
				cs = fake.NewSimpleClientset()
			}
			mgr = NewImageCacheManager(cs, ns, secret)

			err := mgr.RemoveCachedImage(context.Background(), image)
			require.NoError(t, err)

			// Verify DaemonSet is gone.
			list, err := cs.AppsV1().DaemonSets(ns).List(context.Background(), metav1.ListOptions{})
			require.NoError(t, err)
			assert.Empty(t, list.Items)
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./operator/ -run "TestEnsureImageCached|TestRemoveCachedImage" -v`
Expected: compilation error.

- [ ] **Step 3: Implement EnsureImageCached and RemoveCachedImage**

Add to `operator/imagecache.go`. Add `"context"` and `apierrors "k8s.io/apimachinery/pkg/api/errors"` to imports.

```go
// EnsureImageCached creates or updates a cache DaemonSet for the given image.
// If a DaemonSet already exists with the correct image annotation, this is a
// no-op.
func (m *ImageCacheManager) EnsureImageCached(ctx context.Context, image string) error {
	if err := validateImageRef(image); err != nil {
		return err
	}

	slug := slugifyImage(image)
	dsName := dsNamePrefix + slug
	client := m.clientset.AppsV1().DaemonSets(m.namespace)

	existing, err := client.Get(ctx, dsName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Infof("creating image cache DaemonSet %s for %s", dsName, image)
		ds := m.buildCacheDaemonSet(image, slug)
		_, err = client.Create(ctx, ds, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("creating image cache DaemonSet %s: %w", dsName, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking for existing image cache DaemonSet %s: %w", dsName, err)
	}

	// DaemonSet exists — check if the image annotation matches.
	if existing.Annotations[annotationImage] == image {
		log.Debugf("image cache DaemonSet %s already has correct image", dsName)
		return nil
	}

	log.Infof("updating image cache DaemonSet %s from %q to %q", dsName, existing.Annotations[annotationImage], image)
	ds := m.buildCacheDaemonSet(image, slug)
	ds.ResourceVersion = existing.ResourceVersion
	_, err = client.Update(ctx, ds, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("updating image cache DaemonSet %s: %w", dsName, err)
	}
	return nil
}

// RemoveCachedImage deletes the cache DaemonSet for the given image.
// Returns nil if the DaemonSet doesn't exist (idempotent).
func (m *ImageCacheManager) RemoveCachedImage(ctx context.Context, image string) error {
	if err := validateImageRef(image); err != nil {
		return err
	}

	slug := slugifyImage(image)
	dsName := dsNamePrefix + slug
	client := m.clientset.AppsV1().DaemonSets(m.namespace)

	err := client.Delete(ctx, dsName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("deleting image cache DaemonSet %s: %w", dsName, err)
	}
	log.Infof("deleted image cache DaemonSet %s for %s", dsName, image)
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./operator/ -run "TestEnsureImageCached|TestRemoveCachedImage" -v`
Expected: all PASS

- [ ] **Step 5: Commit**

```bash
git add operator/imagecache.go operator/imagecache_test.go
git commit -m "Add EnsureImageCached and RemoveCachedImage with upsert/delete logic"
```

---

### Task 4: Add ListCachedImages, GetCachedImageStatus, and tests

**Files:**
- Modify: `operator/imagecache.go`
- Modify: `operator/imagecache_test.go`

- [ ] **Step 1: Write failing tests**

Add to `operator/imagecache_test.go`:

```go
func TestDeriveStatus(t *testing.T) {
	tests := []struct {
		name       string
		desired    int32
		ready      int32
		wantStatus string
	}{
		{name: "error when 0 desired", desired: 0, ready: 0, wantStatus: "error"},
		{name: "ready when all running", desired: 3, ready: 3, wantStatus: "ready"},
		{name: "cached-with-errors when 0 ready", desired: 3, ready: 0, wantStatus: "cached-with-errors"},
		{name: "pulling when partially ready", desired: 3, ready: 1, wantStatus: "pulling"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantStatus, deriveCacheStatus(tt.desired, tt.ready))
		})
	}
}

func TestListCachedImages(t *testing.T) {
	const (
		ns     = "vice-apps"
		secret = "vice-image-pull-secret"
	)

	mgr := NewImageCacheManager(nil, ns, secret)
	image1 := "harbor.cyverse.org/de/vice-proxy:latest"
	image2 := "harbor.cyverse.org/de/porklock:latest"
	slug1 := slugifyImage(image1)
	slug2 := slugifyImage(image2)
	ds1 := mgr.buildCacheDaemonSet(image1, slug1)
	ds2 := mgr.buildCacheDaemonSet(image2, slug2)

	// Simulate status fields.
	ds1.Status = appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, NumberReady: 3}
	ds2.Status = appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, NumberReady: 1, NumberUnavailable: 2}

	cs := fake.NewSimpleClientset(ds1, ds2)
	mgr = NewImageCacheManager(cs, ns, secret)

	images, err := mgr.ListCachedImages(context.Background())
	require.NoError(t, err)
	assert.Len(t, images, 2)

	// Find each image in the results.
	statusByImage := make(map[string]ImageCacheStatus)
	for _, s := range images {
		statusByImage[s.Image] = s
	}

	s1 := statusByImage[image1]
	assert.Equal(t, "ready", s1.Status)
	assert.Equal(t, int32(3), s1.Ready)

	s2 := statusByImage[image2]
	assert.Equal(t, "pulling", s2.Status)
	assert.Equal(t, int32(1), s2.Ready)
}

func TestGetCachedImageStatus(t *testing.T) {
	const (
		ns     = "vice-apps"
		secret = "vice-image-pull-secret"
		image  = "harbor.cyverse.org/de/vice-proxy:latest"
	)

	mgr := NewImageCacheManager(nil, ns, secret)
	slug := slugifyImage(image)
	ds := mgr.buildCacheDaemonSet(image, slug)
	ds.Status = appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, NumberReady: 3}

	cs := fake.NewSimpleClientset(ds)
	mgr = NewImageCacheManager(cs, ns, secret)

	status, err := mgr.GetCachedImageStatus(context.Background(), slug)
	require.NoError(t, err)
	assert.Equal(t, image, status.Image)
	assert.Equal(t, "ready", status.Status)

	// Non-existent slug returns error.
	_, err = mgr.GetCachedImageStatus(context.Background(), "nonexistent")
	assert.Error(t, err)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./operator/ -run "TestDeriveStatus|TestListCachedImages|TestGetCachedImageStatus" -v`
Expected: compilation error.

- [ ] **Step 3: Implement deriveCacheStatus, ListCachedImages, GetCachedImageStatus**

Add to `operator/imagecache.go`. Add `"k8s.io/apimachinery/pkg/labels"` to imports.

```go
// deriveCacheStatus computes the cache status string from DaemonSet status
// fields. Evaluated in order: error, ready, cached-with-errors, pulling.
func deriveCacheStatus(desired, ready int32) string {
	if desired == 0 {
		return "error"
	}
	if ready == desired {
		return "ready"
	}
	if ready == 0 {
		return "cached-with-errors"
	}
	return "pulling"
}

// ListCachedImages returns the status of all image cache DaemonSets in the
// namespace.
func (m *ImageCacheManager) ListCachedImages(ctx context.Context) ([]ImageCacheStatus, error) {
	client := m.clientset.AppsV1().DaemonSets(m.namespace)

	sel := labels.SelectorFromSet(labels.Set{
		labelManagedBy: valueManagedBy,
		labelPurpose:   valuePurpose,
	})

	list, err := client.List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return nil, fmt.Errorf("listing image cache DaemonSets: %w", err)
	}

	result := make([]ImageCacheStatus, 0, len(list.Items))
	for _, ds := range list.Items {
		result = append(result, ImageCacheStatus{
			Image:   ds.Annotations[annotationImage],
			ID:      ds.Labels[labelImageCacheID],
			Ready:   ds.Status.NumberReady,
			Desired: ds.Status.DesiredNumberScheduled,
			Status:  deriveCacheStatus(ds.Status.DesiredNumberScheduled, ds.Status.NumberReady),
		})
	}
	return result, nil
}

// GetCachedImageStatus returns the status of a single cached image by its slug
// ID. Returns an error if the DaemonSet is not found.
func (m *ImageCacheManager) GetCachedImageStatus(ctx context.Context, id string) (*ImageCacheStatus, error) {
	client := m.clientset.AppsV1().DaemonSets(m.namespace)
	dsName := dsNamePrefix + id

	ds, err := client.Get(ctx, dsName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting image cache DaemonSet %s: %w", dsName, err)
	}

	return &ImageCacheStatus{
		Image:   ds.Annotations[annotationImage],
		ID:      ds.Labels[labelImageCacheID],
		Ready:   ds.Status.NumberReady,
		Desired: ds.Status.DesiredNumberScheduled,
		Status:  deriveCacheStatus(ds.Status.DesiredNumberScheduled, ds.Status.NumberReady),
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./operator/ -run "TestDeriveStatus|TestListCachedImages|TestGetCachedImageStatus" -v`
Expected: all PASS

- [ ] **Step 5: Run full test suite**

Run: `go build ./... && go test ./...`
Expected: all build and pass.

- [ ] **Step 6: Commit**

```bash
git add operator/imagecache.go operator/imagecache_test.go
git commit -m "Add ListCachedImages, GetCachedImageStatus, and status derivation"
```

---

## Chunk 2: HTTP handlers, wiring, and RBAC

### Task 5: Add ImageCacheManager to Operator and wire in main.go

**Files:**
- Modify: `operator/handlers.go:22-61` — add field and constructor param
- Modify: `cmd/vice-operator/main.go:145-146` — pass new param

- [ ] **Step 1: Add `imageCache` field to Operator struct**

In `operator/handlers.go`, add after `capacityCalc`:

```go
	imageCache   *ImageCacheManager
```

- [ ] **Step 2: Add parameter to NewOperator**

Update `NewOperator` signature to accept `imageCache *ImageCacheManager` as the
last parameter. Add it to the returned struct. No nil-check needed since image
caching is always available (the manager is always created).

- [ ] **Step 3: Update main.go to create ImageCacheManager and pass it**

In `cmd/vice-operator/main.go`, after the `capacityCalc` line:

```go
	imageCache := operator.NewImageCacheManager(clientset, namespace, imagePullSecret)
	op := operator.NewOperator(clientset, gwClient, namespace, rt, ingressClass, gpuVendor, capacityCalc, imageCache)
```

- [ ] **Step 4: Build to verify compilation**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 5: Run full test suite**

Run: `go test ./...`
Expected: all pass (existing tests need updating for the new NewOperator param —
check `operator/handlers_test.go` for any calls to `NewOperator` and add the
`imageCache` argument).

- [ ] **Step 6: Commit**

```bash
git add operator/handlers.go cmd/vice-operator/main.go operator/handlers_test.go
git commit -m "Wire ImageCacheManager into Operator and main.go"
```

---

### Task 6: Add HTTP handler methods with Swagger annotations

**Files:**
- Modify: `operator/handlers.go` — add 5 handler methods

- [ ] **Step 1: Add HandleCacheImages (PUT /image-cache)**

```go
// HandleCacheImages creates or updates cache DaemonSets for the given images.
//
//	@Summary		Cache container images
//	@Description	Creates a DaemonSet per image to pre-pull it onto every node.
//	@Description	Each DaemonSet uses an init container with the target image and
//	@Description	a pause main container. For distroless/scratch images lacking
//	@Description	"true", the init container will CrashLoopBackOff — this is expected
//	@Description	and the image is still cached. Status will show "cached-with-errors".
//	@Tags			image-cache
//	@Accept			json
//	@Produce		json
//	@Param			request	body		ImageCacheRequest		true	"Images to cache"
//	@Success		200		{object}	ImageCacheBulkResponse	"All images cached successfully"
//	@Success		207		{object}	ImageCacheBulkResponse	"Partial success"
//	@Failure		400		{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/image-cache [put]
func (o *Operator) HandleCacheImages(c echo.Context) error {
	ctx := c.Request().Context()

	var req ImageCacheRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, common.ErrorResponse{Message: "invalid request body"})
	}
	if len(req.Images) == 0 {
		return c.JSON(http.StatusBadRequest, common.ErrorResponse{Message: "images list must not be empty"})
	}

	results := make([]ImageCacheResult, 0, len(req.Images))
	hasError := false
	for _, image := range req.Images {
		if err := o.imageCache.EnsureImageCached(ctx, image); err != nil {
			results = append(results, ImageCacheResult{Image: image, Status: "error", Error: err.Error()})
			hasError = true
		} else {
			results = append(results, ImageCacheResult{Image: image, Status: "ok"})
		}
	}

	status := http.StatusOK
	if hasError {
		status = http.StatusMultiStatus
	}
	return c.JSON(status, ImageCacheBulkResponse{Results: results})
}
```

- [ ] **Step 2: Add HandleRemoveCachedImages (DELETE /image-cache)**

```go
// HandleRemoveCachedImages removes cache DaemonSets for the given images.
//
//	@Summary		Remove cached images (bulk)
//	@Description	Deletes the cache DaemonSets for the specified images.
//	@Description	Non-existent images are silently ignored (idempotent).
//	@Description	Note: some HTTP clients drop the body on DELETE requests.
//	@Description	Use DELETE /image-cache/:id for single-image removal from browsers.
//	@Tags			image-cache
//	@Accept			json
//	@Produce		json
//	@Param			request	body		ImageCacheRequest		true	"Images to remove"
//	@Success		200		{object}	ImageCacheBulkResponse
//	@Success		207		{object}	ImageCacheBulkResponse	"Partial success"
//	@Failure		400		{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/image-cache [delete]
func (o *Operator) HandleRemoveCachedImages(c echo.Context) error {
	ctx := c.Request().Context()

	var req ImageCacheRequest
	if err := c.Bind(&req); err != nil {
		return c.JSON(http.StatusBadRequest, common.ErrorResponse{Message: "invalid request body"})
	}
	if len(req.Images) == 0 {
		return c.JSON(http.StatusBadRequest, common.ErrorResponse{Message: "images list must not be empty"})
	}

	results := make([]ImageCacheResult, 0, len(req.Images))
	hasError := false
	for _, image := range req.Images {
		if err := o.imageCache.RemoveCachedImage(ctx, image); err != nil {
			results = append(results, ImageCacheResult{Image: image, Status: "error", Error: err.Error()})
			hasError = true
		} else {
			results = append(results, ImageCacheResult{Image: image, Status: "ok"})
		}
	}

	status := http.StatusOK
	if hasError {
		status = http.StatusMultiStatus
	}
	return c.JSON(status, ImageCacheBulkResponse{Results: results})
}
```

- [ ] **Step 3: Add HandleListCachedImages (GET /image-cache)**

```go
// HandleListCachedImages returns the status of all cached images.
//
//	@Summary		List cached images
//	@Description	Returns all image cache DaemonSets with their pull status.
//	@Tags			image-cache
//	@Produce		json
//	@Success		200	{object}	ImageCacheListResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/image-cache [get]
func (o *Operator) HandleListCachedImages(c echo.Context) error {
	ctx := c.Request().Context()

	images, err := o.imageCache.ListCachedImages(ctx)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, common.ErrorResponse{Message: err.Error()})
	}

	return c.JSON(http.StatusOK, ImageCacheListResponse{Images: images})
}
```

- [ ] **Step 4: Add HandleGetCachedImage (GET /image-cache/:id)**

```go
// HandleGetCachedImage returns the status of a single cached image.
//
//	@Summary		Get cached image status
//	@Description	Returns the cache status for a single image by its slug ID
//	@Description	(from the "id" field in list responses).
//	@Tags			image-cache
//	@Produce		json
//	@Param			id	path		string	true	"Image cache slug ID"
//	@Success		200	{object}	ImageCacheStatus
//	@Failure		404	{object}	common.ErrorResponse
//	@Failure		500	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/image-cache/{id} [get]
func (o *Operator) HandleGetCachedImage(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")

	status, err := o.imageCache.GetCachedImageStatus(ctx, id)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return c.JSON(http.StatusNotFound, common.ErrorResponse{Message: fmt.Sprintf("no cached image with id %q", id)})
		}
		return c.JSON(http.StatusInternalServerError, common.ErrorResponse{Message: err.Error()})
	}

	return c.JSON(http.StatusOK, status)
}
```

Note: add `apierrors "k8s.io/apimachinery/pkg/api/errors"` and `"fmt"` to
handlers.go imports.

- [ ] **Step 5: Add HandleDeleteCachedImage (DELETE /image-cache/:id)**

```go
// HandleDeleteCachedImage removes a single cached image by its slug ID.
//
//	@Summary		Remove a cached image
//	@Description	Deletes the cache DaemonSet for the image with the given slug ID.
//	@Description	Returns success even if already absent (idempotent).
//	@Tags			image-cache
//	@Param			id	path	string	true	"Image cache slug ID"
//	@Success		200
//	@Failure		500	{object}	common.ErrorResponse
//	@Security		BasicAuth
//	@Router			/image-cache/{id} [delete]
func (o *Operator) HandleDeleteCachedImage(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")

	if err := o.imageCache.RemoveCachedImageByID(ctx, id); err != nil {
		return c.JSON(http.StatusInternalServerError, common.ErrorResponse{Message: err.Error()})
	}

	return c.NoContent(http.StatusOK)
}
```

This requires a `RemoveCachedImageByID` method on `ImageCacheManager`. Add it
to `operator/imagecache.go` in Task 3 alongside `RemoveCachedImage`:

```go
// RemoveCachedImageByID deletes the cache DaemonSet with the given slug ID.
// Returns nil if the DaemonSet doesn't exist (idempotent).
func (m *ImageCacheManager) RemoveCachedImageByID(ctx context.Context, id string) error {
	dsName := dsNamePrefix + id
	client := m.clientset.AppsV1().DaemonSets(m.namespace)

	err := client.Delete(ctx, dsName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("deleting image cache DaemonSet %s: %w", dsName, err)
	}
	log.Infof("deleted image cache DaemonSet %s", dsName)
	return nil
}
```

- [ ] **Step 6: Build to verify**

Run: `go build ./...`
Expected: clean build.

- [ ] **Step 7: Commit**

```bash
git add operator/handlers.go
git commit -m "Add image cache HTTP handlers with Swagger annotations"
```

---

### Task 7: Register routes and update RBAC

**Files:**
- Modify: `cmd/vice-operator/app.go` — register image cache routes
- Modify: `k8s/vice-operator-local.yml` — add daemonsets to ClusterRole

- [ ] **Step 1: Register routes in app.go**

After the `analyses` group block (after line 65), add:

```go
	// Image cache routes.
	api.PUT("/image-cache", op.HandleCacheImages)
	api.DELETE("/image-cache", op.HandleRemoveCachedImages)
	api.GET("/image-cache", op.HandleListCachedImages)
	api.GET("/image-cache/:id", op.HandleGetCachedImage)
	api.DELETE("/image-cache/:id", op.HandleDeleteCachedImage)
```

- [ ] **Step 2: Update RBAC in vice-operator-local.yml**

Change the `apps` API group resources line from:

```yaml
    resources: ["deployments"]
```

to:

```yaml
    resources: ["deployments", "daemonsets"]
```

- [ ] **Step 3: Build and run full test suite**

Run: `go build ./... && go test ./...`
Expected: all pass.

- [ ] **Step 4: Regenerate Swagger docs**

Run: `swag init -g cmd/vice-operator/app.go -o operatordocs --instanceName operator`

If `swag` is not installed, install with: `go install github.com/swaggo/swag/cmd/swag@latest`

- [ ] **Step 5: Commit**

```bash
git add cmd/vice-operator/app.go k8s/vice-operator-local.yml operatordocs/
git commit -m "Register image cache routes, update RBAC, regenerate Swagger docs"
```

---

## Verification

After all tasks:

- `go build ./...` — compiles cleanly
- `go test ./...` — all tests pass
- `goimports -w operator/imagecache.go operator/imagecache_test.go operator/handlers.go cmd/vice-operator/main.go cmd/vice-operator/app.go` — formatted
- `golangci-lint run ./...` — no warnings
- Manual: deploy to local cluster, verify:
  - `curl -u user:pass -X PUT -d '{"images":["nginx:latest"]}' http://localhost:10000/image-cache`
  - `curl -u user:pass http://localhost:10000/image-cache` — shows cached image with status
  - `curl -u user:pass -X DELETE http://localhost:10000/image-cache/nginx-latest-<hash>` — removes it
  - Check `kubectl get daemonsets -n vice-apps -l purpose=image-cache` to verify DaemonSets
