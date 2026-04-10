package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cyverse-de/app-exposer/common"
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
	tests := []struct {
		name       string
		req        *AddOperatorRequest
		statusCode int
		respBody   string
		wantErr    bool
		wantName   string
		// validate is called with the decoded request body when provided,
		// allowing individual cases to assert on fields sent to the server.
		validate func(t *testing.T, got AddOperatorRequest)
	}{
		{
			name: "success",
			req: &AddOperatorRequest{
				Name:                  "cluster-a",
				URL:                   "https://op-a.example.com",
				AuthUser:              "admin",
				AuthPasswordEncrypted: "encrypted-pw",
			},
			statusCode: http.StatusCreated,
			respBody:   `{"name":"cluster-a","url":"https://op-a.example.com","tls_skip_verify":false}`,
			wantName:   "cluster-a",
		},
		{
			// Verify that all fields (including TLSSkipVerify) are sent correctly in the request body.
			name: "sends all fields in request body",
			req: &AddOperatorRequest{
				Name:                  "op-1",
				URL:                   "https://op.example.com",
				TLSSkipVerify:         true,
				AuthUser:              "admin",
				AuthPasswordEncrypted: "enc-secret",
			},
			statusCode: http.StatusCreated,
			// The handler echos back the decoded request, so respBody is built dynamically via validate.
			wantName: "op-1",
			validate: func(t *testing.T, got AddOperatorRequest) {
				t.Helper()
				assert.Equal(t, "op-1", got.Name)
				assert.Equal(t, "https://op.example.com", got.URL)
				assert.Equal(t, "admin", got.AuthUser)
				assert.Equal(t, "enc-secret", got.AuthPasswordEncrypted)
				assert.True(t, got.TLSSkipVerify)
			},
		},
		{
			name: "missing required field returns error",
			req: &AddOperatorRequest{
				Name: "cluster-b",
			},
			statusCode: http.StatusBadRequest,
			respBody:   `{"message":"url is required"}`,
			wantErr:    true,
		},
		{
			name: "server error",
			req: &AddOperatorRequest{
				Name:                  "cluster-c",
				URL:                   "https://op-c.example.com",
				AuthUser:              "admin",
				AuthPasswordEncrypted: "encrypted-pw",
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

				var got AddOperatorRequest
				if err := json.NewDecoder(r.Body).Decode(&got); err == nil && tt.validate != nil {
					tt.validate(t, got)
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.statusCode)

				if tt.respBody != "" {
					_, _ = w.Write([]byte(tt.respBody)) //nolint:errcheck // test server write
				} else if tt.validate != nil {
					// Echo the request fields back as the summary so the client can decode it.
					_ = json.NewEncoder(w).Encode(OperatorSummary{ //nolint:errcheck // test server write
						Name:          tt.req.Name,
						URL:           tt.req.URL,
						TLSSkipVerify: tt.req.TLSSkipVerify,
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
			}
		})
	}
}

func TestListOperators(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		respBody   string
		wantErr    bool
		wantLen    int
	}{
		{
			name:       "populated list",
			statusCode: http.StatusOK,
			respBody:   `[{"name":"a","url":"https://a.example.com","tls_skip_verify":false},{"name":"b","url":"https://b.example.com","tls_skip_verify":true}]`,
			wantLen:    2,
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
			}
		})
	}
}

func TestDeleteOperator(t *testing.T) {
	tests := []struct {
		name       string
		opName     string
		statusCode int
		respBody   string
		wantErr    bool
		wantPath   string
	}{
		{
			name:       "success",
			opName:     "cluster-a",
			statusCode: http.StatusOK,
			wantPath:   "/vice/admin/operators/name/cluster-a",
		},
		{
			name:       "idempotent delete of missing operator",
			opName:     "nonexistent",
			statusCode: http.StatusOK,
			wantPath:   "/vice/admin/operators/name/nonexistent",
		},
		{
			name:       "server error",
			opName:     "cluster-b",
			statusCode: http.StatusInternalServerError,
			respBody:   `{"message":"FK constraint"}`,
			wantErr:    true,
			wantPath:   "/vice/admin/operators/name/cluster-b",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodDelete, r.Method)
				assert.Equal(t, tt.wantPath, r.URL.Path)

				w.WriteHeader(tt.statusCode)
				if tt.respBody != "" {
					_, _ = w.Write([]byte(tt.respBody)) //nolint:errcheck // test server write
				}
			})

			client := testClient(t, handler)
			err := client.DeleteOperator(context.Background(), tt.opName)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGenerateKey(t *testing.T) {
	tests := []struct {
		name string
	}{
		{name: "first call"},
		{name: "second call"},
	}

	keys := make([]string, 0, len(tests))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key, err := GenerateKey()
			require.NoError(t, err)

			// Must be valid base64.
			decoded, err := base64.StdEncoding.DecodeString(key)
			require.NoError(t, err)

			// AES-256 requires exactly 32 bytes.
			assert.Len(t, decoded, aesKeySize)

			keys = append(keys, key)
		})
	}

	// Consecutive calls must produce distinct keys.
	if len(keys) == 2 {
		assert.NotEqual(t, keys[0], keys[1], "consecutive generated keys should differ")
	}
}

func TestGenerateKeyRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	require.NoError(t, err)

	plaintext := "s3cret-operator-password!"
	encrypted, err := common.Encrypt(plaintext, key)
	require.NoError(t, err)

	decrypted, err := common.Decrypt(encrypted, key)
	require.NoError(t, err)

	assert.Equal(t, plaintext, decrypted)
}
