package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
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
	pullContainer := ds.Spec.Template.Spec.InitContainers[0]
	assert.Equal(t, image, pullContainer.Image)
	assert.Equal(t, []string{"true"}, pullContainer.Command)
	assert.Equal(t, apiv1.PullAlways, pullContainer.ImagePullPolicy)

	// Main container is pause.
	require.Len(t, ds.Spec.Template.Spec.Containers, 1)
	pauseContainer := ds.Spec.Template.Spec.Containers[0]
	assert.Equal(t, pauseImage, pauseContainer.Image)
	assert.Equal(t, apiv1.PullIfNotPresent, pauseContainer.ImagePullPolicy)

	// Image pull secret.
	require.Len(t, ds.Spec.Template.Spec.ImagePullSecrets, 1)
	assert.Equal(t, "vice-image-pull-secret", ds.Spec.Template.Spec.ImagePullSecrets[0].Name)

	// Tolerations.
	assert.Len(t, ds.Spec.Template.Spec.Tolerations, 2)
}

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
			name:     "no-op when image matches",
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

func TestEnsureImageCachedInvalidRef(t *testing.T) {
	cs := fake.NewSimpleClientset()
	mgr := NewImageCacheManager(cs, "vice-apps", "vice-image-pull-secret")

	err := mgr.EnsureImageCached(context.Background(), "")
	assert.Error(t, err, "empty image should be rejected")

	err = mgr.EnsureImageCached(context.Background(), "bad image!")
	assert.Error(t, err, "invalid image should be rejected")
}

func TestRemoveCachedImageByID(t *testing.T) {
	const (
		ns     = "vice-apps"
		secret = "vice-image-pull-secret"
		image  = "harbor.cyverse.org/de/vice-proxy:latest"
	)

	// Create a DaemonSet, then remove by ID.
	mgr := NewImageCacheManager(nil, ns, secret)
	slug := slugifyImage(image)
	ds := mgr.buildCacheDaemonSet(image, slug)
	cs := fake.NewSimpleClientset(ds)
	mgr = NewImageCacheManager(cs, ns, secret)

	err := mgr.RemoveCachedImageByID(context.Background(), slug)
	require.NoError(t, err)

	list, err := cs.AppsV1().DaemonSets(ns).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	assert.Empty(t, list.Items)

	// Removing again should succeed (idempotent).
	err = mgr.RemoveCachedImageByID(context.Background(), slug)
	require.NoError(t, err)
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

// TestRefreshCachedImage exercises all four paths:
//  1. Invalid image ref → validation error, no API calls.
//  2. Missing DaemonSet → wrapped NotFound error.
//  3. Update failure → wrapped error from client.Update.
//  4. Happy path → the pod template carries a fresh restartedAt annotation.
func TestRefreshCachedImage(t *testing.T) {
	const (
		ns     = "vice-apps"
		secret = "vice-image-pull-secret"
		image  = "harbor.cyverse.org/de/vice-proxy:latest"
	)
	slug := slugifyImage(image)
	dsName := dsNamePrefix + slug

	t.Run("invalid image ref rejected", func(t *testing.T) {
		cs := fake.NewSimpleClientset()
		mgr := NewImageCacheManager(cs, ns, secret)

		err := mgr.RefreshCachedImage(context.Background(), "bad image!")
		require.Error(t, err)
	})

	t.Run("missing DaemonSet surfaces not-found", func(t *testing.T) {
		cs := fake.NewSimpleClientset()
		mgr := NewImageCacheManager(cs, ns, secret)

		err := mgr.RefreshCachedImage(context.Background(), image)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no cache DaemonSet found")
	})

	t.Run("update failure is wrapped with DS name", func(t *testing.T) {
		mgr := NewImageCacheManager(nil, ns, secret)
		existing := mgr.buildCacheDaemonSet(image, slug)
		cs := fake.NewSimpleClientset(existing)
		mgr = NewImageCacheManager(cs, ns, secret)

		// Inject an Update failure via the fake clientset's reactor chain.
		cs.PrependReactor("update", "daemonsets", func(action clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("injected update failure")
		})

		err := mgr.RefreshCachedImage(context.Background(), image)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "restarting cache DaemonSet")
		assert.Contains(t, err.Error(), dsName)
	})

	t.Run("happy path stamps restartedAt annotation", func(t *testing.T) {
		mgr := NewImageCacheManager(nil, ns, secret)
		existing := mgr.buildCacheDaemonSet(image, slug)
		// Clear any pre-existing template annotations so we can assert
		// the restartedAt key is created fresh rather than overwritten.
		existing.Spec.Template.Annotations = nil

		cs := fake.NewSimpleClientset(existing)
		mgr = NewImageCacheManager(cs, ns, secret)

		err := mgr.RefreshCachedImage(context.Background(), image)
		require.NoError(t, err)

		updated, err := cs.AppsV1().DaemonSets(ns).Get(context.Background(), dsName, metav1.GetOptions{})
		require.NoError(t, err)
		require.NotNil(t, updated.Spec.Template.Annotations)
		assert.NotEmpty(t, updated.Spec.Template.Annotations["de.cyverse.org/restartedAt"])
	})
}

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

func TestHandleCacheImages(t *testing.T) {
	op, _, _ := newTestOperator(t, 10)

	tests := []struct {
		name       string
		body       ImageCacheRequest
		wantStatus int
	}{
		{
			name:       "successful cache returns 200",
			body:       ImageCacheRequest{Images: []string{"nginx:latest"}},
			wantStatus: http.StatusOK,
		},
		{
			name:       "empty list returns 400",
			body:       ImageCacheRequest{Images: []string{}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "partial failure returns 207",
			body:       ImageCacheRequest{Images: []string{"nginx:latest", "bad image!!!"}},
			wantStatus: http.StatusMultiStatus,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.body)
			e := echo.New()
			req := httptest.NewRequest(http.MethodPut, "/image-cache", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := op.HandleCacheImages(c)
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

// TestHandleRefreshCachedImages exercises the bulk refresh handler.
// Cases mirror TestHandleCacheImages for parity:
//  - all images refreshed → 200
//  - empty list → 400
//  - one image exists, one doesn't → 207 with the missing one's error in its result row
//
// The refresh path requires a pre-existing DaemonSet per image, so the
// "successful refresh" case seeds one first via a direct EnsureImageCached
// call to keep the test focused on handler behavior rather than on how
// DaemonSets get created.
func TestHandleRefreshCachedImages(t *testing.T) {
	tests := []struct {
		name       string
		seed       []string // images to EnsureImageCached before invoking the handler
		body       ImageCacheRequest
		wantStatus int
		wantOK     int // expected "ok" rows in the response
		wantErr    int // expected "error" rows
	}{
		{
			name:       "successful refresh returns 200",
			seed:       []string{"nginx:latest"},
			body:       ImageCacheRequest{Images: []string{"nginx:latest"}},
			wantStatus: http.StatusOK,
			wantOK:     1,
			wantErr:    0,
		},
		{
			name:       "empty list returns 400",
			body:       ImageCacheRequest{Images: []string{}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "partial failure returns 207",
			seed:       []string{"nginx:latest"}, // exists; the other doesn't
			body:       ImageCacheRequest{Images: []string{"nginx:latest", "missing-image:v1"}},
			wantStatus: http.StatusMultiStatus,
			wantOK:     1,
			wantErr:    1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op, _, _ := newTestOperator(t, 10)

			for _, img := range tt.seed {
				require.NoError(t, op.imageCache.EnsureImageCached(context.Background(), img))
			}

			body, _ := json.Marshal(tt.body)
			e := echo.New()
			req := httptest.NewRequest(http.MethodPost, "/image-cache/refresh", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := op.HandleRefreshCachedImages(c)
			require.NoError(t, err) // handler writes the response directly
			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantStatus == http.StatusBadRequest {
				return
			}

			var resp ImageCacheBulkResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			var gotOK, gotErr int
			for _, r := range resp.Results {
				switch r.Status {
				case "ok":
					gotOK++
				case "error":
					gotErr++
				}
			}
			assert.Equal(t, tt.wantOK, gotOK, "unexpected ok count")
			assert.Equal(t, tt.wantErr, gotErr, "unexpected error count")
		})
	}
}

func TestHandleGetCachedImageNotFound(t *testing.T) {
	op, _, _ := newTestOperator(t, 10)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("nonexistent-slug")

	err := op.HandleGetCachedImage(c)
	require.NoError(t, err) // handler writes response directly
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
