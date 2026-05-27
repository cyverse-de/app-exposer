package operator

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// DaemonSetImageCacheManager manages image cache DaemonSets in a namespace.
// One DaemonSet per cached image; the init-container pull populates the local
// containerd store on every matching node.
type DaemonSetImageCacheManager struct {
	clientset           kubernetes.Interface
	namespace           string
	imagePullSecretName string
}

// NewDaemonSetImageCacheManager creates a manager that controls image cache
// DaemonSets within the given namespace. If imagePullSecretName is empty,
// cache DaemonSets will not reference an image pull secret (suitable for
// public images only).
func NewDaemonSetImageCacheManager(clientset kubernetes.Interface, namespace, imagePullSecretName string) *DaemonSetImageCacheManager {
	return &DaemonSetImageCacheManager{
		clientset:           clientset,
		namespace:           namespace,
		imagePullSecretName: imagePullSecretName,
	}
}

// buildCacheDaemonSet constructs a DaemonSet that pre-pulls the given image
// onto every node. The init container references the target image and runs
// "true" to exit immediately; K8s pulls the image before running the command.
//
// For distroless or scratch-based images that lack "true", the init container
// will fail with CrashLoopBackOff. This is expected — the image is still pulled
// and cached on each node. The status API reports these as "cached-with-errors".
func (m *DaemonSetImageCacheManager) buildCacheDaemonSet(image, slug string) *appsv1.DaemonSet {
	dsLabels := cacheResourceLabels(slug)

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
			Name:      cacheNamePrefix + slug,
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
					ImagePullSecrets: imagePullSecretsFor(m.imagePullSecretName),
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
					Tolerations: cachePodTolerations(),
				},
			},
		},
	}
}

// EnsureImageCached creates or updates a cache DaemonSet for the given image.
// If a DaemonSet already exists with the correct image annotation, this is a
// no-op.
func (m *DaemonSetImageCacheManager) EnsureImageCached(ctx context.Context, image string) error {
	if err := validateImageRef(image); err != nil {
		return err
	}

	slug := slugifyImage(image)
	dsName := cacheNamePrefix + slug
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
func (m *DaemonSetImageCacheManager) RefreshCachedImage(ctx context.Context, image string) error {
	if err := validateImageRef(image); err != nil {
		return err
	}

	slug := slugifyImage(image)
	dsName := cacheNamePrefix + slug
	client := m.clientset.AppsV1().DaemonSets(m.namespace)

	ds, err := client.Get(ctx, dsName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("no cache DaemonSet found for image %q", image)
	}
	if err != nil {
		return fmt.Errorf("getting cache DaemonSet %s: %w", dsName, err)
	}

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
func (m *DaemonSetImageCacheManager) RemoveCachedImage(ctx context.Context, image string) error {
	if err := validateImageRef(image); err != nil {
		return err
	}
	return m.RemoveCachedImageByID(ctx, slugifyImage(image))
}

// RemoveCachedImageByID deletes the cache DaemonSet with the given slug ID.
// Returns nil if the DaemonSet doesn't exist (idempotent).
func (m *DaemonSetImageCacheManager) RemoveCachedImageByID(ctx context.Context, id string) error {
	dsName := cacheNamePrefix + id
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
func (m *DaemonSetImageCacheManager) ListCachedImages(ctx context.Context) ([]ImageCacheStatus, error) {
	list, err := m.clientset.AppsV1().DaemonSets(m.namespace).List(ctx, metav1.ListOptions{LabelSelector: cacheManagedBySelector().String()})
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
func (m *DaemonSetImageCacheManager) GetCachedImageStatus(ctx context.Context, id string) (*ImageCacheStatus, error) {
	dsName := cacheNamePrefix + id
	ds, err := m.clientset.AppsV1().DaemonSets(m.namespace).Get(ctx, dsName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting image cache DaemonSet %s: %w", dsName, err)
	}
	status := cacheStatusFromDS(ds)
	return &status, nil
}
