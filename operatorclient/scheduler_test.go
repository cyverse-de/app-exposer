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

// mockOperatorServer creates a test HTTP server that simulates an operator.
// capacitySlots controls how many available slots are reported.
// rejectLaunch causes the launch endpoint to return 409.
func mockOperatorServer(capacitySlots int, rejectLaunch bool) *httptest.Server {
	var launchCount atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/capacity", func(w http.ResponseWriter, r *http.Request) {
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
