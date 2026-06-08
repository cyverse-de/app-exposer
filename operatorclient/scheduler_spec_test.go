package operatorclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// specRoutingServer records which launch endpoint was hit and advertises a
// configurable SpecVersion, so routing decisions can be asserted.
type specRoutingServer struct {
	server     *httptest.Server
	specHits   atomic.Int32
	bundleHits atomic.Int32
}

func newSpecRoutingServer(slots, specVersion int) *specRoutingServer {
	s := &specRoutingServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/capacity", func(w http.ResponseWriter, _ *http.Request) {
		resp := CapacityResponse{
			MaxAnalyses:     10,
			RunningAnalyses: 10 - slots,
			AvailableSlots:  slots,
			SpecVersion:     specVersion,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/analyses/spec", func(w http.ResponseWriter, _ *http.Request) {
		s.specHits.Add(1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/analyses", func(w http.ResponseWriter, _ *http.Request) {
		s.bundleHits.Add(1)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	s.server = httptest.NewServer(mux)
	return s
}

// TestSchedulerLaunchAnalysisSpecRouting verifies that LaunchAnalysisSpec sends
// a spec to a spec-aware operator and falls back to the legacy bundle for a
// spec-unaware one (SpecVersion 0), building the bundle only when needed.
func TestSchedulerLaunchAnalysisSpecRouting(t *testing.T) {
	tests := []struct {
		name            string
		specVersion     int
		wantSpecHit     bool
		wantBundleBuilt bool
	}{
		{name: "spec-aware operator gets the spec", specVersion: CurrentVICESpecVersion, wantSpecHit: true, wantBundleBuilt: false},
		{name: "spec-unaware operator gets the bundle", specVersion: 0, wantSpecHit: false, wantBundleBuilt: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newSpecRoutingServer(5, tt.specVersion)
			defer srv.server.Close()

			scheduler := schedulerWithConfigs(t, []OperatorConfig{{Name: "op-0", URL: srv.server.URL}})

			spec := &VICESpec{SpecVersion: CurrentVICESpecVersion, AnalysisID: "a-1"}
			var bundleBuilt bool
			legacyBundle := func() (*AnalysisBundle, error) {
				bundleBuilt = true
				return &AnalysisBundle{AnalysisID: "a-1"}, nil
			}

			_, name, err := scheduler.LaunchAnalysisSpec(context.Background(), spec, legacyBundle)
			require.NoError(t, err)
			assert.Equal(t, "op-0", name)

			if tt.wantSpecHit {
				assert.Equal(t, int32(1), srv.specHits.Load(), "spec endpoint should be hit")
				assert.Equal(t, int32(0), srv.bundleHits.Load(), "bundle endpoint should not be hit")
			} else {
				assert.Equal(t, int32(1), srv.bundleHits.Load(), "bundle endpoint should be hit")
				assert.Equal(t, int32(0), srv.specHits.Load(), "spec endpoint should not be hit")
			}
			assert.Equal(t, tt.wantBundleBuilt, bundleBuilt, "legacy bundle should be built only for spec-unaware operators")
		})
	}
}

// TestSchedulerLaunchAnalysisSpecMixedFleet verifies that, in a fleet mixing
// spec-aware and spec-unaware operators, a spec-aware operator appearing first
// in priority order is preferred and receives the spec — without the legacy
// bundle ever being built.
func TestSchedulerLaunchAnalysisSpecMixedFleet(t *testing.T) {
	specAware := newSpecRoutingServer(5, CurrentVICESpecVersion)
	legacy := newSpecRoutingServer(5, 0)
	defer specAware.server.Close()
	defer legacy.server.Close()

	// Priority order is list order; spec-aware first.
	scheduler := schedulerWithConfigs(t, []OperatorConfig{
		{Name: "spec-aware", URL: specAware.server.URL},
		{Name: "legacy", URL: legacy.server.URL},
	})

	spec := &VICESpec{SpecVersion: CurrentVICESpecVersion, AnalysisID: "a-1"}
	_, name, err := scheduler.LaunchAnalysisSpec(context.Background(), spec, func() (*AnalysisBundle, error) {
		return nil, fmt.Errorf("should not build bundle when spec-aware operator is available")
	})
	require.NoError(t, err)
	assert.Equal(t, "spec-aware", name)
	assert.Equal(t, int32(1), specAware.specHits.Load())
	assert.Equal(t, int32(0), legacy.bundleHits.Load())
}
