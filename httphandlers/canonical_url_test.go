package httphandlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/cyverse-de/app-exposer/db"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// operatorRowColumns and operatorRow build a sqlmock Rows matching the
// SELECT list used by Database.GetOperatorByID. The created_at/updated_at
// columns are non-nullable in the schema, so they must carry real time
// values even in fixtures.
var operatorRowColumns = []string{
	"id", "name", "url", "tls_skip_verify", "priority", "base_url",
	"accepting_launches", "deactivated",
	"last_reconciled_at", "reconciled_by", "created_at", "updated_at",
}

func operatorRow(id uuid.UUID, name, opURL string, baseURL any) *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows(operatorRowColumns).
		AddRow(id, name, opURL, false, 0, baseURL, true, false,
			sql.NullTime{}, sql.NullString{}, now, now)
}

func TestBuildCanonicalURL(t *testing.T) {
	tests := []struct {
		name      string
		baseURL   string
		subdomain string
		want      string
		wantErr   bool
	}{
		{
			name:      "https without port",
			baseURL:   "https://sandbox.cyverse.rocks",
			subdomain: "a38c27842",
			want:      "https://a38c27842.sandbox.cyverse.rocks",
		},
		{
			name:      "https with port preserved",
			baseURL:   "https://cyverse.run:4343",
			subdomain: "a38c27842",
			want:      "https://a38c27842.cyverse.run:4343",
		},
		{
			name:      "http scheme preserved",
			baseURL:   "http://localhost:8080",
			subdomain: "abc",
			want:      "http://abc.localhost:8080",
		},
		{
			name:      "trailing slash on base preserved",
			baseURL:   "https://sandbox.cyverse.rocks/",
			subdomain: "a1",
			want:      "https://a1.sandbox.cyverse.rocks/",
		},
		{
			name:      "empty base URL",
			baseURL:   "",
			subdomain: "a1",
			wantErr:   true,
		},
		{
			name:      "base URL with no host",
			baseURL:   "https://",
			subdomain: "a1",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildCanonicalURL(tt.baseURL, tt.subdomain)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// newTestDB returns a *db.Database backed by sqlmock for query-shape testing
// without a live PostgreSQL. Mirrors the helper in db/operators_test.go.
func newTestDB(t *testing.T) (*db.Database, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	sqlxDB := sqlx.NewDb(rawDB, "postgres")
	return db.New(sqlxDB, ""), mock
}

// fakeOperator stands up an httptest.Server that responds to GET /analyses
// with the configured ResourceInfo (or an error status). Used to compose a
// Scheduler whose clients reach test servers instead of real operators.
type fakeOperator struct {
	name        string
	id          uuid.UUID
	server      *httptest.Server
	listingResp *reporting.ResourceInfo
	listingCode int
}

func newFakeOperator(t *testing.T, name string, deployments []reporting.DeploymentInfo) *fakeOperator {
	t.Helper()
	fo := &fakeOperator{
		name: name,
		id:   uuid.New(),
		listingResp: &reporting.ResourceInfo{
			Deployments: deployments,
			Pods:        []reporting.PodInfo{},
			ConfigMaps:  []reporting.ConfigMapInfo{},
			Services:    []reporting.ServiceInfo{},
			Ingresses:   []reporting.IngressInfo{},
			Routes:      []reporting.RouteInfo{},
		},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/analyses", func(w http.ResponseWriter, r *http.Request) {
		if fo.listingCode != 0 {
			http.Error(w, "injected", fo.listingCode)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(fo.listingResp)
	})
	fo.server = httptest.NewServer(mux)
	t.Cleanup(fo.server.Close)
	return fo
}

func (f *fakeOperator) summary(baseURL *string) operatorclient.OperatorAdminSummary {
	return operatorclient.OperatorAdminSummary{
		ID: f.id,
		OperatorConfig: operatorclient.OperatorConfig{
			Name:    f.name,
			URL:     f.server.URL,
			BaseURL: baseURL,
		},
	}
}

// newHandlerWithScheduler wires an HTTPHandlers value with a real Scheduler
// (Sync'd from the given summaries) and a sqlmock-backed *db.Database. Skips
// SetScheduler so the nil incluster doesn't blow up — production callers wire
// incluster first.
func newHandlerWithScheduler(t *testing.T, summaries []operatorclient.OperatorAdminSummary) (*HTTPHandlers, sqlmock.Sqlmock) {
	t.Helper()
	testDB, mock := newTestDB(t)
	h := New(nil, nil, nil, nil, testDB, 0)
	sched := operatorclient.NewScheduler(nil)
	require.NoError(t, sched.Sync(summaries))
	h.scheduler = sched
	return h, mock
}

// requestCanonicalURL routes a GET /vice/admin/:host/canonical-url through
// the Echo router so the :host path param is bound the same way as in
// production.
func requestCanonicalURL(t *testing.T, h *HTTPHandlers, host string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	e.GET("/vice/admin/:host/canonical-url", h.AdminCanonicalURLHandler)
	req := httptest.NewRequest(http.MethodGet, "/vice/admin/"+url.PathEscape(host)+"/canonical-url", nil)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	return rec
}

func TestAdminCanonicalURLHandler(t *testing.T) {
	const subdomain = "a38c27842"

	t.Run("match returns canonical URL", func(t *testing.T) {
		owner := newFakeOperator(t, "aws", []reporting.DeploymentInfo{
			{MetaInfo: reporting.MetaInfo{Name: "vice-" + subdomain}},
		})
		bystander := newFakeOperator(t, "qa", nil)

		baseURL := "https://sandbox.cyverse.rocks"
		h, mock := newHandlerWithScheduler(t, []operatorclient.OperatorAdminSummary{
			owner.summary(&baseURL),
			bystander.summary(nil),
		})

		mock.ExpectQuery(`SELECT id, name, url, tls_skip_verify, priority, base_url, accepting_launches, deactivated, last_reconciled_at, reconciled_by, created_at, updated_at FROM operators WHERE id = $1`).
			WithArgs(owner.id).
			WillReturnRows(operatorRow(owner.id, "aws", owner.server.URL, baseURL))

		rec := requestCanonicalURL(t, h, subdomain)
		require.Equal(t, http.StatusOK, rec.Code, "body: %s", rec.Body.String())

		var body CanonicalURLResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		assert.Equal(t, "https://"+subdomain+".sandbox.cyverse.rocks", body.URL)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no operator owns subdomain returns 404", func(t *testing.T) {
		bystander := newFakeOperator(t, "qa", nil)
		h, _ := newHandlerWithScheduler(t, []operatorclient.OperatorAdminSummary{
			bystander.summary(nil),
		})

		rec := requestCanonicalURL(t, h, subdomain)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("every operator errors returns 502", func(t *testing.T) {
		op1 := newFakeOperator(t, "op1", nil)
		op1.listingCode = http.StatusInternalServerError
		op2 := newFakeOperator(t, "op2", nil)
		op2.listingCode = http.StatusInternalServerError

		h, _ := newHandlerWithScheduler(t, []operatorclient.OperatorAdminSummary{
			op1.summary(nil),
			op2.summary(nil),
		})

		rec := requestCanonicalURL(t, h, subdomain)
		assert.Equal(t, http.StatusBadGateway, rec.Code, "body: %s", rec.Body.String())
	})

	t.Run("owning operator with NULL base_url returns 404", func(t *testing.T) {
		owner := newFakeOperator(t, "legacy", []reporting.DeploymentInfo{
			{MetaInfo: reporting.MetaInfo{Name: "vice-" + subdomain}},
		})
		h, mock := newHandlerWithScheduler(t, []operatorclient.OperatorAdminSummary{
			owner.summary(nil),
		})

		mock.ExpectQuery(`SELECT id, name, url, tls_skip_verify, priority, base_url, accepting_launches, deactivated, last_reconciled_at, reconciled_by, created_at, updated_at FROM operators WHERE id = $1`).
			WithArgs(owner.id).
			WillReturnRows(operatorRow(owner.id, "legacy", owner.server.URL, sql.NullString{}))

		rec := requestCanonicalURL(t, h, subdomain)
		assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("owning operator row deleted under scheduler returns 404", func(t *testing.T) {
		owner := newFakeOperator(t, "vanished", []reporting.DeploymentInfo{
			{MetaInfo: reporting.MetaInfo{Name: "vice-" + subdomain}},
		})
		h, mock := newHandlerWithScheduler(t, []operatorclient.OperatorAdminSummary{
			owner.summary(nil),
		})

		mock.ExpectQuery(`SELECT id, name, url, tls_skip_verify, priority, base_url, accepting_launches, deactivated, last_reconciled_at, reconciled_by, created_at, updated_at FROM operators WHERE id = $1`).
			WithArgs(owner.id).
			WillReturnError(sql.ErrNoRows)

		rec := requestCanonicalURL(t, h, subdomain)
		assert.Equal(t, http.StatusNotFound, rec.Code, "body: %s", rec.Body.String())
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("no operators configured returns 404", func(t *testing.T) {
		h, _ := newHandlerWithScheduler(t, nil)
		rec := requestCanonicalURL(t, h, subdomain)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}

// TestFindOperatorForSubdomain_PrefersDeploymentMatch ensures that when one
// operator reports the deployment and others don't, the deployment-bearing
// operator is selected even if it isn't first in the list.
func TestFindOperatorForSubdomain_PrefersDeploymentMatch(t *testing.T) {
	empty := newFakeOperator(t, "first", nil)
	owner := newFakeOperator(t, "second", []reporting.DeploymentInfo{
		{MetaInfo: reporting.MetaInfo{Name: "vice-a1"}},
	})

	h, _ := newHandlerWithScheduler(t, []operatorclient.OperatorAdminSummary{
		empty.summary(nil),
		owner.summary(nil),
	})

	client, opErrs, err := h.findOperatorForSubdomain(context.Background(), "a1")
	require.NoError(t, err)
	assert.Empty(t, opErrs)
	require.NotNil(t, client)
	assert.Equal(t, owner.id, client.ID())
}
