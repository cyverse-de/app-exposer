package operator

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/messaging/v12"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// recordingListener captures every status POST so tests can assert which
// updates the informer actually emitted.
type recordingListener struct {
	mu       sync.Mutex
	requests []StatusUpdatePayload
}

func (r *recordingListener) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		var status StatusUpdatePayload
		_ = json.Unmarshal(body, &status)
		r.mu.Lock()
		r.requests = append(r.requests, status)
		r.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
}

func (r *recordingListener) snapshot() []StatusUpdatePayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]StatusUpdatePayload, len(r.requests))
	copy(out, r.requests)
	return out
}

// newTestInformer constructs an informer + publisher backed by a recording
// listener. Returns the informer and the listener so tests can drive the
// informer's handler methods directly and inspect the resulting traffic.
func newTestInformer(t *testing.T) (*StatusInformer, *recordingListener) {
	t.Helper()
	rec := &recordingListener{}
	srv := httptest.NewServer(rec.handler())
	t.Cleanup(srv.Close)

	publisher, err := NewStatusPublisher(srv.URL, "test-cluster")
	require.NoError(t, err)

	informer, err := NewStatusInformer(StatusInformerConfig{
		Clientset:      fake.NewSimpleClientset(),
		Publisher:      publisher,
		Namespace:      "vice-apps",
		LeaseNamespace: "vice-apps",
		LeaseName:      "vice-operator-status-publisher",
		Identity:       "test-pod",
	})
	require.NoError(t, err)
	return informer, rec
}

func deployment(externalID, name string, availableReplicas int32) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "vice-apps",
			Labels: map[string]string{
				constants.ExternalIDLabel: externalID,
				constants.AppTypeLabel:    string(constants.Interactive),
				constants.AppNameLabel:    "my-app",
			},
		},
		Status: appsv1.DeploymentStatus{
			AvailableReplicas: availableReplicas,
		},
	}
}

func TestNewStatusInformerValidation(t *testing.T) {
	cs := fake.NewSimpleClientset()
	pub, err := NewStatusPublisher("http://example.org", "h")
	require.NoError(t, err)

	tests := []struct {
		name string
		cfg  StatusInformerConfig
	}{
		{"missing clientset", StatusInformerConfig{Publisher: pub, Namespace: "ns", LeaseNamespace: "ns", LeaseName: "l", Identity: "i"}},
		{"missing publisher", StatusInformerConfig{Clientset: cs, Namespace: "ns", LeaseNamespace: "ns", LeaseName: "l", Identity: "i"}},
		{"missing namespace", StatusInformerConfig{Clientset: cs, Publisher: pub, LeaseNamespace: "ns", LeaseName: "l", Identity: "i"}},
		{"missing lease namespace", StatusInformerConfig{Clientset: cs, Publisher: pub, Namespace: "ns", LeaseName: "l", Identity: "i"}},
		{"missing lease name", StatusInformerConfig{Clientset: cs, Publisher: pub, Namespace: "ns", LeaseNamespace: "ns", Identity: "i"}},
		{"missing identity", StatusInformerConfig{Clientset: cs, Publisher: pub, Namespace: "ns", LeaseNamespace: "ns", LeaseName: "l"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewStatusInformer(tt.cfg)
			assert.Error(t, err)
		})
	}
}

func TestHandleAddOrUpdate(t *testing.T) {
	tests := []struct {
		name        string
		deps        []*appsv1.Deployment
		wantStates  []messaging.JobState
		wantHosts   []string
		description string
	}{
		{
			name:        "ready deployment fires Running once",
			deps:        []*appsv1.Deployment{deployment("ext-1", "vice-1", 1)},
			wantStates:  []messaging.JobState{messaging.RunningState},
			description: "first observation with available replica publishes Running",
		},
		{
			name:        "not-ready deployment does not publish",
			deps:        []*appsv1.Deployment{deployment("ext-1", "vice-1", 0)},
			wantStates:  nil,
			description: "Pending pod with no available replica is suppressed until ready",
		},
		{
			name: "ready then ready again deduplicates",
			deps: []*appsv1.Deployment{
				deployment("ext-1", "vice-1", 1),
				deployment("ext-1", "vice-1", 1),
			},
			wantStates:  []messaging.JobState{messaging.RunningState},
			description: "repeated Running observations should publish only once",
		},
		{
			name: "not-ready then ready publishes Running once",
			deps: []*appsv1.Deployment{
				deployment("ext-1", "vice-1", 0),
				deployment("ext-1", "vice-1", 1),
			},
			wantStates: []messaging.JobState{messaging.RunningState},
		},
		{
			name: "two different deployments both publish",
			deps: []*appsv1.Deployment{
				deployment("ext-1", "vice-1", 1),
				deployment("ext-2", "vice-2", 1),
			},
			wantStates: []messaging.JobState{messaging.RunningState, messaging.RunningState},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			informer, rec := newTestInformer(t)
			ctx := context.Background()
			for _, dep := range tt.deps {
				informer.handleAddOrUpdate(ctx, dep)
			}
			snap := rec.snapshot()
			var gotStates []messaging.JobState
			for _, r := range snap {
				gotStates = append(gotStates, r.State)
			}
			assert.Equal(t, tt.wantStates, gotStates, tt.description)
		})
	}
}

func TestHandleAddOrUpdateSkipsDeletingDeployment(t *testing.T) {
	informer, rec := newTestInformer(t)
	now := metav1.NewTime(time.Now())
	dep := deployment("ext-1", "vice-1", 1)
	dep.DeletionTimestamp = &now

	informer.handleAddOrUpdate(context.Background(), dep)
	assert.Empty(t, rec.snapshot(), "deployments mid-deletion should be left for the delete handler")
}

func TestHandleAddOrUpdateRequiresExternalIDLabel(t *testing.T) {
	informer, rec := newTestInformer(t)
	dep := deployment("ext-1", "vice-1", 1)
	delete(dep.Labels, constants.ExternalIDLabel)

	informer.handleAddOrUpdate(context.Background(), dep)
	assert.Empty(t, rec.snapshot(), "deployments without external-id label should be skipped")
}

func TestHandleDeletePublishesSucceeded(t *testing.T) {
	informer, rec := newTestInformer(t)
	dep := deployment("ext-1", "vice-1", 1)

	informer.handleAddOrUpdate(context.Background(), dep)
	informer.handleDelete(context.Background(), dep)

	got := rec.snapshot()
	require.Len(t, got, 2)
	assert.Equal(t, messaging.RunningState, got[0].State)
	assert.Equal(t, messaging.SucceededState, got[1].State)
}

func TestHandleDeleteClearsCacheForRelaunch(t *testing.T) {
	informer, rec := newTestInformer(t)
	dep := deployment("ext-1", "vice-1", 1)

	informer.handleAddOrUpdate(context.Background(), dep)
	informer.handleDelete(context.Background(), dep)
	// Relaunch with the same external ID — should publish Running again
	// (not be suppressed by the cached Succeeded state).
	informer.handleAddOrUpdate(context.Background(), dep)

	got := rec.snapshot()
	require.Len(t, got, 3)
	assert.Equal(t, messaging.RunningState, got[0].State)
	assert.Equal(t, messaging.SucceededState, got[1].State)
	assert.Equal(t, messaging.RunningState, got[2].State)
}

func TestPublishIfChangedDoesNotCacheOnError(t *testing.T) {
	// First listener fails; second succeeds. Both observations of the
	// same Running state should result in two POST attempts because the
	// first didn't update the cache.
	var (
		mu        sync.Mutex
		callCount int
		failFirst = true
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		callCount++
		shouldFail := failFirst && callCount == 1
		mu.Unlock()
		if shouldFail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	publisher, err := NewStatusPublisher(srv.URL, "h")
	require.NoError(t, err)
	informer, err := NewStatusInformer(StatusInformerConfig{
		Clientset:      fake.NewSimpleClientset(),
		Publisher:      publisher,
		Namespace:      "vice-apps",
		LeaseNamespace: "vice-apps",
		LeaseName:      "l",
		Identity:       "i",
	})
	require.NoError(t, err)

	dep := deployment("ext-1", "vice-1", 1)
	informer.handleAddOrUpdate(context.Background(), dep)
	informer.handleAddOrUpdate(context.Background(), dep)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 2, callCount, "retried after first POST failed")
}

func TestResetStateAllowsRepublish(t *testing.T) {
	informer, rec := newTestInformer(t)
	dep := deployment("ext-1", "vice-1", 1)

	informer.handleAddOrUpdate(context.Background(), dep)
	// Simulate leadership handoff: resetState wipes the cache so a re-elected
	// leader publishes the current state again (the bounded-duplicate path
	// called out in plans/push-based-status-updates.md).
	informer.resetState()
	informer.handleAddOrUpdate(context.Background(), dep)

	got := rec.snapshot()
	require.Len(t, got, 2)
	assert.Equal(t, messaging.RunningState, got[0].State)
	assert.Equal(t, messaging.RunningState, got[1].State)
}
