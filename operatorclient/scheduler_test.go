package operatorclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// mockOperatorServer creates a test HTTP server that simulates an operator.
// capacitySlots controls how many available slots are reported.
// rejectLaunch causes the launch endpoint to return 409.
func mockOperatorServer(capacitySlots int, rejectLaunch bool) *httptest.Server {
	return mockOperatorServerWithStatuses(capacitySlots, rejectLaunch, 0, 0)
}

// mockOperatorServerWithStatuses is the explicit variant that also lets a
// test inject non-2xx responses for the capacity and launch endpoints —
// used by cases that exercise error-classification paths in the scheduler.
// A capacityStatus or launchStatus of 0 means "use the default behavior"
// (respect the slots / rejectLaunch arguments).
func mockOperatorServerWithStatuses(capacitySlots int, rejectLaunch bool, capacityStatus, launchStatus int) *httptest.Server {
	var launchCount atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/capacity", func(w http.ResponseWriter, r *http.Request) {
		if capacityStatus != 0 {
			http.Error(w, "injected capacity failure", capacityStatus)
			return
		}
		resp := CapacityResponse{
			MaxAnalyses:     10,
			RunningAnalyses: 10 - capacitySlots,
			AvailableSlots:  capacitySlots,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	})
	mux.HandleFunc("/analyses", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		launchCount.Add(1)
		if launchStatus != 0 {
			http.Error(w, "injected launch failure", launchStatus)
			return
		}
		if rejectLaunch {
			http.Error(w, "at capacity", http.StatusConflict)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
	})

	return httptest.NewServer(mux)
}

func TestSchedulerLaunchAnalysis(t *testing.T) {
	tests := []struct {
		name      string
		operators []struct {
			slots  int
			reject bool
		}
		wantOperator string // expected operator name, empty if error
		wantErr      error
	}{
		{
			name: "first operator has capacity",
			operators: []struct {
				slots  int
				reject bool
			}{
				{slots: 5, reject: false},
				{slots: 5, reject: false},
			},
			wantOperator: "op-0",
		},
		{
			name: "first at capacity, second accepts",
			operators: []struct {
				slots  int
				reject bool
			}{
				{slots: 0, reject: false},
				{slots: 5, reject: false},
			},
			wantOperator: "op-1",
		},
		{
			name: "first returns 409, second accepts",
			operators: []struct {
				slots  int
				reject bool
			}{
				{slots: 5, reject: true}, // Has capacity but race condition
				{slots: 5, reject: false},
			},
			wantOperator: "op-1",
		},
		{
			name: "all operators exhausted",
			operators: []struct {
				slots  int
				reject bool
			}{
				{slots: 0, reject: false},
				{slots: 0, reject: false},
			},
			wantErr: ErrAllOperatorsExhausted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var configs []OperatorConfig
			var servers []*httptest.Server

			for i, op := range tt.operators {
				srv := mockOperatorServer(op.slots, op.reject)
				servers = append(servers, srv)
				configs = append(configs, OperatorConfig{
					Name: fmt.Sprintf("op-%d", i),
					URL:  srv.URL,
				})
			}
			defer func() {
				for _, srv := range servers {
					srv.Close()
				}
			}()

			scheduler, err := NewScheduler(configs, nil)
			require.NoError(t, err)

			bundle := &AnalysisBundle{AnalysisID: "test-123"}
			operatorName, err := scheduler.LaunchAnalysis(context.Background(), bundle)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantOperator, operatorName)
			}
		})
	}
}

// TestSchedulerLaunchAllCapacityChecksFail exercises the "every operator
// errored on capacity check" branch — as distinct from "every operator is
// at capacity," which the table above already covers. LaunchAnalysis must
// surface the capacity-failure case rather than falling back to the plain
// ErrAllOperatorsExhausted shape.
func TestSchedulerLaunchAllCapacityChecksFail(t *testing.T) {
	srv0 := mockOperatorServerWithStatuses(0, false, http.StatusInternalServerError, 0)
	defer srv0.Close()
	srv1 := mockOperatorServerWithStatuses(0, false, http.StatusInternalServerError, 0)
	defer srv1.Close()

	scheduler, err := NewScheduler([]OperatorConfig{
		{Name: "op-0", URL: srv0.URL},
		{Name: "op-1", URL: srv1.URL},
	}, nil)
	require.NoError(t, err)

	_, err = scheduler.LaunchAnalysis(context.Background(), &AnalysisBundle{AnalysisID: "test"})
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrAllOperatorsExhausted)
	assert.Contains(t, err.Error(), "failed capacity check", "message should distinguish capacity-failure from at-capacity")
}

// TestSchedulerLaunchTransientErrorFalthrough covers the case added along
// with isTransientLaunchError: the first operator accepts capacity but then
// returns a 5xx on Launch. The scheduler should fall through to the next
// operator rather than aborting the whole launch.
func TestSchedulerLaunchTransientErrorFalthrough(t *testing.T) {
	srv0 := mockOperatorServerWithStatuses(5, false, 0, http.StatusBadGateway)
	defer srv0.Close()
	srv1 := mockOperatorServer(5, false)
	defer srv1.Close()

	scheduler, err := NewScheduler([]OperatorConfig{
		{Name: "op-0", URL: srv0.URL},
		{Name: "op-1", URL: srv1.URL},
	}, nil)
	require.NoError(t, err)

	name, err := scheduler.LaunchAnalysis(context.Background(), &AnalysisBundle{AnalysisID: "test"})
	require.NoError(t, err)
	assert.Equal(t, "op-1", name, "scheduler must fall through on transient 5xx launch errors")
}

// TestSchedulerLaunchNonTransientAborts guards the other side of the
// classification: a 400 from Launch is a request we built wrong, so it must
// abort the scheduling loop rather than walk every operator producing the
// same failure.
func TestSchedulerLaunchNonTransientAborts(t *testing.T) {
	srv0 := mockOperatorServerWithStatuses(5, false, 0, http.StatusBadRequest)
	defer srv0.Close()
	srv1 := mockOperatorServer(5, false) // would succeed if we reached it
	defer srv1.Close()

	scheduler, err := NewScheduler([]OperatorConfig{
		{Name: "op-0", URL: srv0.URL},
		{Name: "op-1", URL: srv1.URL},
	}, nil)
	require.NoError(t, err)

	_, err = scheduler.LaunchAnalysis(context.Background(), &AnalysisBundle{AnalysisID: "test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "op-0", "error must name the operator that failed")
	assert.Contains(t, err.Error(), "400", "error must surface the HTTP status")
}

// TestSchedulerLaunchAllTransient covers the case where every operator is
// healthy-looking on capacity but fails on Launch with a transient 5xx. The
// scheduler should surface the last underlying error so the caller can
// tell the difference from the plain "all at capacity" shape.
func TestSchedulerLaunchAllTransient(t *testing.T) {
	srv0 := mockOperatorServerWithStatuses(5, false, 0, http.StatusServiceUnavailable)
	defer srv0.Close()
	srv1 := mockOperatorServerWithStatuses(5, false, 0, http.StatusBadGateway)
	defer srv1.Close()

	scheduler, err := NewScheduler([]OperatorConfig{
		{Name: "op-0", URL: srv0.URL},
		{Name: "op-1", URL: srv1.URL},
	}, nil)
	require.NoError(t, err)

	_, err = scheduler.LaunchAnalysis(context.Background(), &AnalysisBundle{AnalysisID: "test"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unhealthy", "message should distinguish from at-capacity")

	var statusErr *HTTPStatusError
	require.ErrorAs(t, err, &statusErr, "last transient error must be preserved in the chain")
	assert.True(t, statusErr.Transient(), "preserved error should be a transient status error")
}

func TestSchedulerNoOperators(t *testing.T) {
	scheduler, err := NewScheduler(nil, nil)
	assert.NoError(t, err)

	_, err = scheduler.LaunchAnalysis(context.Background(), &AnalysisBundle{AnalysisID: "test"})
	assert.ErrorIs(t, err, ErrNoOperators)

	scheduler, err = NewScheduler([]OperatorConfig{}, nil)
	assert.NoError(t, err)

	_, err = scheduler.LaunchAnalysis(context.Background(), &AnalysisBundle{AnalysisID: "test"})
	assert.ErrorIs(t, err, ErrNoOperators)
}

func TestSchedulerClientByName(t *testing.T) {
	srv := mockOperatorServer(5, false)
	defer srv.Close()

	scheduler, err := NewScheduler([]OperatorConfig{
		{Name: "test-op", URL: srv.URL},
	}, nil)
	require.NoError(t, err)

	client := scheduler.ClientByName("test-op")
	assert.NotNil(t, client)
	assert.Equal(t, "test-op", client.Name())

	client = scheduler.ClientByName("nonexistent")
	assert.Nil(t, client)
}

// TestSchedulerLaunchUnlimitedCapacity verifies that an operator reporting
// AvailableSlots=-1 (unlimited capacity) accepts launches normally.
func TestSchedulerLaunchUnlimitedCapacity(t *testing.T) {
	// Build a mock server that always reports unlimited capacity
	// (AvailableSlots=-1, MaxAnalyses=0, RunningAnalyses=5).
	mux := http.NewServeMux()
	mux.HandleFunc("/capacity", func(w http.ResponseWriter, r *http.Request) {
		resp := CapacityResponse{
			MaxAnalyses:     0,
			RunningAnalyses: 5,
			AvailableSlots:  -1,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp) //nolint:errcheck
	})
	mux.HandleFunc("/analyses", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"}) //nolint:errcheck
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	scheduler, err := NewScheduler([]OperatorConfig{
		{Name: "unlimited-op", URL: srv.URL},
	}, nil)
	require.NoError(t, err)

	bundle := &AnalysisBundle{AnalysisID: "unlimited-test-456"}
	operatorName, err := scheduler.LaunchAnalysis(context.Background(), bundle)

	require.NoError(t, err)
	assert.Equal(t, "unlimited-op", operatorName)
}

// TestSchedulerSyncAndSetTokenSource verifies that Sync replaces the operator
// list and SetTokenSource updates the token source. It also exercises the
// RW-mutex by running Sync and SetTokenSource concurrently to detect races
// under `go test -race`.
func TestSchedulerSyncAndSetTokenSource(t *testing.T) {
	// --- initial state: one operator ---
	srv0 := mockOperatorServer(5, false)
	defer srv0.Close()

	scheduler, err := NewScheduler([]OperatorConfig{
		{Name: "original-op", URL: srv0.URL},
	}, nil)
	require.NoError(t, err)

	// The original operator must be findable.
	require.NotNil(t, scheduler.ClientByName("original-op"), "original-op should exist before Sync")

	// --- Sync with two new operators ---
	srv1 := mockOperatorServer(3, false)
	defer srv1.Close()
	srv2 := mockOperatorServer(3, false)
	defer srv2.Close()

	newConfigs := []OperatorConfig{
		{Name: "new-op-0", URL: srv1.URL},
		{Name: "new-op-1", URL: srv2.URL},
	}
	require.NoError(t, scheduler.Sync(newConfigs))

	// The old operator must be gone; new ones must be present.
	assert.Nil(t, scheduler.ClientByName("original-op"), "original-op should be absent after Sync")
	assert.NotNil(t, scheduler.ClientByName("new-op-0"), "new-op-0 should exist after Sync")
	assert.NotNil(t, scheduler.ClientByName("new-op-1"), "new-op-1 should exist after Sync")

	// --- race-condition stress test ---
	// Run Sync and SetTokenSource concurrently to exercise the RW-mutex under
	// `-race`. Each goroutine performs 100 iterations.
	var wg sync.WaitGroup
	const iterations = 100

	wg.Add(1)
	go func() {
		defer wg.Done()
		for range iterations {
			// Alternate between the two operator lists so that Sync is meaningful.
			if err := scheduler.Sync(newConfigs); err != nil {
				t.Errorf("Sync failed during race: %v", err)
				return
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		// staticTokenSource is a minimal oauth2.TokenSource for testing.
		ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-token"})
		for range iterations {
			scheduler.SetTokenSource(ts)
		}
	}()

	wg.Wait()

	// After concurrent churn the scheduler must still be operational.
	assert.NotNil(t, scheduler.Clients(), "scheduler should still have clients after concurrent Sync/SetTokenSource")
}
