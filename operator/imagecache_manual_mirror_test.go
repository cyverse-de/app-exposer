package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManualMirrorRewriteImage(t *testing.T) {
	mappings := map[string]string{
		"harbor.cyverse.org/de/vice-proxy:latest": "123456789012.dkr.ecr.us-east-1.amazonaws.com/de/vice-proxy:latest",
		"harbor.cyverse.org/de/porklock:qa":       "123456789012.dkr.ecr.us-east-1.amazonaws.com/de/porklock:qa",
	}
	mgr := NewManualMirrorImageCacheManager(mappings)

	tests := []struct {
		name    string
		in      string
		want    string
		wantHit bool
	}{
		{name: "hit", in: "harbor.cyverse.org/de/vice-proxy:latest", want: "123456789012.dkr.ecr.us-east-1.amazonaws.com/de/vice-proxy:latest", wantHit: true},
		{name: "different hit", in: "harbor.cyverse.org/de/porklock:qa", want: "123456789012.dkr.ecr.us-east-1.amazonaws.com/de/porklock:qa", wantHit: true},
		{name: "miss leaves ref untouched", in: "harbor.cyverse.org/de/never-mirrored:latest", want: "harbor.cyverse.org/de/never-mirrored:latest", wantHit: false},
		{name: "tag-mismatch is a miss", in: "harbor.cyverse.org/de/vice-proxy:qa", want: "harbor.cyverse.org/de/vice-proxy:qa", wantHit: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := mgr.RewriteImage(tt.in)
			assert.Equal(t, tt.want, got)
			assert.Equal(t, tt.wantHit, ok)
		})
	}
}

func TestManualMirrorReadOnlyAndMutatingMethodsReturnSentinel(t *testing.T) {
	mgr := NewManualMirrorImageCacheManager(map[string]string{"a:1": "b:1"})
	assert.True(t, mgr.ReadOnly())

	ctx := context.Background()
	for _, op := range []struct {
		name string
		fn   func() error
	}{
		{"EnsureImageCached", func() error { return mgr.EnsureImageCached(ctx, "a:1") }},
		{"RefreshCachedImage", func() error { return mgr.RefreshCachedImage(ctx, "a:1") }},
		{"RemoveCachedImage", func() error { return mgr.RemoveCachedImage(ctx, "a:1") }},
		{"RemoveCachedImageByID", func() error { return mgr.RemoveCachedImageByID(ctx, slugifyImage("a:1")) }},
	} {
		t.Run(op.name, func(t *testing.T) {
			err := op.fn()
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrCacheReadOnly)
		})
	}
}

func TestManualMirrorListCachedImages(t *testing.T) {
	mappings := map[string]string{
		"harbor.cyverse.org/de/vice-proxy:latest": "123456789012.dkr.ecr.us-east-1.amazonaws.com/de/vice-proxy:latest",
		"harbor.cyverse.org/de/porklock:latest":   "123456789012.dkr.ecr.us-east-1.amazonaws.com/de/porklock:latest",
	}
	mgr := NewManualMirrorImageCacheManager(mappings)

	got, err := mgr.ListCachedImages(context.Background())
	require.NoError(t, err)
	require.Len(t, got, 2)

	byImage := map[string]ImageCacheStatus{}
	for _, s := range got {
		byImage[s.Image] = s
	}
	for upstream := range mappings {
		s, ok := byImage[upstream]
		require.True(t, ok, "list response missing %s", upstream)
		assert.Equal(t, slugifyImage(upstream), s.ID)
		assert.Equal(t, int32(1), s.Ready)
		assert.Equal(t, int32(1), s.Desired)
		assert.Equal(t, "ready", s.Status)
	}
}

func TestManualMirrorGetCachedImageStatus(t *testing.T) {
	upstream := "harbor.cyverse.org/de/vice-proxy:latest"
	mgr := NewManualMirrorImageCacheManager(map[string]string{
		upstream: "123456789012.dkr.ecr.us-east-1.amazonaws.com/de/vice-proxy:latest",
	})

	t.Run("hit returns status with correct upstream image and slug", func(t *testing.T) {
		s, err := mgr.GetCachedImageStatus(context.Background(), slugifyImage(upstream))
		require.NoError(t, err)
		assert.Equal(t, upstream, s.Image)
		assert.Equal(t, "ready", s.Status)
	})

	t.Run("miss returns sentinel that handler maps to 404", func(t *testing.T) {
		_, err := mgr.GetCachedImageStatus(context.Background(), "no-such-slug-0123456789ab")
		require.Error(t, err)
		assert.ErrorIs(t, err, errImageCacheEntryNotFound)
	})
}

func TestLoadImageMirrorMap(t *testing.T) {
	tmp := t.TempDir()

	writeFile := func(t *testing.T, name, body string) string {
		t.Helper()
		p := filepath.Join(tmp, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		return p
	}

	validMap := map[string]string{
		"harbor.cyverse.org/de/vice-proxy:latest": "123456789012.dkr.ecr.us-east-1.amazonaws.com/de/vice-proxy:latest",
		"harbor.cyverse.org/de/porklock:qa":       "123456789012.dkr.ecr.us-east-1.amazonaws.com/de/porklock:qa",
	}
	validBytes, err := json.Marshal(validMap)
	require.NoError(t, err)
	validPath := writeFile(t, "valid.json", string(validBytes))

	tests := []struct {
		name        string
		path        string
		wantErr     bool
		wantErrPart string
	}{
		{name: "valid file parses and validates", path: validPath, wantErr: false},
		{name: "missing file is a clear error", path: filepath.Join(tmp, "does-not-exist.json"), wantErr: true, wantErrPart: "reading repos file"},
		{name: "malformed JSON surfaces parse error", path: writeFile(t, "malformed.json", "{not valid"), wantErr: true, wantErrPart: "parsing repos file"},
		{name: "empty object is rejected", path: writeFile(t, "empty.json", "{}"), wantErr: true, wantErrPart: "is empty"},
		{
			name: "entry-count cap is enforced",
			path: func() string {
				m := make(map[string]string, maxRepoFileEntries+1)
				for i := 0; i <= maxRepoFileEntries; i++ {
					// validateImageRef requires alnum-start refs.
					k := fmt.Sprintf("upstream%d:tag", i)
					v := fmt.Sprintf("mirror%d:tag", i)
					m[k] = v
				}
				b, err := json.Marshal(m)
				require.NoError(t, err)
				return writeFile(t, "too-many.json", string(b))
			}(),
			wantErr:     true,
			wantErrPart: "maximum is",
		},
		{name: "empty key is rejected", path: writeFile(t, "empty-key.json", `{"":"x:1"}`), wantErr: true, wantErrPart: "invalid upstream key"},
		{name: "key with spaces is rejected", path: writeFile(t, "bad-key.json", `{"bad image":"x:1"}`), wantErr: true, wantErrPart: "invalid upstream key"},
		{name: "value with spaces is rejected", path: writeFile(t, "bad-value.json", `{"harbor.cyverse.org/x:1":"bad ref"}`), wantErr: true, wantErrPart: "invalid mirrored value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LoadImageMirrorMap(tt.path)
			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrPart != "" {
					assert.Contains(t, err.Error(), tt.wantErrPart)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, validMap, got)
		})
	}
}

func TestManualMirrorSatisfiesInterfaces(_ *testing.T) {
	// Compile-time check that ManualMirrorImageCacheManager implements both
	// interfaces; runtime body is intentionally empty.
	var (
		_ ImageCacheManager = (*ManualMirrorImageCacheManager)(nil)
		_ ImageRewriter     = (*ManualMirrorImageCacheManager)(nil)
	)
}
