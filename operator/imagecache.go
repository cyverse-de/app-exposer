package operator

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
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

	// pauseImage is the minimal container that runs as the main container
	// in cache DaemonSets. Cron mode uses a Job and does not need it.
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

// ImageCacheManager is the interface implemented by both backends: the
// DaemonSet-per-image cache (node-local containerd warming) and the
// CronJob-per-image cache (periodic pulls routed through an upstream
// pull-through registry such as AWS ECR in EKS Auto Mode).
type ImageCacheManager interface {
	EnsureImageCached(ctx context.Context, image string) error
	RefreshCachedImage(ctx context.Context, image string) error
	RemoveCachedImage(ctx context.Context, image string) error
	RemoveCachedImageByID(ctx context.Context, id string) error
	ListCachedImages(ctx context.Context) ([]ImageCacheStatus, error)
	GetCachedImageStatus(ctx context.Context, id string) (*ImageCacheStatus, error)
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

	// Truncate so cacheNamePrefix + slug + "-" + hash fits in 253 chars.
	maxSlugLen := 253 - len(cacheNamePrefix) - 1 - slugHashLen
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

// deriveCacheStatus computes the cache status string from "ready" and
// "desired" counts. Both backends populate these fields; in cron mode they
// take values 0 or 1 derived from CronJob success state. Evaluated in order:
// error, ready, cached-with-errors, pulling.
//
// "cached-with-errors" is returned when ready==0 and desired>0. For
// DaemonSets this covers distroless images where the init container
// CrashLoopBackOffs (image still cached) and newly created DaemonSets where
// pods haven't started yet. For CronJobs it covers a failed last run.
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
