package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// viceProxyForwardContext builds a minimal Echo context with the
// analysis-id path param populated so the forwarding handlers can run.
func viceProxyForwardContext(e *echo.Echo, method, body, analysisID string) (echo.Context, *httptest.ResponseRecorder) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, "/", reader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues(analysisID)
	return c, rec
}

// createAnalysisService registers a Service with the analysis-id label so
// findAnalysisService can locate it.
func createAnalysisService(t *testing.T, op *Operator, analysisID, svcName string) {
	t.Helper()
	_, err := op.clientset.CoreV1().Services(op.namespace).Create(context.Background(), &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: op.namespace,
			Labels:    map[string]string{"analysis-id": analysisID},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
}

func TestForwardToViceProxyMissingService(t *testing.T) {
	// No service registered for the analysis — handler should 404 rather
	// than attempt an HTTP call to a bogus hostname. Catching this at the
	// service-lookup layer keeps a bad analysis-id from producing an
	// ambiguous "unreachable" error.
	op, _, _ := newTestOperator(t, 10)

	_, err := op.forwardToViceProxy(context.Background(), "missing-analysis", http.MethodGet, "/active-sessions", nil)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok, "expected *echo.HTTPError, got %T", err)
	assert.Equal(t, http.StatusNotFound, he.Code)
}

func TestForwardToViceProxyUnreachable(t *testing.T) {
	// The vice-proxy sidecar is reachable according to DNS but the
	// transport errors out (connection refused, TLS failure, etc.).
	// Must translate to 502 Bad Gateway so callers can tell this apart
	// from "analysis doesn't exist".
	op, _, _ := newTestOperator(t, 10)
	createAnalysisService(t, op, "an-unreachable", "svc-unreachable")

	op.httpClient = &mockHTTPClient{
		DoFunc: func(*http.Request) (*http.Response, error) {
			return nil, errors.New("simulated connection refused")
		},
	}

	_, err := op.forwardToViceProxy(context.Background(), "an-unreachable", http.MethodGet, "/active-sessions", nil)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadGateway, he.Code)
	assert.Contains(t, fmt.Sprint(he.Message), "failed to reach vice-proxy")
}

func TestForwardToViceProxyNon2xx(t *testing.T) {
	// The sidecar answered but returned 500. This must also be 502 at
	// our edge — the failure is upstream and the client can't
	// meaningfully retry at their layer. Same treatment whether the
	// body is empty or descriptive.
	op, _, _ := newTestOperator(t, 10)
	createAnalysisService(t, op, "an-500", "svc-500")

	op.httpClient = &mockHTTPClient{
		DoFunc: func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(strings.NewReader("proxy exploded")),
			}, nil
		},
	}

	_, err := op.forwardToViceProxy(context.Background(), "an-500", http.MethodGet, "/active-sessions", nil)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadGateway, he.Code)
	assert.Contains(t, fmt.Sprint(he.Message), "500")
}

func TestHandleGetActiveSessionsHappyPath(t *testing.T) {
	// Service exists, proxy returns a well-formed ActiveSessionsResponse.
	// The handler must pass the body through verbatim as JSON and
	// forward the correct method+path to the vice-proxy.
	op, _, _ := newTestOperator(t, 10)
	createAnalysisService(t, op, "an-active", "svc-active")

	var capturedMethod, capturedPath string
	op.httpClient = &mockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			capturedMethod = req.Method
			capturedPath = req.URL.Path
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"sessions":[{"session_id":"s1","username":"alice"}]}`)),
			}, nil
		},
	}

	e := echo.New()
	c, rec := viceProxyForwardContext(e, http.MethodGet, "", "an-active")
	require.NoError(t, op.HandleGetActiveSessions(c))
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, http.MethodGet, capturedMethod)
	assert.Equal(t, "/active-sessions", capturedPath)
	assert.JSONEq(t, `{"sessions":[{"session_id":"s1","username":"alice"}]}`, rec.Body.String())
}

func TestHandleLogoutUserHappyPath(t *testing.T) {
	// Happy path for the logout forwarder: the body is re-marshalled
	// and sent to the sidecar's /logout-user endpoint, and a successful
	// response is returned verbatim to the caller.
	op, _, _ := newTestOperator(t, 10)
	createAnalysisService(t, op, "an-logout", "svc-logout")

	var capturedBody []byte
	op.httpClient = &mockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			capturedBody, _ = io.ReadAll(req.Body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader([]byte(`{"sessions_invalidated":2}`))),
			}, nil
		},
	}

	e := echo.New()
	c, rec := viceProxyForwardContext(e, http.MethodPost, `{"username":"alice"}`, "an-logout")
	require.NoError(t, op.HandleLogoutUser(c))
	assert.Equal(t, http.StatusOK, rec.Code)
	// The handler re-marshals via operatorclient.LogoutUserRequest, so the
	// body shape we observe at the sidecar is exactly that struct.
	assert.JSONEq(t, `{"username":"alice"}`, string(capturedBody))
}

func TestHandleLogoutUserMissingUsername(t *testing.T) {
	// Empty username must 400 at the handler — we should never ask the
	// sidecar to invalidate sessions for the empty string, which could
	// invalidate every unauthenticated session on some implementations.
	op, _, _ := newTestOperator(t, 10)
	createAnalysisService(t, op, "an-empty", "svc-empty")

	// Guard: if the handler accidentally reaches the HTTP layer we want
	// the test to surface it loudly rather than a silent pass.
	op.httpClient = &mockHTTPClient{
		DoFunc: func(*http.Request) (*http.Response, error) {
			t.Fatal("HandleLogoutUser must not forward when username is empty")
			return nil, nil
		},
	}

	e := echo.New()
	c, _ := viceProxyForwardContext(e, http.MethodPost, `{"username":""}`, "an-empty")
	err := op.HandleLogoutUser(c)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, he.Code)
}

// TestOperatorLogoutUserRequestRoundTrip guards against the shared
// request type drifting in a way that breaks this forwarder — if a
// future refactor renames a field, this test will surface it.
func TestOperatorLogoutUserRequestRoundTrip(t *testing.T) {
	want := operatorclient.LogoutUserRequest{Username: "bob"}
	op, _, _ := newTestOperator(t, 10)
	createAnalysisService(t, op, "an-round", "svc-round")

	var capturedBody []byte
	op.httpClient = &mockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			capturedBody, _ = io.ReadAll(req.Body)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"sessions_invalidated":0}`)),
			}, nil
		},
	}

	e := echo.New()
	c, _ := viceProxyForwardContext(e, http.MethodPost, `{"username":"bob"}`, "an-round")
	require.NoError(t, op.HandleLogoutUser(c))

	var gotSent operatorclient.LogoutUserRequest
	require.NoError(t, decodeJSON(capturedBody, &gotSent))
	assert.Equal(t, want, gotSent)
}

// decodeJSON is a tiny helper to keep the assertion block tight.
func decodeJSON(data []byte, out any) error {
	return json.Unmarshal(data, out)
}
func TestHandleGetPermissions(t *testing.T) {
	analysisID := "perms-test-1"
	labels := map[string]string{"analysis-id": analysisID}

	op, clientset, _ := newTestOperator(t, 10)
	ctx := context.Background()

	// Create a permissions ConfigMap.
	_, err := clientset.CoreV1().ConfigMaps("vice-apps").Create(ctx, &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "permissions-" + analysisID,
			Labels: labels,
		},
		Data: map[string]string{
			"allowed-users": "user1@example.org\nuser2@example.org\n",
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Call HandleGetPermissions.
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues(analysisID)

	err = op.HandleGetPermissions(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp PermissionsResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Equal(t, []string{"user1@example.org", "user2@example.org"}, resp.AllowedUsers)
}

func TestHandleGetPermissionsNotFound(t *testing.T) {
	op, _, _ := newTestOperator(t, 10)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues("nonexistent")

	err := op.HandleGetPermissions(c)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusNotFound, he.Code)
}

func TestHandleUpdatePermissions(t *testing.T) {
	analysisID := "perms-update-1"
	labels := map[string]string{"analysis-id": analysisID}

	op, clientset, _ := newTestOperator(t, 10)
	ctx := context.Background()

	// Create existing permissions ConfigMap.
	_, err := clientset.CoreV1().ConfigMaps("vice-apps").Create(ctx, &apiv1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "permissions-" + analysisID,
			Labels: labels,
		},
		Data: map[string]string{"allowed-users": "old-user\n"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Call HandleUpdatePermissions with new users.
	body, _ := json.Marshal(operatorclient.UpdatePermissionsRequest{AllowedUsers: []string{"new-user1", "new-user2"}})
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues(analysisID)

	err = op.HandleUpdatePermissions(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Verify the ConfigMap was updated.
	cm, err := clientset.CoreV1().ConfigMaps("vice-apps").Get(ctx, "permissions-"+analysisID, metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "new-user1\nnew-user2\n", cm.Data["allowed-users"])
}

func TestHandleUpdatePermissionsEmptyUsers(t *testing.T) {
	op, _, _ := newTestOperator(t, 10)

	body, _ := json.Marshal(operatorclient.UpdatePermissionsRequest{AllowedUsers: []string{}})
	e := echo.New()
	req := httptest.NewRequest(http.MethodPut, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("analysis-id")
	c.SetParamValues("any-id")

	err := op.HandleUpdatePermissions(c)
	require.Error(t, err)
	he, ok := err.(*echo.HTTPError)
	require.True(t, ok)
	assert.Equal(t, http.StatusBadRequest, he.Code)
}
