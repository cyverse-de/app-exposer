package operator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

const testCronSchedule = "0 2 * * *"

func TestBuildCacheCronJob(t *testing.T) {
	mgr := NewCronJobImageCacheManager(nil, "vice-apps", "vice-image-pull-secret", testCronSchedule)
	image := "harbor.cyverse.org/de/vice-proxy:latest"
	slug := slugifyImage(image)

	cj := mgr.buildCacheCronJob(image, slug)

	assert.Equal(t, cacheNamePrefix+slug, cj.Name)
	assert.Equal(t, "vice-apps", cj.Namespace)
	assert.Equal(t, valueManagedBy, cj.Labels[labelManagedBy])
	assert.Equal(t, valuePurpose, cj.Labels[labelPurpose])
	assert.Equal(t, slug, cj.Labels[labelImageCacheID])
	assert.Equal(t, image, cj.Annotations[annotationImage])

	assert.Equal(t, testCronSchedule, cj.Spec.Schedule)
	assert.Equal(t, batchv1.ForbidConcurrent, cj.Spec.ConcurrencyPolicy)
	require.NotNil(t, cj.Spec.SuccessfulJobsHistoryLimit)
	assert.Equal(t, int32(1), *cj.Spec.SuccessfulJobsHistoryLimit)
	require.NotNil(t, cj.Spec.FailedJobsHistoryLimit)
	assert.Equal(t, int32(1), *cj.Spec.FailedJobsHistoryLimit)

	jobSpec := cj.Spec.JobTemplate.Spec
	require.NotNil(t, jobSpec.BackoffLimit)
	assert.Equal(t, int32(0), *jobSpec.BackoffLimit)

	require.Len(t, jobSpec.Template.Spec.Containers, 1)
	pull := jobSpec.Template.Spec.Containers[0]
	assert.Equal(t, image, pull.Image)
	assert.Equal(t, []string{"true"}, pull.Command)
	assert.Equal(t, apiv1.PullAlways, pull.ImagePullPolicy)
	assert.Equal(t, apiv1.RestartPolicyNever, jobSpec.Template.Spec.RestartPolicy)

	require.Len(t, jobSpec.Template.Spec.ImagePullSecrets, 1)
	assert.Equal(t, "vice-image-pull-secret", jobSpec.Template.Spec.ImagePullSecrets[0].Name)

	assert.Len(t, jobSpec.Template.Spec.Tolerations, 2)
}

func TestBuildCacheCronJobOmitsPullSecretWhenEmpty(t *testing.T) {
	mgr := NewCronJobImageCacheManager(nil, "vice-apps", "", testCronSchedule)
	cj := mgr.buildCacheCronJob("nginx:1.27", slugifyImage("nginx:1.27"))
	assert.Empty(t, cj.Spec.JobTemplate.Spec.Template.Spec.ImagePullSecrets)
}

func TestCronEnsureImageCached(t *testing.T) {
	const (
		ns     = "vice-apps"
		secret = "vice-image-pull-secret"
		image  = "harbor.cyverse.org/de/vice-proxy:latest"
	)
	slug := slugifyImage(image)
	cjName := cacheNamePrefix + slug

	tests := []struct {
		name     string
		existing *batchv1.CronJob // nil = no pre-existing CronJob
	}{
		{
			name:     "creates CronJob when missing",
			existing: nil,
		},
		{
			name:     "no-op when image matches",
			existing: NewCronJobImageCacheManager(nil, ns, secret, testCronSchedule).buildCacheCronJob(image, slug),
		},
		{
			name: "updates when annotation differs",
			existing: func() *batchv1.CronJob {
				cj := NewCronJobImageCacheManager(nil, ns, secret, testCronSchedule).buildCacheCronJob("old-image:v1", slug)
				cj.Name = cjName
				cj.Annotations[annotationImage] = "old-image:v1"
				return cj
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

			mgr := NewCronJobImageCacheManager(cs, ns, secret, testCronSchedule)
			err := mgr.EnsureImageCached(context.Background(), image)
			require.NoError(t, err)

			cj, err := cs.BatchV1().CronJobs(ns).Get(context.Background(), cjName, metav1.GetOptions{})
			require.NoError(t, err)
			assert.Equal(t, image, cj.Annotations[annotationImage])
		})
	}
}

func TestCronEnsureImageCachedInvalidRef(t *testing.T) {
	cs := fake.NewSimpleClientset()
	mgr := NewCronJobImageCacheManager(cs, "vice-apps", "vice-image-pull-secret", testCronSchedule)

	assert.Error(t, mgr.EnsureImageCached(context.Background(), ""))
	assert.Error(t, mgr.EnsureImageCached(context.Background(), "bad image!"))
}

func TestCronRefreshCachedImageCreatesJob(t *testing.T) {
	const (
		ns     = "vice-apps"
		secret = "vice-image-pull-secret"
		image  = "harbor.cyverse.org/de/vice-proxy:latest"
	)
	slug := slugifyImage(image)
	existing := NewCronJobImageCacheManager(nil, ns, secret, testCronSchedule).buildCacheCronJob(image, slug)
	// Fake clientset doesn't set a UID; give one so we can assert the
	// OwnerReference round-trips correctly.
	existing.UID = "fake-uid-123"

	cs := fake.NewSimpleClientset(existing)
	mgr := NewCronJobImageCacheManager(cs, ns, secret, testCronSchedule)

	err := mgr.RefreshCachedImage(context.Background(), image)
	require.NoError(t, err)

	jobs, err := cs.BatchV1().Jobs(ns).List(context.Background(), metav1.ListOptions{})
	require.NoError(t, err)
	require.Len(t, jobs.Items, 1)

	job := jobs.Items[0]
	assert.Equal(t, cacheNamePrefix+slug+"-r-", job.GenerateName)
	assert.Equal(t, image, job.Annotations[annotationImage])
	assert.Equal(t, slug, job.Labels[labelImageCacheID])
	require.NotNil(t, job.Spec.TTLSecondsAfterFinished)
	assert.Equal(t, int32(refreshJobTTLSeconds), *job.Spec.TTLSecondsAfterFinished)
	require.Len(t, job.OwnerReferences, 1)
	assert.Equal(t, "CronJob", job.OwnerReferences[0].Kind)
	assert.Equal(t, existing.Name, job.OwnerReferences[0].Name)
	assert.Equal(t, existing.UID, job.OwnerReferences[0].UID)
}

func TestCronRefreshCachedImageMissingSurfacesNotFound(t *testing.T) {
	cs := fake.NewSimpleClientset()
	mgr := NewCronJobImageCacheManager(cs, "vice-apps", "vice-image-pull-secret", testCronSchedule)

	err := mgr.RefreshCachedImage(context.Background(), "harbor.cyverse.org/de/vice-proxy:latest")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no cache CronJob found")
}

func TestCronRefreshCachedImageInvalidRef(t *testing.T) {
	cs := fake.NewSimpleClientset()
	mgr := NewCronJobImageCacheManager(cs, "vice-apps", "vice-image-pull-secret", testCronSchedule)
	assert.Error(t, mgr.RefreshCachedImage(context.Background(), "bad image!"))
}

func TestCronRemoveCachedImage(t *testing.T) {
	const (
		ns     = "vice-apps"
		secret = "vice-image-pull-secret"
		image  = "harbor.cyverse.org/de/vice-proxy:latest"
	)

	tests := []struct {
		name     string
		existing bool
	}{
		{name: "deletes existing CronJob", existing: true},
		{name: "succeeds silently when missing", existing: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cs *fake.Clientset
			mgr := NewCronJobImageCacheManager(nil, ns, secret, testCronSchedule)
			if tt.existing {
				cj := mgr.buildCacheCronJob(image, slugifyImage(image))
				cs = fake.NewSimpleClientset(cj)
			} else {
				cs = fake.NewSimpleClientset()
			}
			mgr = NewCronJobImageCacheManager(cs, ns, secret, testCronSchedule)

			require.NoError(t, mgr.RemoveCachedImage(context.Background(), image))

			list, err := cs.BatchV1().CronJobs(ns).List(context.Background(), metav1.ListOptions{})
			require.NoError(t, err)
			assert.Empty(t, list.Items)
		})
	}
}

func TestCronRemoveCachedImageByIDIdempotent(t *testing.T) {
	cs := fake.NewSimpleClientset()
	mgr := NewCronJobImageCacheManager(cs, "vice-apps", "vice-image-pull-secret", testCronSchedule)
	require.NoError(t, mgr.RemoveCachedImageByID(context.Background(), "does-not-exist"))
}

func TestCronListCachedImages(t *testing.T) {
	const (
		ns     = "vice-apps"
		secret = "vice-image-pull-secret"
	)
	mgr := NewCronJobImageCacheManager(nil, ns, secret, testCronSchedule)
	image1 := "harbor.cyverse.org/de/vice-proxy:latest"
	image2 := "harbor.cyverse.org/de/porklock:latest"
	cj1 := mgr.buildCacheCronJob(image1, slugifyImage(image1))
	cj2 := mgr.buildCacheCronJob(image2, slugifyImage(image2))

	// Mark cj1 as successfully run; leave cj2 as never-run.
	now := metav1.Now()
	earlier := metav1.NewTime(now.Add(-time.Minute))
	cj1.Status = batchv1.CronJobStatus{
		LastScheduleTime:   &earlier,
		LastSuccessfulTime: &now,
	}

	cs := fake.NewSimpleClientset(cj1, cj2)
	mgr = NewCronJobImageCacheManager(cs, ns, secret, testCronSchedule)

	images, err := mgr.ListCachedImages(context.Background())
	require.NoError(t, err)
	require.Len(t, images, 2)

	byImage := map[string]ImageCacheStatus{}
	for _, s := range images {
		byImage[s.Image] = s
	}

	s1 := byImage[image1]
	assert.Equal(t, "ready", s1.Status)
	assert.Equal(t, int32(1), s1.Ready)
	assert.Equal(t, int32(1), s1.Desired)

	s2 := byImage[image2]
	assert.Equal(t, "pulling", s2.Status)
	assert.Equal(t, int32(0), s2.Ready)
	assert.Equal(t, int32(1), s2.Desired)
}

func TestCronGetCachedImageStatus(t *testing.T) {
	const (
		ns     = "vice-apps"
		secret = "vice-image-pull-secret"
		image  = "harbor.cyverse.org/de/vice-proxy:latest"
	)
	mgr := NewCronJobImageCacheManager(nil, ns, secret, testCronSchedule)
	slug := slugifyImage(image)
	cj := mgr.buildCacheCronJob(image, slug)
	now := metav1.Now()
	cj.Status = batchv1.CronJobStatus{
		LastScheduleTime:   &now,
		LastSuccessfulTime: &now,
	}

	cs := fake.NewSimpleClientset(cj)
	mgr = NewCronJobImageCacheManager(cs, ns, secret, testCronSchedule)

	status, err := mgr.GetCachedImageStatus(context.Background(), slug)
	require.NoError(t, err)
	assert.Equal(t, image, status.Image)
	assert.Equal(t, "ready", status.Status)

	_, err = mgr.GetCachedImageStatus(context.Background(), "nonexistent")
	assert.Error(t, err)
}

func TestDeriveCronJobCacheStatus(t *testing.T) {
	now := metav1.Now()
	earlier := metav1.NewTime(now.Add(-time.Minute))
	later := metav1.NewTime(now.Add(time.Minute))

	tests := []struct {
		name        string
		cj          *batchv1.CronJob
		wantDesired int32
		wantReady   int32
		wantStatus  string
	}{
		{
			name: "suspended → error",
			cj: &batchv1.CronJob{
				Spec: batchv1.CronJobSpec{Suspend: trueP()},
			},
			wantDesired: 0, wantReady: 0, wantStatus: "error",
		},
		{
			name:        "never scheduled → pulling",
			cj:          &batchv1.CronJob{},
			wantDesired: 1, wantReady: 0, wantStatus: "pulling",
		},
		{
			name: "last run succeeded → ready",
			cj: &batchv1.CronJob{
				Status: batchv1.CronJobStatus{
					LastScheduleTime:   &earlier,
					LastSuccessfulTime: &now,
				},
			},
			wantDesired: 1, wantReady: 1, wantStatus: "ready",
		},
		{
			name: "success at same time as schedule → ready",
			cj: &batchv1.CronJob{
				Status: batchv1.CronJobStatus{
					LastScheduleTime:   &now,
					LastSuccessfulTime: &now,
				},
			},
			wantDesired: 1, wantReady: 1, wantStatus: "ready",
		},
		{
			name: "scheduled after last success → cached-with-errors",
			cj: &batchv1.CronJob{
				Status: batchv1.CronJobStatus{
					LastScheduleTime:   &later,
					LastSuccessfulTime: &earlier,
				},
			},
			wantDesired: 1, wantReady: 0, wantStatus: "cached-with-errors",
		},
		{
			name: "scheduled but never succeeded → cached-with-errors",
			cj: &batchv1.CronJob{
				Status: batchv1.CronJobStatus{
					LastScheduleTime: &earlier,
				},
			},
			wantDesired: 1, wantReady: 0, wantStatus: "cached-with-errors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, r, s := deriveCronJobCacheStatus(tt.cj)
			assert.Equal(t, tt.wantDesired, d)
			assert.Equal(t, tt.wantReady, r)
			assert.Equal(t, tt.wantStatus, s)
		})
	}
}

func TestRefreshJobGeneratePrefixTruncation(t *testing.T) {
	// Construct a slug that, with cacheNamePrefix and "-r-", exceeds
	// the K8s name limit minus 5. The truncated prefix must stay
	// within bounds so the random suffix appended by the API server
	// won't push the name over 253 chars.
	long := strings.Repeat("a", 300)
	prefix := refreshJobGeneratePrefix(long)
	assert.LessOrEqual(t, len(prefix), k8sMaxNameLen-5)
}

// trueP returns a pointer to true, used for Suspend field tests.
func trueP() *bool { v := true; return &v }
