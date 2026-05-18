package operator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/messaging/v12"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStatusPublisher(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantError bool
	}{
		{"valid https URL", "https://de.example.org/job", false},
		{"valid http URL", "http://localhost:8080", false},
		{"valid URL with path", "https://de.example.org/some/path", false},
		{"empty URL", "", true},
		{"missing scheme", "de.example.org/job", true},
		{"malformed URL", "://not-a-url", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewStatusPublisher(tt.url, "test-host")
			if tt.wantError {
				assert.Error(t, err)
				assert.Nil(t, p)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, p)
			}
		})
	}
}

func TestStatusPublisherPublish(t *testing.T) {
	const (
		externalID = constants.ExternalID("ext-abc-123")
		hostname   = "vice-operator-aws"
	)

	tests := []struct {
		name         string
		state        messaging.JobState
		message      string
		basePath     string // appended to the test server URL
		serverStatus int
		serverBody   string
		wantError    bool
		wantPath     string
		wantBody     AnalysisStatus
	}{
		{
			name:         "happy path running",
			state:        messaging.RunningState,
			message:      "deployment X is running",
			serverStatus: http.StatusOK,
			wantPath:     "/" + string(externalID) + "/status",
			wantBody:     AnalysisStatus{Host: hostname, State: messaging.RunningState, Message: "deployment X is running"},
		},
		{
			name:         "happy path with base path",
			state:        messaging.SucceededState,
			message:      "done",
			basePath:     "/job",
			serverStatus: http.StatusAccepted,
			wantPath:     "/job/" + string(externalID) + "/status",
			wantBody:     AnalysisStatus{Host: hostname, State: messaging.SucceededState, Message: "done"},
		},
		{
			name:         "listener returns 500",
			state:        messaging.FailedState,
			message:      "boom",
			serverStatus: http.StatusInternalServerError,
			serverBody:   "internal error",
			wantError:    true,
		},
		{
			name:         "listener returns 400",
			state:        messaging.RunningState,
			serverStatus: http.StatusBadRequest,
			serverBody:   "bad payload",
			wantError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotPath, gotMethod, gotContentType string
			var gotBody AnalysisStatus
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				gotMethod = r.Method
				gotContentType = r.Header.Get("Content-Type")
				body, _ := io.ReadAll(r.Body)
				_ = json.Unmarshal(body, &gotBody)
				w.WriteHeader(tt.serverStatus)
				if tt.serverBody != "" {
					_, _ = io.WriteString(w, tt.serverBody)
				}
			}))
			t.Cleanup(srv.Close)

			p, err := NewStatusPublisher(srv.URL+tt.basePath, hostname)
			require.NoError(t, err)

			err = p.Publish(context.Background(), externalID, tt.state, tt.message)
			if tt.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, http.MethodPost, gotMethod)
			assert.Equal(t, tt.wantPath, gotPath)
			assert.Equal(t, "application/json", gotContentType)
			assert.Equal(t, tt.wantBody, gotBody)
		})
	}
}

func TestStatusPublisherPublishContextCancelled(t *testing.T) {
	// Server that hangs long enough for the cancelled context to short-circuit
	// the request.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	p, err := NewStatusPublisher(srv.URL, "test-host")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled
	err = p.Publish(ctx, "ext-1", messaging.RunningState, "x")
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "context canceled") || strings.Contains(err.Error(), "context deadline exceeded"),
		"expected context error, got %v", err)
}
