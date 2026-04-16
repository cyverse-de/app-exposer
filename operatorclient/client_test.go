package operatorclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestClient spins up an httptest.Server running the supplied handler,
// returns a Client aimed at it, and registers cleanup. Uses no token source
// to keep assertions on request bodies/methods free of bearer-header noise.
func newTestClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	client, err := NewClient(OperatorConfig{Name: "test-op", URL: srv.URL}, nil)
	require.NoError(t, err)
	return client, srv
}

func TestNewClient(t *testing.T) {
	tests := []struct {
		name          string
		cfg           OperatorConfig
		wantErr       bool
		wantErrSubstr string
	}{
		{
			name: "valid URL",
			cfg:  OperatorConfig{Name: "op-a", URL: "http://op-a.example.invalid"},
		},
		{
			name:          "invalid URL",
			cfg:           OperatorConfig{Name: "op-bad", URL: "://nope"},
			wantErr:       true,
			wantErrSubstr: "parsing operator URL",
		},
		{
			// Verify the TLSSkipVerify branch constructs without error.
			// Exercising the actual InsecureSkipVerify flag would require
			// introspecting the otelhttp-wrapped transport, which offers no
			// stable public API for unwrapping in the version in use here.
			name: "TLS skip verify constructs successfully",
			cfg:  OperatorConfig{Name: "op-skip", URL: "https://op.example.invalid", TLSSkipVerify: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewClient(tt.cfg, nil)
			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrSubstr != "" {
					assert.Contains(t, err.Error(), tt.wantErrSubstr)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.cfg.Name, c.Name())
			assert.NotNil(t, c.http, "http client must be initialized")
			assert.Equal(t, 30*time.Second, c.http.Timeout)
		})
	}
}

// clientCall captures the shape of a Client method invocation for table-driven
// coverage: each entry specifies what the Client should emit and what the
// test server is allowed to return.
type clientCall struct {
	name       string
	wantMethod string
	wantPath   string
	wantQuery  url.Values // optional; checked with url.Values.Encode() equality when set
	wantBody   string     // optional; non-empty triggers JSON-equal body assertion
	respStatus int
	respBody   string // JSON body returned to the client
	// invoke executes the Client method under test against c. Returning the
	// value (or nil for methods that only return error) lets the table row
	// assert on both the result and any error.
	invoke func(ctx context.Context, c *Client) (any, error)
	// wantResult, when non-nil, is compared against the invoke return value.
	wantResult any
}

func TestClientMethods(t *testing.T) {
	// Pre-declare response payloads so invoke closures can reference them in
	// their expected-result assertions.
	capacityBody := `{"maxAnalyses":10,"runningAnalyses":3,"availableSlots":7,"allocatableCPU":0,"allocatableMemory":0,"usedCPU":0,"usedMemory":0}`
	listingBody := `{"deployments":[],"pods":[{"name":"p1","analysisID":"an-1"}],"configMaps":[],"services":[],"ingresses":[],"routes":[]}`
	statusBody := `{"deployments":[{"name":"d1"}],"pods":[]}`
	statusEmptyBody := `{"deployments":[],"pods":[]}`
	urlReadyBody := `{"ready":true,"access_url":"https://x.example"}`
	podsBody := `[{"name":"p1"}]`
	logsBody := `{"pod":"p1","container":"analysis","logs":"hello"}`
	activeSessionsBody := `{"sessions":[{"session_id":"s1","username":"u1"}]}`

	tests := []clientCall{
		{
			name: "Capacity", wantMethod: http.MethodGet, wantPath: "/capacity",
			respStatus: 200, respBody: capacityBody,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return c.Capacity(ctx)
			},
			wantResult: &CapacityResponse{MaxAnalyses: 10, RunningAnalyses: 3, AvailableSlots: 7},
		},
		{
			name: "Launch", wantMethod: http.MethodPost, wantPath: "/analyses",
			wantBody:   `{"analysisID":"an-1","deployment":null,"service":null,"httpRoute":null,"configMaps":null,"persistentVolumes":null,"persistentVolumeClaims":null,"podDisruptionBudget":null}`,
			respStatus: 200, respBody: "",
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return nil, c.Launch(ctx, &AnalysisBundle{AnalysisID: "an-1"})
			},
		},
		{
			name: "Exit", wantMethod: http.MethodDelete, wantPath: "/analyses/an-1",
			respStatus: 200,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return nil, c.Exit(ctx, "an-1")
			},
		},
		{
			name: "SaveAndExit", wantMethod: http.MethodPost, wantPath: "/analyses/an-1/save-and-exit",
			respStatus: 200,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return nil, c.SaveAndExit(ctx, "an-1")
			},
		},
		{
			name: "DownloadInputFiles", wantMethod: http.MethodPost, wantPath: "/analyses/an-1/download-input-files",
			respStatus: 200,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return nil, c.DownloadInputFiles(ctx, "an-1")
			},
		},
		{
			name: "SaveOutputFiles", wantMethod: http.MethodPost, wantPath: "/analyses/an-1/save-output-files",
			respStatus: 200,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return nil, c.SaveOutputFiles(ctx, "an-1")
			},
		},
		{
			name: "UpdatePermissions", wantMethod: http.MethodPut, wantPath: "/analyses/an-1/permissions",
			wantBody:   `{"allowedUsers":["u1","u2"]}`,
			respStatus: 200,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return nil, c.UpdatePermissions(ctx, "an-1", []string{"u1", "u2"})
			},
		},
		{
			name: "ActiveSessions", wantMethod: http.MethodGet, wantPath: "/analyses/an-1/active-sessions",
			respStatus: 200, respBody: activeSessionsBody,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return c.ActiveSessions(ctx, "an-1")
			},
			wantResult: &ActiveSessionsResponse{Sessions: []ActiveSession{{SessionID: "s1", Username: "u1"}}},
		},
		{
			name: "LogoutUser", wantMethod: http.MethodPost, wantPath: "/analyses/an-1/logout-user",
			wantBody:   `{"username":"u1"}`,
			respStatus: 200,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return nil, c.LogoutUser(ctx, "an-1", "u1")
			},
		},
		{
			name: "Listing", wantMethod: http.MethodGet, wantPath: "/analyses",
			respStatus: 200, respBody: listingBody,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return c.Listing(ctx, nil)
			},
			// Only verify the Pods slice length — field-by-field equality
			// would couple the test to reporting type internals.
			wantResult: &reporting.ResourceInfo{},
		},
		{
			name: "HasAnalysis found", wantMethod: http.MethodGet, wantPath: "/analyses/an-1/status",
			respStatus: 200, respBody: statusBody,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return c.HasAnalysis(ctx, "an-1")
			},
			wantResult: true,
		},
		{
			name: "HasAnalysis not found", wantMethod: http.MethodGet, wantPath: "/analyses/an-1/status",
			respStatus: 200, respBody: statusEmptyBody,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return c.HasAnalysis(ctx, "an-1")
			},
			wantResult: false,
		},
		{
			name: "Status", wantMethod: http.MethodGet, wantPath: "/analyses/an-1/status",
			respStatus: 200, respBody: statusBody,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return c.Status(ctx, "an-1")
			},
			wantResult: json.RawMessage(statusBody),
		},
		{
			name: "URLReady", wantMethod: http.MethodGet, wantPath: "/analyses/an-1/url-ready",
			respStatus: 200, respBody: urlReadyBody,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return c.URLReady(ctx, "an-1")
			},
			wantResult: json.RawMessage(urlReadyBody),
		},
		{
			name: "Pods", wantMethod: http.MethodGet, wantPath: "/analyses/an-1/pods",
			respStatus: 200, respBody: podsBody,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return c.Pods(ctx, "an-1")
			},
			wantResult: json.RawMessage(podsBody),
		},
		{
			name: "Logs", wantMethod: http.MethodGet, wantPath: "/analyses/an-1/logs",
			respStatus: 200, respBody: logsBody,
			invoke: func(ctx context.Context, c *Client) (any, error) {
				return c.Logs(ctx, "an-1", nil)
			},
			wantResult: json.RawMessage(logsBody),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tt.wantMethod, r.Method, "unexpected HTTP method")
				assert.Equal(t, tt.wantPath, r.URL.Path, "unexpected path")
				if tt.wantQuery != nil {
					assert.Equal(t, tt.wantQuery.Encode(), r.URL.Query().Encode(), "unexpected query")
				}
				if tt.wantBody != "" {
					body, err := io.ReadAll(r.Body)
					require.NoError(t, err)
					assert.JSONEq(t, tt.wantBody, string(body), "unexpected request body")
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.respStatus)
				if tt.respBody != "" {
					_, _ = w.Write([]byte(tt.respBody))
				}
			})

			client, _ := newTestClient(t, handler)
			got, err := tt.invoke(context.Background(), client)
			require.NoError(t, err)

			if tt.wantResult == nil {
				return
			}
			switch want := tt.wantResult.(type) {
			case bool:
				assert.Equal(t, want, got)
			case json.RawMessage:
				// json.RawMessage is []byte underneath; compare as strings
				// so diffs are readable if they drift.
				gotRaw, ok := got.(json.RawMessage)
				require.True(t, ok, "expected json.RawMessage result, got %T", got)
				assert.JSONEq(t, string(want), string(gotRaw))
			case *CapacityResponse:
				gotCap, ok := got.(*CapacityResponse)
				require.True(t, ok)
				assert.Equal(t, want.MaxAnalyses, gotCap.MaxAnalyses)
				assert.Equal(t, want.RunningAnalyses, gotCap.RunningAnalyses)
				assert.Equal(t, want.AvailableSlots, gotCap.AvailableSlots)
			case *ActiveSessionsResponse:
				gotAS, ok := got.(*ActiveSessionsResponse)
				require.True(t, ok)
				assert.Equal(t, want.Sessions, gotAS.Sessions)
			case *reporting.ResourceInfo:
				gotRI, ok := got.(*reporting.ResourceInfo)
				require.True(t, ok)
				// Happy-path smoke check: decoder produced something sensible.
				assert.Len(t, gotRI.Pods, 1, "expected one pod decoded from listing body")
			default:
				t.Fatalf("unhandled wantResult type %T in table row %q", tt.wantResult, tt.name)
			}
		})
	}
}

// TestClientErrorPropagation checks that a non-2xx response causes each
// method to return an error whose message carries the response body, rather
// than silently swallowing the failure. One row per family of error paths
// (checkStatus, getAnalysisJSON, Logs custom, Launch custom).
func TestClientErrorPropagation(t *testing.T) {
	tests := []struct {
		name   string
		path   string
		invoke func(ctx context.Context, c *Client) error
	}{
		{
			name: "Exit surfaces body via checkStatus",
			path: "/analyses/an-1",
			invoke: func(ctx context.Context, c *Client) error {
				return c.Exit(ctx, "an-1")
			},
		},
		{
			name: "SaveAndExit surfaces body via checkStatus",
			path: "/analyses/an-1/save-and-exit",
			invoke: func(ctx context.Context, c *Client) error {
				return c.SaveAndExit(ctx, "an-1")
			},
		},
		{
			name: "UpdatePermissions surfaces body via checkStatus",
			path: "/analyses/an-1/permissions",
			invoke: func(ctx context.Context, c *Client) error {
				return c.UpdatePermissions(ctx, "an-1", []string{"u"})
			},
		},
		{
			name: "Status surfaces body via getAnalysisJSON",
			path: "/analyses/an-1/status",
			invoke: func(ctx context.Context, c *Client) error {
				_, err := c.Status(ctx, "an-1")
				return err
			},
		},
		{
			name: "Logs surfaces body via custom path",
			path: "/analyses/an-1/logs",
			invoke: func(ctx context.Context, c *Client) error {
				_, err := c.Logs(ctx, "an-1", nil)
				return err
			},
		},
		{
			name: "Launch surfaces body via custom path",
			path: "/analyses",
			invoke: func(ctx context.Context, c *Client) error {
				return c.Launch(ctx, &AnalysisBundle{AnalysisID: "an-1"})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const bodyText = "upstream blew up"
			handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, bodyText, http.StatusInternalServerError)
			})
			client, _ := newTestClient(t, handler)
			err := tt.invoke(context.Background(), client)
			require.Error(t, err)
			assert.Contains(t, err.Error(), bodyText, "error should propagate response body")
			assert.Contains(t, err.Error(), "500", "error should propagate status code")
		})
	}
}

func TestListingQueryParams(t *testing.T) {
	var gotRaw string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRaw = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"deployments":[],"pods":[],"configMaps":[],"services":[],"ingresses":[],"routes":[]}`)
	})
	client, _ := newTestClient(t, handler)

	params := url.Values{}
	params.Set("subdomain", "foo")
	params.Set("user-id", "u-42")
	_, err := client.Listing(context.Background(), params)
	require.NoError(t, err)

	// url.Values.Encode() orders keys lexically, so the server-observed
	// query string must match.
	assert.Equal(t, params.Encode(), gotRaw)
}

func TestLogsQueryParams(t *testing.T) {
	var gotQuery url.Values
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	})
	client, _ := newTestClient(t, handler)

	params := url.Values{}
	params.Set("tail-lines", "100")
	params.Set("previous", "true")
	_, err := client.Logs(context.Background(), "an-1", params)
	require.NoError(t, err)

	assert.Equal(t, "100", gotQuery.Get("tail-lines"))
	assert.Equal(t, "true", gotQuery.Get("previous"))
}

func TestClientContextCancel(t *testing.T) {
	// Handler blocks until the request's context is cancelled, then exits
	// without writing a response — the client should see the cancellation
	// and return an error rather than hanging on the read.
	handler := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	})
	client, _ := newTestClient(t, handler)

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel shortly after dispatch so the request is definitely in flight.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	_, err := client.Status(ctx, "an-1")
	require.Error(t, err)
	assert.True(t,
		errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "context canceled"),
		"expected context-cancelled error, got %v", err,
	)
}

func TestLaunchReturnsCapacityExhaustedOn409(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "at capacity", http.StatusConflict)
	})
	client, _ := newTestClient(t, handler)

	err := client.Launch(context.Background(), &AnalysisBundle{AnalysisID: "an-1"})
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCapacityExhausted), "expected ErrCapacityExhausted sentinel, got %v", err)
}

func TestClientSendsNoAuthHeaderWithoutTokenSource(t *testing.T) {
	var sawAuth bool
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			sawAuth = true
		}
		w.WriteHeader(http.StatusOK)
	})
	client, _ := newTestClient(t, handler)

	require.NoError(t, client.Exit(context.Background(), "an-1"))
	assert.False(t, sawAuth, "no oauth2 source configured, so no Authorization header should be sent")
}
