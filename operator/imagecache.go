package operator

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

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
// It requires the reference to start with an alphanumeric character and contain
// only alphanumeric characters, dots, underscores, slashes, colons, at-signs,
// and hyphens.
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

// NewImageCacheManager creates a manager that controls image cache DaemonSets
// within the given namespace. If imagePullSecretName is empty, cache DaemonSets
// will not reference an image pull secret (suitable for public images only).
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
		return errors.New("image reference must not be empty")
	}
	if !imageRefPattern.MatchString(image) {
		return fmt.Errorf("invalid image reference: %q", image)
	}
	return nil
}

// imagePullSecrets returns the image pull secrets list for cache DaemonSets.
// Returns nil when no secret name is configured, which omits the field from
// the pod spec entirely (allowing public images to be cached without credentials).
func (m *ImageCacheManager) imagePullSecrets() []apiv1.LocalObjectReference {
	if m.imagePullSecretName == "" {
		return nil
	}
	return []apiv1.LocalObjectReference{{Name: m.imagePullSecretName}}
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
	for i := range len(s) {
		if s[i] == c && prev == c {
			continue
		}
		b.WriteByte(s[i])
		prev = s[i]
	}
	return b.String()
}

// buildCacheDaemonSet constructs a DaemonSet that pre-pulls the given image
// onto every node. The init container references the target image and runs
// "true" to exit immediately; K8s pulls the image before running the command.
//
// For distroless or scratch-based images that lack "true", the init container
// will fail with CrashLoopBackOff. This is expected — the image is still pulled
// and cached on each node. The status API reports these as "cached-with-errors".
func (m *ImageCacheManager) buildCacheDaemonSet(image, slug string) *appsv1.DaemonSet {
	// dsLabels is named to avoid shadowing the imported "k8s.io/apimachinery/pkg/labels" package.
	dsLabels := map[string]string{
		labelManagedBy:    valueManagedBy,
		labelPurpose:      valuePurpose,
		labelImageCacheID: slug,
	}

	// The init container needs enough memory to start the entrypoint process
	// (even just "true") without being OOM-killed. The pause container is a
	// tiny static binary and can run with minimal resources.
	initResources := apiv1.ResourceRequirements{
		Requests: apiv1.ResourceList{
			apiv1.ResourceCPU:    resource.MustParse("1m"),
			apiv1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Limits: apiv1.ResourceList{
			apiv1.ResourceCPU:    resource.MustParse("10m"),
			apiv1.ResourceMemory: resource.MustParse("64Mi"),
		},
	}
	pauseResources := apiv1.ResourceRequirements{
		Requests: apiv1.ResourceList{
			apiv1.ResourceCPU:    resource.MustParse("1m"),
			apiv1.ResourceMemory: resource.MustParse("16Mi"),
		},
		Limits: apiv1.ResourceList{
			apiv1.ResourceCPU:    resource.MustParse("1m"),
			apiv1.ResourceMemory: resource.MustParse("16Mi"),
		},
	}

	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      dsNamePrefix + slug,
			Namespace: m.namespace,
			Labels:    dsLabels,
			Annotations: map[string]string{
				annotationImage: image,
			},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: dsLabels,
			},
			Template: apiv1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: dsLabels,
				},
				Spec: apiv1.PodSpec{
					ImagePullSecrets: m.imagePullSecrets(),
					InitContainers: []apiv1.Container{
						{
							Name:            "pull",
							Image:           image,
							Command:         []string{"true"},
							ImagePullPolicy: apiv1.PullAlways,
							Resources:       initResources,
						},
					},
					Containers: []apiv1.Container{
						{
							Name:            "pause",
							Image:           pauseImage,
							ImagePullPolicy: apiv1.PullIfNotPresent,
							Resources:       pauseResources,
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

// RefreshCachedImage forces the cache DaemonSet for the given image to re-pull
// by updating a restart annotation on the pod template, which triggers a rolling
// update. The init container's PullAlways policy causes containerd to fetch the
// latest manifest. This is needed when a new image is pushed under the same tag.
func (m *ImageCacheManager) RefreshCachedImage(ctx context.Context, image string) error {
	if err := validateImageRef(image); err != nil {
		return err
	}

	slug := slugifyImage(image)
	dsName := dsNamePrefix + slug
	client := m.clientset.AppsV1().DaemonSets(m.namespace)

	ds, err := client.Get(ctx, dsName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("no cache DaemonSet found for image %q", image)
	}
	if err != nil {
		return fmt.Errorf("getting cache DaemonSet %s: %w", dsName, err)
	}

	// Restart the DaemonSet by adding/updating a restart annotation on the
	// pod template. This triggers a rolling update of all pods, which forces
	// containerd to re-pull the image due to PullAlways on the init container.
	if ds.Spec.Template.Annotations == nil {
		ds.Spec.Template.Annotations = make(map[string]string)
	}
	ds.Spec.Template.Annotations["de.cyverse.org/restartedAt"] = metav1.Now().UTC().Format(time.RFC3339)

	if _, err := client.Update(ctx, ds, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("restarting cache DaemonSet %s: %w", dsName, err)
	}

	log.Infof("refreshed image cache DaemonSet %s for %s", dsName, image)
	return nil
}

// RemoveCachedImage deletes the cache DaemonSet for the given image.
// Returns nil if the DaemonSet doesn't exist (idempotent).
func (m *ImageCacheManager) RemoveCachedImage(ctx context.Context, image string) error {
	if err := validateImageRef(image); err != nil {
		return err
	}
	return m.RemoveCachedImageByID(ctx, slugifyImage(image))
}

// RemoveCachedImageByID deletes the cache DaemonSet with the given slug ID.
// Returns nil if the DaemonSet doesn't exist (idempotent).
func (m *ImageCacheManager) RemoveCachedImageByID(ctx context.Context, id string) error {
	dsName := dsNamePrefix + id
	err := m.clientset.AppsV1().DaemonSets(m.namespace).Delete(ctx, dsName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("deleting image cache DaemonSet %s: %w", dsName, err)
	}
	log.Infof("deleted image cache DaemonSet %s", dsName)
	return nil
}

// deriveCacheStatus computes the cache status string from DaemonSet status
// fields. Evaluated in order: error, ready, cached-with-errors, pulling.
//
// Note: "cached-with-errors" is returned when ready==0 and desired>0. This
// covers both distroless images where the init container CrashLoopBackOff
// (image is still cached) and newly created DaemonSets where pods haven't
// started yet. We cannot distinguish these cases from aggregate counts alone;
// pod-level inspection would be needed. In practice, the transient "initial
// pull" state resolves quickly, while CrashLoopBackOff is persistent.
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

// cacheStatusFromDS builds an ImageCacheStatus from a DaemonSet.
func cacheStatusFromDS(ds *appsv1.DaemonSet) ImageCacheStatus {
	return ImageCacheStatus{
		Image:   ds.Annotations[annotationImage],
		ID:      ds.Labels[labelImageCacheID],
		Ready:   ds.Status.NumberReady,
		Desired: ds.Status.DesiredNumberScheduled,
		Status:  deriveCacheStatus(ds.Status.DesiredNumberScheduled, ds.Status.NumberReady),
	}
}

// ListCachedImages returns the status of all image cache DaemonSets in the
// namespace.
func (m *ImageCacheManager) ListCachedImages(ctx context.Context) ([]ImageCacheStatus, error) {
	sel := labels.SelectorFromSet(labels.Set{
		labelManagedBy: valueManagedBy,
		labelPurpose:   valuePurpose,
	})

	list, err := m.clientset.AppsV1().DaemonSets(m.namespace).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return nil, fmt.Errorf("listing image cache DaemonSets: %w", err)
	}

	result := make([]ImageCacheStatus, 0, len(list.Items))
	for i := range list.Items {
		result = append(result, cacheStatusFromDS(&list.Items[i]))
	}
	return result, nil
}

// GetCachedImageStatus returns the status of a single cached image by its slug
// ID. Returns an error if the DaemonSet is not found.
func (m *ImageCacheManager) GetCachedImageStatus(ctx context.Context, id string) (*ImageCacheStatus, error) {
	dsName := dsNamePrefix + id
	ds, err := m.clientset.AppsV1().DaemonSets(m.namespace).Get(ctx, dsName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting image cache DaemonSet %s: %w", dsName, err)
	}
	status := cacheStatusFromDS(ds)
	return &status, nil
}
