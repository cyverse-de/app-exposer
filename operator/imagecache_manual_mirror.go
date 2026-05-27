package operator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// ManualMirrorImageCacheManager satisfies both ImageCacheManager and
// ImageRewriter. The cache surface is read-only — the mapping is owned by
// an external mirror process (e.g. mirror-images-to-ecr.sh), not by the
// operator — so mutating cache methods return ErrCacheReadOnly. At launch
// time RewriteImage substitutes upstream image refs for their mirrored
// counterparts using the same mapping.
type ManualMirrorImageCacheManager struct {
	// mappings is keyed by the upstream image ref (the string that appears
	// in incoming analysis bundles) and valued by the fully-qualified
	// mirrored ref to substitute in its place.
	mappings map[string]string

	// idIndex maps slugifyImage(key) -> key so GetCachedImageStatus can
	// resolve a slug ID without re-iterating the mapping.
	idIndex map[string]string
}

// NewManualMirrorImageCacheManager constructs a manager around an
// already-loaded mapping. Callers should use LoadImageMirrorMap to obtain
// and validate the map from a JSON file.
func NewManualMirrorImageCacheManager(mappings map[string]string) *ManualMirrorImageCacheManager {
	idIndex := make(map[string]string, len(mappings))
	for k := range mappings {
		idIndex[slugifyImage(k)] = k
	}
	return &ManualMirrorImageCacheManager{
		mappings: mappings,
		idIndex:  idIndex,
	}
}

// ReadOnly is true: the operator can't update the mapping at runtime; it
// has to be edited in the JSON file and the operator restarted.
func (*ManualMirrorImageCacheManager) ReadOnly() bool { return true }

// RewriteImage looks up image in the mapping. If found, returns the
// mirrored ref and ok=true; otherwise returns image unchanged and ok=false.
func (m *ManualMirrorImageCacheManager) RewriteImage(image string) (string, bool) {
	target, ok := m.mappings[image]
	if !ok {
		return image, false
	}
	return target, true
}

// EnsureImageCached, RefreshCachedImage, RemoveCachedImage, and
// RemoveCachedImageByID all return ErrCacheReadOnly. Handlers normally
// detect ReadOnly() before reaching these, but having the methods return
// a typed error keeps direct callers honest.
func (*ManualMirrorImageCacheManager) EnsureImageCached(context.Context, string) error {
	return ErrCacheReadOnly
}

func (*ManualMirrorImageCacheManager) RefreshCachedImage(context.Context, string) error {
	return ErrCacheReadOnly
}

func (*ManualMirrorImageCacheManager) RemoveCachedImage(context.Context, string) error {
	return ErrCacheReadOnly
}

func (*ManualMirrorImageCacheManager) RemoveCachedImageByID(context.Context, string) error {
	return ErrCacheReadOnly
}

// ListCachedImages exposes the mapping read-only as ImageCacheStatus
// entries. Each entry's Image is the upstream key (what bundles reference)
// and ID is its slug; Ready=Desired=1 with status="ready" since the mirror
// is treated as authoritative.
func (m *ManualMirrorImageCacheManager) ListCachedImages(context.Context) ([]ImageCacheStatus, error) {
	out := make([]ImageCacheStatus, 0, len(m.mappings))
	for upstream := range m.mappings {
		out = append(out, ImageCacheStatus{
			Image:   upstream,
			ID:      slugifyImage(upstream),
			Ready:   1,
			Desired: 1,
			Status:  "ready",
		})
	}
	return out, nil
}

// GetCachedImageStatus resolves a slug ID back to its mapping entry.
// Returns a wrapped not-found error if the slug doesn't correspond to any
// entry; the handler converts that into a 404 the same way it does for
// the K8s-backed backends.
func (m *ManualMirrorImageCacheManager) GetCachedImageStatus(_ context.Context, id string) (*ImageCacheStatus, error) {
	upstream, ok := m.idIndex[id]
	if !ok {
		return nil, fmt.Errorf("no manual-mirror entry for id %q: %w", id, errImageCacheEntryNotFound)
	}
	return &ImageCacheStatus{
		Image:   upstream,
		ID:      id,
		Ready:   1,
		Desired: 1,
		Status:  "ready",
	}, nil
}

// errImageCacheEntryNotFound is the manual-mirror-side sentinel for a
// missing slug lookup. The Get handler in imagecache_handlers.go uses
// errors.As against a K8s StatusError to detect 404s from the daemonset
// and cron backends; for manual-mirror we map this sentinel to the same
// 404 outcome — see HandleGetCachedImage.
var errImageCacheEntryNotFound = errors.New("manual-mirror entry not found")

// LoadImageMirrorMap reads and validates a JSON file mapping upstream
// image refs to mirrored refs. Both keys and values must pass
// validateImageRef; the map must be non-empty.
func LoadImageMirrorMap(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading repos file %s: %w", path, err)
	}

	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parsing repos file %s as JSON object of upstream->mirrored: %w", path, err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("repos file %s is empty", path)
	}

	for k, v := range raw {
		if err := validateImageRef(k); err != nil {
			return nil, fmt.Errorf("repos file %s: invalid upstream key %q: %w", path, k, err)
		}
		if err := validateImageRef(v); err != nil {
			return nil, fmt.Errorf("repos file %s: invalid mirrored value %q for key %q: %w", path, v, k, err)
		}
	}

	return raw, nil
}
