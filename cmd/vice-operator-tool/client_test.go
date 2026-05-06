package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testClient(t *testing.T, handler http.Handler) *OperatorClient {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	return NewOperatorClient(u, srv.Client())
}

func TestAddOperator(t *testing.T) {
	createdID := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	tests := []struct {
		name       string
		req        *operatorclient.OperatorConfig
		statusCode int
		respBody   string
		wantErr    bool
		wantName   string
		wantID     uuid.UUID
		// validate is called with the decoded request body when provided,
		// allowing individual cases to assert on fields sent to the server.
		validate func(t *testing.T, got operatorclient.OperatorConfig)
	}{
		{
			name: "success",
			req: &operatorclient.OperatorConfig{
				Name: "cluster-a",
				URL:  "https://op-a.example.com",
			},
			statusCode: http.StatusCreated,
			respBody:   `{"id":"33333333-3333-3333-3333-333333333333","name":"cluster-a","url":"https://op-a.example.com","tls_skip_verify":false,"priority":0}`,
			wantName:   "cluster-a",
			wantID:     createdID,
		},
		{
			// Verify that all fields (including TLSSkipVerify) are sent correctly in the request body.
			name: "sends all fields in request body",
			req: &operatorclient.OperatorConfig{
				Name:          "op-1",
				URL:           "https://op.example.com",
				TLSSkipVerify: true,
				Priority:      5,
			},
			statusCode: http.StatusCreated,
			// The handler echos back the decoded request, so respBody is built dynamically via validate.
			wantName: "op-1",
			wantID:   createdID,
			validate: func(t *testing.T, got operatorclient.OperatorConfig) {
				t.Helper()
				assert.Equal(t, "op-1", got.Name)
				assert.Equal(t, "https://op.example.com", got.URL)
				assert.True(t, got.TLSSkipVerify)
				assert.Equal(t, 5, got.Priority)
			},
		},
		{
			name: "missing required field returns error",
			req: &operatorclient.OperatorConfig{
				Name: "cluster-b",
			},
			statusCode: http.StatusBadRequest,
			respBody:   `{"message":"url is required"}`,
			wantErr:    true,
		},
		{
			name: "server error",
			req: &operatorclient.OperatorConfig{
				Name: "cluster-c",
				URL:  "https://op-c.example.com",
			},
			statusCode: http.StatusInternalServerError,
			respBody:   `{"message":"database connection lost"}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/vice/admin/operators", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				var got operatorclient.OperatorConfig
				if err := json.NewDecoder(r.Body).Decode(&got); err == nil && tt.validate != nil {
					tt.validate(t, got)
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)

				if tt.respBody != "" {
					_, _ = w.Write([]byte(tt.respBody)) //nolint:errcheck // test server write
				} else if tt.validate != nil {
					// Echo the request fields back as the admin summary
					// (id + four config fields) so the client can decode it.
					_ = json.NewEncoder(w).Encode(operatorclient.OperatorAdminSummary{ //nolint:errcheck // test server write
						ID:            createdID,
						Name:          tt.req.Name,
						URL:           tt.req.URL,
						TLSSkipVerify: tt.req.TLSSkipVerify,
						Priority:      tt.req.Priority,
					})
				}
			})

			client := testClient(t, handler)
			summary, err := client.AddOperator(context.Background(), tt.req)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, summary)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantName, summary.Name)
				assert.Equal(t, tt.wantID, summary.ID)
			}
		})
	}
}

func TestListOperators(t *testing.T) {
	idA := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	idB := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	tests := []struct {
		name       string
		statusCode int
		respBody   string
		wantErr    bool
		wantLen    int
		// validate runs on the decoded slice when no error is expected,
		// allowing per-case assertions (e.g., that id decodes correctly).
		validate func(t *testing.T, ops []operatorclient.OperatorAdminSummary)
	}{
		{
			name:       "populated list with ids",
			statusCode: http.StatusOK,
			respBody:   `[{"id":"11111111-1111-1111-1111-111111111111","name":"a","url":"https://a.example.com","tls_skip_verify":false,"priority":0},{"id":"22222222-2222-2222-2222-222222222222","name":"b","url":"https://b.example.com","tls_skip_verify":true,"priority":10}]`,
			wantLen:    2,
			validate: func(t *testing.T, ops []operatorclient.OperatorAdminSummary) {
				t.Helper()
				assert.Equal(t, idA, ops[0].ID)
				assert.Equal(t, idB, ops[1].ID)
			},
		},
		{
			name:       "empty list",
			statusCode: http.StatusOK,
			respBody:   `[]`,
			wantLen:    0,
		},
		{
			name:       "server error",
			statusCode: http.StatusInternalServerError,
			respBody:   `{"message":"internal error"}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/vice/admin/operators", r.URL.Path)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.respBody)) //nolint:errcheck // test server write
			})

			client := testClient(t, handler)
			ops, err := client.ListOperators(context.Background())

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Len(t, ops, tt.wantLen)
				if tt.validate != nil {
					tt.validate(t, ops)
				}
			}
		})
	}
}

func TestUpdateOperator(t *testing.T) {
	id := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	newName := "renamed"
	newURL := "https://new.example.com"
	newSkip := true
	newPriority := 7

	tests := []struct {
		name       string
		req        *UpdateOperatorRequest
		statusCode int
		respBody   string
		wantErr    bool
		// validate inspects the decoded request body so individual cases can
		// assert on which fields were sent (omitted nil pointers should not
		// appear in the JSON thanks to omitempty).
		validate func(t *testing.T, raw []byte, decoded UpdateOperatorRequest)
	}{
		{
			name: "single-field rename",
			req:  &UpdateOperatorRequest{Name: &newName},
			validate: func(t *testing.T, raw []byte, decoded UpdateOperatorRequest) {
				t.Helper()
				require.NotNil(t, decoded.Name)
				assert.Equal(t, "renamed", *decoded.Name)
				assert.Nil(t, decoded.URL)
				assert.Nil(t, decoded.TLSSkipVerify)
				assert.Nil(t, decoded.Priority)
				assert.NotContains(t, string(raw), "url")
				assert.NotContains(t, string(raw), "tls_skip_verify")
				assert.NotContains(t, string(raw), "priority")
			},
			statusCode: http.StatusOK,
			respBody:   `{"name":"renamed","url":"https://orig.example.com","tls_skip_verify":false,"priority":0}`,
		},
		{
			name: "all fields",
			req: &UpdateOperatorRequest{
				Name:          &newName,
				URL:           &newURL,
				TLSSkipVerify: &newSkip,
				Priority:      &newPriority,
			},
			validate: func(t *testing.T, raw []byte, decoded UpdateOperatorRequest) {
				t.Helper()
				require.NotNil(t, decoded.Name)
				require.NotNil(t, decoded.URL)
				require.NotNil(t, decoded.TLSSkipVerify)
				require.NotNil(t, decoded.Priority)
				assert.Equal(t, "renamed", *decoded.Name)
				assert.Equal(t, "https://new.example.com", *decoded.URL)
				assert.True(t, *decoded.TLSSkipVerify)
				assert.Equal(t, 7, *decoded.Priority)
			},
			statusCode: http.StatusOK,
			respBody:   `{"name":"renamed","url":"https://new.example.com","tls_skip_verify":true,"priority":7}`,
		},
		{
			name:       "404 not found",
			req:        &UpdateOperatorRequest{Priority: &newPriority},
			statusCode: http.StatusNotFound,
			respBody:   `{"message":"operator not found"}`,
			wantErr:    true,
		},
		{
			name:       "409 conflict on rename collision",
			req:        &UpdateOperatorRequest{Name: &newName},
			statusCode: http.StatusConflict,
			respBody:   `{"message":"operator with that name or url already exists"}`,
			wantErr:    true,
		},
		{
			name:       "400 bad request on validation failure",
			req:        &UpdateOperatorRequest{URL: strPtr("not-a-url")},
			statusCode: http.StatusBadRequest,
			respBody:   `{"message":"url must be a valid HTTP(S) URL"}`,
			wantErr:    true,
		},
	}

	wantPath := "/vice/admin/operators/id/" + id.String()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPatch, r.Method)
				assert.Equal(t, wantPath, r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				if tt.validate != nil {
					raw, err := readAllAndRestore(r)
					require.NoError(t, err)
					var decoded UpdateOperatorRequest
					require.NoError(t, json.Unmarshal(raw, &decoded))
					tt.validate(t, raw, decoded)
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)
				if tt.respBody != "" {
					_, _ = w.Write([]byte(tt.respBody)) //nolint:errcheck // test server write
				}
			})

			client := testClient(t, handler)
			summary, err := client.UpdateOperator(context.Background(), id, tt.req)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, summary)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, summary)
			}
		})
	}
}

// strPtr returns a pointer to the given string. Used to build pointer-typed
// fields concisely in test table entries.
func strPtr(s string) *string { return &s }

// readAllAndRestore drains r.Body so the test can both inspect the raw
// JSON and decode it. The body is replaced with a fresh reader so any
// subsequent reader sees the same bytes.
func readAllAndRestore(r *http.Request) ([]byte, error) {
	buf, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	_ = r.Body.Close() //nolint:errcheck // test request body
	r.Body = io.NopCloser(bytes.NewReader(buf))
	return buf, nil
}

func TestDeleteOperator(t *testing.T) {
	idA := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	idMissing := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	idFKBlocked := uuid.MustParse("cccccccc-cccc-cccc-cccc-cccccccccccc")

	tests := []struct {
		name       string
		id         uuid.UUID
		statusCode int
		respBody   string
		wantErr    bool
	}{
		{
			name:       "success",
			id:         idA,
			statusCode: http.StatusOK,
		},
		{
			// The DELETE endpoint stays idempotent at the API layer:
			// deleting a non-existent UUID returns 200. The CLI prevents
			// missing-name calls earlier (resolveOperatorID errors), but
			// programmatic clients calling DeleteOperator directly with
			// a stale id still see the silent-success behavior.
			name:       "idempotent delete of missing operator",
			id:         idMissing,
			statusCode: http.StatusOK,
		},
		{
			name:       "server error (FK constraint blocks delete)",
			id:         idFKBlocked,
			statusCode: http.StatusInternalServerError,
			respBody:   `{"message":"failed to delete operator"}`,
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantPath := "/vice/admin/operators/id/" + tt.id.String()
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodDelete, r.Method)
				assert.Equal(t, wantPath, r.URL.Path)

				w.WriteHeader(tt.statusCode)
				if tt.respBody != "" {
					_, _ = w.Write([]byte(tt.respBody)) //nolint:errcheck // test server write
				}
			})

			client := testClient(t, handler)
			err := client.DeleteOperator(context.Background(), tt.id)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
