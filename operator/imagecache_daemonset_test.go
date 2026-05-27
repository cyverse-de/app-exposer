package operator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
)

func TestBuildCacheDaemonSet(t *testing.T) {
	mgr := NewDaemonSetImageCacheManager(nil, "vice-apps", "vice-image-pull-secret")
	image := "harbor.cyverse.org/de/vice-proxy:latest"
	slug := slugifyImage(image)

	ds := mgr.buildCacheDaemonSet(image, slug)

	// Metadata.
	assert.Equal(t, cacheNamePrefix+slug, ds.Name)
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
	dsName := cacheNamePrefix + slug

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
			existing: NewDaemonSetImageCacheManager(nil, ns, secret).buildCacheDaemonSet(image, slug),
		},
		{
			name: "updates when annotation differs",
			existing: func() *appsv1.DaemonSet {
				ds := NewDaemonSetImageCacheManager(nil, ns, secret).buildCacheDaemonSet("old-image:v1", slug)
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

			mgr := NewDaemonSetImageCacheManager(cs, ns, secret)
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
	mgr := NewDaemonSetImageCacheManager(cs, "vice-apps", "vice-image-pull-secret")

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
	mgr := NewDaemonSetImageCacheManager(nil, ns, secret)
	slug := slugifyImage(image)
	ds := mgr.buildCacheDaemonSet(image, slug)
	cs := fake.NewSimpleClientset(ds)
	mgr = NewDaemonSetImageCacheManager(cs, ns, secret)

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
			mgr := NewDaemonSetImageCacheManager(nil, ns, secret)
			if tt.existing {
				slug := slugifyImage(image)
				ds := mgr.buildCacheDaemonSet(image, slug)
				cs = fake.NewSimpleClientset(ds)
			} else {
				cs = fake.NewSimpleClientset()
			}
			mgr = NewDaemonSetImageCacheManager(cs, ns, secret)

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
	dsName := cacheNamePrefix + slug

	t.Run("invalid image ref rejected", func(t *testing.T) {
		cs := fake.NewSimpleClientset()
		mgr := NewDaemonSetImageCacheManager(cs, ns, secret)

		err := mgr.RefreshCachedImage(context.Background(), "bad image!")
		require.Error(t, err)
	})

	t.Run("missing DaemonSet surfaces not-found", func(t *testing.T) {
		cs := fake.NewSimpleClientset()
		mgr := NewDaemonSetImageCacheManager(cs, ns, secret)

		err := mgr.RefreshCachedImage(context.Background(), image)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no cache DaemonSet found")
	})

	t.Run("update failure is wrapped with DS name", func(t *testing.T) {
		mgr := NewDaemonSetImageCacheManager(nil, ns, secret)
		existing := mgr.buildCacheDaemonSet(image, slug)
		cs := fake.NewSimpleClientset(existing)
		mgr = NewDaemonSetImageCacheManager(cs, ns, secret)

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
		mgr := NewDaemonSetImageCacheManager(nil, ns, secret)
		existing := mgr.buildCacheDaemonSet(image, slug)
		// Clear any pre-existing template annotations so we can assert
		// the restartedAt key is created fresh rather than overwritten.
		existing.Spec.Template.Annotations = nil

		cs := fake.NewSimpleClientset(existing)
		mgr = NewDaemonSetImageCacheManager(cs, ns, secret)

		err := mgr.RefreshCachedImage(context.Background(), image)
		require.NoError(t, err)

		updated, err := cs.AppsV1().DaemonSets(ns).Get(context.Background(), dsName, metav1.GetOptions{})
		require.NoError(t, err)
		require.NotNil(t, updated.Spec.Template.Annotations)
		assert.NotEmpty(t, updated.Spec.Template.Annotations["de.cyverse.org/restartedAt"])
	})
}

func TestListCachedImages(t *testing.T) {
	const (
		ns     = "vice-apps"
		secret = "vice-image-pull-secret"
	)

	mgr := NewDaemonSetImageCacheManager(nil, ns, secret)
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
	mgr = NewDaemonSetImageCacheManager(cs, ns, secret)

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

	mgr := NewDaemonSetImageCacheManager(nil, ns, secret)
	slug := slugifyImage(image)
	ds := mgr.buildCacheDaemonSet(image, slug)
	ds.Status = appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, NumberReady: 3}

	cs := fake.NewSimpleClientset(ds)
	mgr = NewDaemonSetImageCacheManager(cs, ns, secret)

	status, err := mgr.GetCachedImageStatus(context.Background(), slug)
	require.NoError(t, err)
	assert.Equal(t, image, status.Image)
	assert.Equal(t, "ready", status.Status)

	// Non-existent slug returns error.
	_, err = mgr.GetCachedImageStatus(context.Background(), "nonexistent")
	assert.Error(t, err)
}
