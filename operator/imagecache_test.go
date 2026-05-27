package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
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

			// Total cache resource name must fit K8s 253-char limit.
			fullName := cacheNamePrefix + slug
			assert.LessOrEqual(t, len(fullName), 253, "full cache resource name must fit K8s limit")
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
		{name: "valid ECR pull-through ref", image: "123456789012.dkr.ecr.us-east-1.amazonaws.com/cache/harbor.cyverse.org/de/vice-proxy:latest", wantErr: false},
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
//   - all images refreshed → 200
//   - empty list → 400
//   - one image exists, one doesn't → 207 with the missing one's error in its result row
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
