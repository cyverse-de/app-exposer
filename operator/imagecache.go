package operator

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"

	apiv1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
)

const (
	// Labels and annotations used to identify image cache resources
	// (DaemonSets or CronJobs).
	labelManagedBy    = "managed-by"
	labelPurpose      = "purpose"
	labelImageCacheID = "image-cache-id"
	annotationImage   = "de.cyverse.org/cached-image"

	valueManagedBy = "vice-operator"
	valuePurpose   = "image-cache"

	// cacheNamePrefix is prepended to every cache resource name (DaemonSet
	// or CronJob), keeping the slug ID the same across modes.
	cacheNamePrefix = "image-cache-"

	// slugHashLen is the number of hex characters from the SHA-256 hash
	// appended to slugs for uniqueness (12 hex chars = 48 bits).
	slugHashLen = 12

	// k8sMaxNameLen is the maximum length of a Kubernetes object name.
	k8sMaxNameLen = 253

	// pauseImage is the minimal container that runs as the main container
	// in cache DaemonSets. Cron mode uses a Job and does not need it.
	pauseImage = "registry.k8s.io/pause:3.10"
)

// imageRefPattern accepts well-formed image references and rejects strings
// containing whitespace or shell-unsafe characters.
var imageRefPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]*$`)

// imageCacheIDPattern matches the slug IDs produced by slugifyImage so
// the path-parameter handlers can reject obvious garbage before forwarding
// it to the K8s API server. Bound is k8sMaxNameLen minus cacheNamePrefix.
var imageCacheIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,240}$`)

// maxImageRefLen caps the image reference length so a caller can't park a
// huge string in CronJob/DaemonSet annotations (each annotation value can
// be up to 256KiB; a real OCI ref tops out near 700 chars).
const maxImageRefLen = 1024

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

// ImageCacheManager is the interface implemented by the cache backends:
// the DaemonSet-per-image cache (node-local containerd warming), the
// CronJob-per-image cache (periodic pulls routed through an upstream
// pull-through registry such as AWS ECR), and the manual-mirror manager
// (read-only window onto a static mapping file maintained externally).
type ImageCacheManager interface {
	EnsureImageCached(ctx context.Context, image string) error
	RefreshCachedImage(ctx context.Context, image string) error
	RemoveCachedImage(ctx context.Context, image string) error
	RemoveCachedImageByID(ctx context.Context, id string) error
	ListCachedImages(ctx context.Context) ([]ImageCacheStatus, error)
	GetCachedImageStatus(ctx context.Context, id string) (*ImageCacheStatus, error)
	// ReadOnly reports whether the backing cache is externally managed,
	// in which case mutating HTTP endpoints short-circuit to 400 rather
	// than calling into the manager.
	ReadOnly() bool
}

// ImageRewriter rewrites image references at launch time. Orthogonal to
// ImageCacheManager: daemonset/cron modes contribute no rewriter (the
// operator launches whatever images the bundle carries); manual-mirror
// contributes one that swaps upstream refs for their mirrored counterparts
// using a static JSON map.
type ImageRewriter interface {
	// RewriteImage returns the substitute ref when image has a mapping and
	// ok=true; otherwise returns image unchanged and ok=false.
	RewriteImage(image string) (rewritten string, ok bool)
}

// ErrCacheReadOnly is returned by manual-mirror's mutating cache methods.
// Handlers detect ReadOnly() at the request level and don't normally call
// into the manager when it's set, but this is the canonical error for any
// path that does reach the methods.
var ErrCacheReadOnly = errors.New("image cache is externally managed in this mode")

// imagePullSecretsFor returns a pull-secret list for the given secret name, or
// nil when the name is empty (omits the field from the pod spec for public images).
func imagePullSecretsFor(name string) []apiv1.LocalObjectReference {
	if name == "" {
		return nil
	}
	return []apiv1.LocalObjectReference{{Name: name}}
}

// cachePodTolerations returns the standard tolerations applied to every cache
// pod (DaemonSet init-container or CronJob pod). Tolerates the analysis taint
// so pods land on analysis nodes, and the GPU taint so image pulls still reach
// GPU-only nodes.
func cachePodTolerations() []apiv1.Toleration {
	return []apiv1.Toleration{
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
	}
}

// cacheResourceLabels returns the standard label set for a cache resource
// identified by slug.
func cacheResourceLabels(slug string) map[string]string {
	return map[string]string{
		labelManagedBy:    valueManagedBy,
		labelPurpose:      valuePurpose,
		labelImageCacheID: slug,
	}
}

// cacheManagedBySelector returns a label selector that matches all resources
// managed by this operator's image cache.
func cacheManagedBySelector() labels.Selector {
	return labels.SelectorFromSet(labels.Set{
		labelManagedBy: valueManagedBy,
		labelPurpose:   valuePurpose,
	})
}

// validateImageRef checks that an image reference is minimally valid.
func validateImageRef(image string) error {
	if image == "" {
		return errors.New("image reference must not be empty")
	}
	if len(image) > maxImageRefLen {
		return fmt.Errorf("image reference too long: %d chars (max %d)", len(image), maxImageRefLen)
	}
	if !imageRefPattern.MatchString(image) {
		return fmt.Errorf("invalid image reference: %q", image)
	}
	return nil
}

// validateImageCacheID rejects slug IDs that don't match the format
// slugifyImage produces, so handlers can return 404 immediately instead of
// forwarding malformed input to the K8s API.
func validateImageCacheID(id string) error {
	if id == "" {
		return errors.New("image cache id must not be empty")
	}
	if !imageCacheIDPattern.MatchString(id) {
		return fmt.Errorf("invalid image cache id: %q", id)
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

	// Truncate so cacheNamePrefix + slug + "-" + hash fits within k8sMaxNameLen.
	maxSlugLen := k8sMaxNameLen - len(cacheNamePrefix) - 1 - slugHashLen
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

// deriveCacheStatus maps (desired, ready) counts to a status string shared by
// both backends. In cron mode desired and ready are 0 or 1 derived from
// CronJob success state. "cached-with-errors" covers distroless images
// (CrashLoopBackOff after pull) and failed CronJob runs — the image is
// present even though the container exited non-zero.
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
