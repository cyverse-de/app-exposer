package quota

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeAnalysisStatusLookup implements apps.AnalysisStatusLookup for unit
// tests. Per-analysis status and error results are configurable.
type fakeAnalysisStatusLookup struct {
	statuses map[string]string // analysisID -> status
	err      error             // returned for any lookup when non-nil
}

func (f *fakeAnalysisStatusLookup) GetAnalysisStatus(_ context.Context, analysisID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	status, ok := f.statuses[analysisID]
	if !ok {
		return "", sql.ErrNoRows
	}
	return status, nil
}

// newListingResponse builds the JSON payload an operator would return for
// its /analyses listing endpoint with the given analysis IDs.
func newListingResponse(analysisIDs ...string) []byte {
	info := reporting.NewResourceInfo()
	for _, id := range analysisIDs {
		info.Deployments = append(info.Deployments, reporting.DeploymentInfo{
			MetaInfo: reporting.MetaInfo{
				AnalysisID: id,
				Name:       "dep-" + id,
			},
		})
	}
	body, _ := json.Marshal(info)
	return body
}

// newListingServer serves /analyses with the supplied body. Other paths
// return 404 so the test fails loudly if a call unexpectedly lands.
func newListingServer(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/analyses", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// newTestEnforcer wires an Enforcer with the supplied scheduler and fake
// analysis-status lookup. The DB/NATS fields are left nil because
// countJobsForUser doesn't touch them.
func newTestEnforcer(t *testing.T, scheduler *operatorclient.Scheduler, lookup *fakeAnalysisStatusLookup) *Enforcer {
	t.Helper()
	return &Enforcer{
		apps:      lookup,
		scheduler: scheduler,
	}
}

func newTestScheduler(t *testing.T, operators ...operatorclient.OperatorConfig) *operatorclient.Scheduler {
	t.Helper()
	s, err := operatorclient.NewScheduler(operators, nil)
	require.NoError(t, err)
	return s
}

func TestCountJobsForUserNoScheduler(t *testing.T) {
	e := &Enforcer{} // scheduler intentionally nil
	_, _, err := e.countJobsForUser(context.Background(), "user-1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "scheduler not configured")
}

func TestCountJobsForUserNoOperators(t *testing.T) {
	sched := newTestScheduler(t)
	e := newTestEnforcer(t, sched, &fakeAnalysisStatusLookup{})
	count, degraded, err := e.countJobsForUser(context.Background(), "user-1")
	require.NoError(t, err)
	assert.Equal(t, 0, count)
	assert.Empty(t, degraded)
}

func TestCountJobsForUserAllOperatorsRespond(t *testing.T) {
	// Two analyses on op-a (one Running, one Completed), one on op-b (Running).
	// Only the Running ones should count.
	srvA := newListingServer(t, http.StatusOK, newListingResponse("an-a1", "an-a2"))
	srvB := newListingServer(t, http.StatusOK, newListingResponse("an-b1"))

	sched := newTestScheduler(t,
		operatorclient.OperatorConfig{Name: "op-a", URL: srvA.URL},
		operatorclient.OperatorConfig{Name: "op-b", URL: srvB.URL},
	)

	lookup := &fakeAnalysisStatusLookup{
		statuses: map[string]string{
			"an-a1": "Running",
			"an-a2": "Completed", // skipped by shouldCountStatus
			"an-b1": "Running",
		},
	}
	e := newTestEnforcer(t, sched, lookup)

	count, degraded, err := e.countJobsForUser(context.Background(), "user-1")
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	assert.Empty(t, degraded, "no operators should be marked degraded when all respond")
}

func TestCountJobsForUserOperatorListingFailure(t *testing.T) {
	// op-a responds with 500 → listing error → degraded.
	// op-b responds normally with one Running analysis.
	srvA := newListingServer(t, http.StatusInternalServerError, []byte("internal"))
	srvB := newListingServer(t, http.StatusOK, newListingResponse("an-b1"))

	sched := newTestScheduler(t,
		operatorclient.OperatorConfig{Name: "op-a", URL: srvA.URL},
		operatorclient.OperatorConfig{Name: "op-b", URL: srvB.URL},
	)

	lookup := &fakeAnalysisStatusLookup{
		statuses: map[string]string{
			"an-b1": "Running",
		},
	}
	e := newTestEnforcer(t, sched, lookup)

	count, degraded, err := e.countJobsForUser(context.Background(), "user-1")
	require.NoError(t, err, "listing errors should NOT be fatal — count-surviving policy")
	assert.Equal(t, 1, count, "only visible (op-b) jobs counted")
	assert.Equal(t, []string{"op-a"}, degraded, "degraded list should name the failed operator")
}

func TestCountJobsForUserSkipsErrNoRows(t *testing.T) {
	// Deployment exists in the cluster but has no DB row. Should not be counted.
	srv := newListingServer(t, http.StatusOK, newListingResponse("an-orphan"))

	sched := newTestScheduler(t, operatorclient.OperatorConfig{Name: "op-a", URL: srv.URL})
	lookup := &fakeAnalysisStatusLookup{statuses: map[string]string{}} // no statuses → ErrNoRows
	e := newTestEnforcer(t, sched, lookup)

	count, degraded, err := e.countJobsForUser(context.Background(), "user-1")
	require.NoError(t, err)
	assert.Equal(t, 0, count, "deployments with no DB row should not count")
	assert.Empty(t, degraded)
}

func TestCountJobsForUserPropagatesNonErrNoRows(t *testing.T) {
	// A deployment exists. GetAnalysisStatus returns a non-ErrNoRows error
	// (e.g. DB connection lost). This should be fatal because a DB outage
	// can't be localized to one operator and silently proceeding would
	// silently corrupt quota decisions for every user.
	srv := newListingServer(t, http.StatusOK, newListingResponse("an-1"))

	sched := newTestScheduler(t, operatorclient.OperatorConfig{Name: "op-a", URL: srv.URL})
	injected := errors.New("db connection lost")
	lookup := &fakeAnalysisStatusLookup{err: injected}
	e := newTestEnforcer(t, sched, lookup)

	count, degraded, err := e.countJobsForUser(context.Background(), "user-1")
	require.Error(t, err, "non-ErrNoRows should be fatal")
	assert.True(t, errors.Is(err, injected), "fatal error should wrap the underlying DB error, got: %v", err)
	assert.Equal(t, 0, count)
	assert.Empty(t, degraded)
}

func TestCountJobsForUserCountSurvivingBelowLimit(t *testing.T) {
	// Integration-style: user has 2 visible jobs across surviving operators
	// and one operator is down. Count is still 2; the caller (ValidateJob)
	// decides whether the user is over a limit using this lower bound.
	srvA := newListingServer(t, http.StatusOK, newListingResponse("an-a1", "an-a2"))
	srvDown := newListingServer(t, http.StatusInternalServerError, []byte("down"))

	sched := newTestScheduler(t,
		operatorclient.OperatorConfig{Name: "op-a", URL: srvA.URL},
		operatorclient.OperatorConfig{Name: "op-down", URL: srvDown.URL},
	)

	lookup := &fakeAnalysisStatusLookup{
		statuses: map[string]string{
			"an-a1": "Running",
			"an-a2": "Submitted",
		},
	}
	e := newTestEnforcer(t, sched, lookup)

	count, degraded, err := e.countJobsForUser(context.Background(), "user-1")
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	assert.Equal(t, []string{"op-down"}, degraded)
}

func TestShouldCountStatus(t *testing.T) {
	tests := map[string]bool{
		"Submitted": true,
		"Running":   true,
		"Failed":    false,
		"Completed": false,
		"Canceled":  false,
		"":          true, // unknown statuses default to counted (conservative)
	}
	for status, want := range tests {
		t.Run(status, func(t *testing.T) {
			assert.Equal(t, want, shouldCountStatus(status))
		})
	}
}
