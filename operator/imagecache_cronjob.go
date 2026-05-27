package operator

import (
	"context"
	"fmt"

	"github.com/cyverse-de/app-exposer/constants"
	batchv1 "k8s.io/api/batch/v1"
	apiv1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// k8sMaxNameLen is the maximum length of a Kubernetes object name.
const k8sMaxNameLen = 253

// refreshJobTTLSeconds is the TTLSecondsAfterFinished applied to refresh
// Jobs so they self-clean a few minutes after completing.
const refreshJobTTLSeconds int32 = 300

// CronJobImageCacheManager manages image cache CronJobs in a namespace. One
// CronJob per cached image; each scheduled Job pulls the target image,
// which on EKS Auto Mode nodes is transparently routed through an ECR
// pull-through cache repository — populating ECR rather than node-local
// containerd. Deletion removes only the CronJob; image eviction is handled
// by ECR lifecycle policies, outside vice-operator.
type CronJobImageCacheManager struct {
	clientset           kubernetes.Interface
	namespace           string
	imagePullSecretName string
	schedule            string
}

// NewCronJobImageCacheManager creates a manager that controls image cache
// CronJobs within the given namespace. The schedule is a standard cron
// expression applied to every cached image; the caller is responsible for
// validating it before construction. imagePullSecretName behaves the same
// as in the DaemonSet manager — empty means no secret is referenced.
func NewCronJobImageCacheManager(clientset kubernetes.Interface, namespace, imagePullSecretName, schedule string) *CronJobImageCacheManager {
	return &CronJobImageCacheManager{
		clientset:           clientset,
		namespace:           namespace,
		imagePullSecretName: imagePullSecretName,
		schedule:            schedule,
	}
}

func (m *CronJobImageCacheManager) imagePullSecrets() []apiv1.LocalObjectReference {
	if m.imagePullSecretName == "" {
		return nil
	}
	return []apiv1.LocalObjectReference{{Name: m.imagePullSecretName}}
}

// buildCacheCronJob constructs a CronJob that pulls the given image on its
// configured schedule. The Job runs a single container with the target
// image and command ["true"]; K8s pulls the image before running the
// command. On EKS Auto Mode nodes configured with an ECR pull-through
// cache, this pull populates ECR transparently.
//
// Distroless or scratch-based images that lack "true" will produce failed
// Jobs, but the image is still pulled and cached in ECR (the pull happens
// before the entrypoint runs). The status API surfaces those as
// "cached-with-errors".
func (m *CronJobImageCacheManager) buildCacheCronJob(image, slug string) *batchv1.CronJob {
	cjLabels := map[string]string{
		labelManagedBy:    valueManagedBy,
		labelPurpose:      valuePurpose,
		labelImageCacheID: slug,
	}

	pullResources := apiv1.ResourceRequirements{
		Requests: apiv1.ResourceList{
			apiv1.ResourceCPU:    resource.MustParse("1m"),
			apiv1.ResourceMemory: resource.MustParse("64Mi"),
		},
		Limits: apiv1.ResourceList{
			apiv1.ResourceCPU:    resource.MustParse("10m"),
			apiv1.ResourceMemory: resource.MustParse("64Mi"),
		},
	}

	podSpec := apiv1.PodSpec{
		RestartPolicy:    apiv1.RestartPolicyNever,
		ImagePullSecrets: m.imagePullSecrets(),
		Containers: []apiv1.Container{
			{
				Name:            "pull",
				Image:           image,
				Command:         []string{"true"},
				ImagePullPolicy: apiv1.PullAlways,
				Resources:       pullResources,
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
	}

	jobSpec := batchv1.JobSpec{
		BackoffLimit: constants.Int32Ptr(0),
		Template: apiv1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{Labels: cjLabels},
			Spec:       podSpec,
		},
	}

	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cacheNamePrefix + slug,
			Namespace: m.namespace,
			Labels:    cjLabels,
			Annotations: map[string]string{
				annotationImage: image,
			},
		},
		Spec: batchv1.CronJobSpec{
			Schedule:                   m.schedule,
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			SuccessfulJobsHistoryLimit: constants.Int32Ptr(1),
			FailedJobsHistoryLimit:     constants.Int32Ptr(1),
			JobTemplate: batchv1.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: cjLabels},
				Spec:       jobSpec,
			},
		},
	}
}

// EnsureImageCached creates or updates a cache CronJob for the given image.
// If a CronJob already exists with the correct image annotation, this is a
// no-op.
func (m *CronJobImageCacheManager) EnsureImageCached(ctx context.Context, image string) error {
	if err := validateImageRef(image); err != nil {
		return err
	}

	slug := slugifyImage(image)
	cjName := cacheNamePrefix + slug
	client := m.clientset.BatchV1().CronJobs(m.namespace)

	existing, err := client.Get(ctx, cjName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		log.Infof("creating image cache CronJob %s for %s", cjName, image)
		cj := m.buildCacheCronJob(image, slug)
		if _, err := client.Create(ctx, cj, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("creating image cache CronJob %s: %w", cjName, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking for existing image cache CronJob %s: %w", cjName, err)
	}

	if existing.Annotations[annotationImage] == image {
		log.Debugf("image cache CronJob %s already has correct image", cjName)
		return nil
	}

	log.Infof("updating image cache CronJob %s from %q to %q", cjName, existing.Annotations[annotationImage], image)
	cj := m.buildCacheCronJob(image, slug)
	cj.ResourceVersion = existing.ResourceVersion
	if _, err := client.Update(ctx, cj, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("updating image cache CronJob %s: %w", cjName, err)
	}
	return nil
}

// RefreshCachedImage triggers an immediate pull by creating a one-off Job
// from the CronJob's job template. The CronJob continues firing on its
// normal schedule afterwards. Mirrors the DaemonSet manager's refresh
// semantics ("re-pull now") so the bulk handler is mode-agnostic.
func (m *CronJobImageCacheManager) RefreshCachedImage(ctx context.Context, image string) error {
	if err := validateImageRef(image); err != nil {
		return err
	}

	slug := slugifyImage(image)
	cjName := cacheNamePrefix + slug
	cjClient := m.clientset.BatchV1().CronJobs(m.namespace)

	cj, err := cjClient.Get(ctx, cjName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return fmt.Errorf("no cache CronJob found for image %q", image)
	}
	if err != nil {
		return fmt.Errorf("getting cache CronJob %s: %w", cjName, err)
	}

	jobSpec := cj.Spec.JobTemplate.Spec.DeepCopy()
	jobSpec.TTLSecondsAfterFinished = constants.Int32Ptr(refreshJobTTLSeconds)

	jobLabels := map[string]string{
		labelManagedBy:    valueManagedBy,
		labelPurpose:      valuePurpose,
		labelImageCacheID: slug,
	}

	// generateName lets the API server append a 5-char suffix so the name is
	// unique even across rapid back-to-back refreshes. The prefix is
	// truncated if needed to stay under the K8s name length limit.
	generatePrefix := refreshJobGeneratePrefix(slug)

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: generatePrefix,
			Namespace:    m.namespace,
			Labels:       jobLabels,
			Annotations:  map[string]string{annotationImage: image},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "batch/v1",
				Kind:       "CronJob",
				Name:       cj.Name,
				UID:        cj.UID,
			}},
		},
		Spec: *jobSpec,
	}

	created, err := m.clientset.BatchV1().Jobs(m.namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating refresh Job for cache CronJob %s: %w", cjName, err)
	}
	log.Infof("refreshed image cache %s for %s via Job %s", cjName, image, created.Name)
	return nil
}

// refreshJobGeneratePrefix returns a generateName prefix for a refresh Job,
// truncated so the API server's 5-char random suffix fits within the K8s
// name limit.
func refreshJobGeneratePrefix(slug string) string {
	prefix := cacheNamePrefix + slug + "-r-"
	const maxPrefix = k8sMaxNameLen - 5
	if len(prefix) > maxPrefix {
		prefix = prefix[:maxPrefix]
	}
	return prefix
}

// RemoveCachedImage deletes the cache CronJob for the given image.
// Returns nil if the CronJob doesn't exist (idempotent). Owned refresh
// Jobs are cleaned up by the K8s garbage collector via OwnerReferences;
// image eviction in ECR is handled by lifecycle policies, outside this
// service.
func (m *CronJobImageCacheManager) RemoveCachedImage(ctx context.Context, image string) error {
	if err := validateImageRef(image); err != nil {
		return err
	}
	return m.RemoveCachedImageByID(ctx, slugifyImage(image))
}

// RemoveCachedImageByID deletes the cache CronJob with the given slug ID.
// Returns nil if the CronJob doesn't exist (idempotent).
func (m *CronJobImageCacheManager) RemoveCachedImageByID(ctx context.Context, id string) error {
	cjName := cacheNamePrefix + id
	err := m.clientset.BatchV1().CronJobs(m.namespace).Delete(ctx, cjName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("deleting image cache CronJob %s: %w", cjName, err)
	}
	log.Infof("deleted image cache CronJob %s", cjName)
	return nil
}

// cacheStatusFromCronJob builds an ImageCacheStatus from a CronJob, mapping
// cron-specific state onto the DaemonSet-shaped (ready, desired, status)
// triple so the API response is the same in both modes.
//
//	Suspended           → desired=0, ready=0, status=error
//	Never scheduled yet → desired=1, ready=0, status=pulling
//	Last run succeeded  → desired=1, ready=1, status=ready
//	Last run failed     → desired=1, ready=0, status=cached-with-errors
func cacheStatusFromCronJob(cj *batchv1.CronJob) ImageCacheStatus {
	desired, ready, status := deriveCronJobCacheStatus(cj)
	return ImageCacheStatus{
		Image:   cj.Annotations[annotationImage],
		ID:      cj.Labels[labelImageCacheID],
		Ready:   ready,
		Desired: desired,
		Status:  status,
	}
}

// deriveCronJobCacheStatus computes the (desired, ready, status) triple
// from a CronJob's spec and status. Split out from cacheStatusFromCronJob
// so it can be unit-tested without constructing a full CronJob.
func deriveCronJobCacheStatus(cj *batchv1.CronJob) (desired, ready int32, status string) {
	if cj.Spec.Suspend != nil && *cj.Spec.Suspend {
		return 0, 0, "error"
	}
	desired = 1

	scheduled := cj.Status.LastScheduleTime
	success := cj.Status.LastSuccessfulTime

	switch {
	case scheduled == nil && success == nil:
		// First run hasn't fired yet — schedule is in place, image not pulled.
		return desired, 0, "pulling"
	case success != nil && (scheduled == nil || !success.Time.Before(scheduled.Time)):
		return desired, 1, "ready"
	default:
		return desired, 0, "cached-with-errors"
	}
}

// ListCachedImages returns the status of all image cache CronJobs in the
// namespace.
func (m *CronJobImageCacheManager) ListCachedImages(ctx context.Context) ([]ImageCacheStatus, error) {
	sel := labels.SelectorFromSet(labels.Set{
		labelManagedBy: valueManagedBy,
		labelPurpose:   valuePurpose,
	})

	list, err := m.clientset.BatchV1().CronJobs(m.namespace).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return nil, fmt.Errorf("listing image cache CronJobs: %w", err)
	}

	result := make([]ImageCacheStatus, 0, len(list.Items))
	for i := range list.Items {
		result = append(result, cacheStatusFromCronJob(&list.Items[i]))
	}
	return result, nil
}

// GetCachedImageStatus returns the status of a single cached image by its
// slug ID. Returns an error if the CronJob is not found.
func (m *CronJobImageCacheManager) GetCachedImageStatus(ctx context.Context, id string) (*ImageCacheStatus, error) {
	cjName := cacheNamePrefix + id
	cj, err := m.clientset.BatchV1().CronJobs(m.namespace).Get(ctx, cjName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("getting image cache CronJob %s: %w", cjName, err)
	}
	status := cacheStatusFromCronJob(cj)
	return &status, nil
}

