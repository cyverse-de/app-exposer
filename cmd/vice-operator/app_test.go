package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

// newFakeOIDCProvider stands up an httptest server that serves a minimal
// /.well-known/openid-configuration document and returns a real *oidc.Provider
// pointed at it. This lets tests exercise code paths that take a non-nil
// provider without hitting a real Keycloak.
func newFakeOIDCProvider(t *testing.T) *oidc.Provider {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"issuer": %q,
			"authorization_endpoint": %q,
			"token_endpoint": %q,
			"jwks_uri": %q
		}`, srv.URL, srv.URL+"/auth", srv.URL+"/token", srv.URL+"/jwks")
	}))
	t.Cleanup(srv.Close)

	provider, err := oidc.NewProvider(context.Background(), srv.URL)
	require.NoError(t, err)
	return provider
}

func TestSwaggerAuthConfigEnabled(t *testing.T) {
	tests := []struct {
		name string
		cfg  SwaggerAuthConfig
		want bool
	}{
		{"empty config", SwaggerAuthConfig{}, false},
		{"client ID only, no endpoint", SwaggerAuthConfig{ClientID: "c"}, false},
		{"endpoint only, no client ID", SwaggerAuthConfig{Endpoint: oauth2.Endpoint{AuthURL: "https://idp/auth", TokenURL: "https://idp/token"}}, false},
		{"both client ID and endpoint", SwaggerAuthConfig{ClientID: "c", Endpoint: oauth2.Endpoint{AuthURL: "https://idp/auth", TokenURL: "https://idp/token"}}, true},
		{"client ID with partial endpoint (no auth URL)", SwaggerAuthConfig{ClientID: "c", Endpoint: oauth2.Endpoint{TokenURL: "https://idp/token"}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.cfg.Enabled())
		})
	}
}

func TestGenerateCookieSecret(t *testing.T) {
	key, err := generateCookieSecret()
	require.NoError(t, err)
	assert.Len(t, key, 32, "cookie secret must be 32 bytes")

	// Two calls should produce different values (collision probability negligible).
	key2, err := generateCookieSecret()
	require.NoError(t, err)
	assert.NotEqual(t, key, key2)
}

func TestSignAndVerifyToken(t *testing.T) {
	key := []byte("test-secret-key-32-bytes-long!!!")
	tests := []struct {
		name      string
		token     string
		cookieKey []byte
		wantErr   bool
	}{
		{"valid token", "eyJhbGciOiJSUzI1NiJ9.payload.signature", key, false},
		{"empty token", "", key, false},
		{"wrong key", "some.jwt.token", []byte("different-key-32-bytes-long!!!!!"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signed := signToken(tt.token, key)
			got, err := verifyAndExtractToken(signed, tt.cookieKey)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.token, got)
			}
		})
	}
}

func TestVerifyAndExtractTokenMalformed(t *testing.T) {
	key := []byte("test-secret-key-32-bytes-long!!!")
	tests := []struct {
		name   string
		cookie string
	}{
		{"no dot separator", "nodot"},
		{"empty string", ""},
		{"tampered payload", "dGFtcGVyZWQ.invalidsignature"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := verifyAndExtractToken(tt.cookie, key)
			assert.Error(t, err)
		})
	}
}

func TestBuildSwaggerAuthConfigNilProvider(t *testing.T) {
	// When provider is nil (API auth disabled), the Swagger endpoint must
	// be zero-valued so Enabled() returns false even if a client ID is set.
	cfg, err := buildSwaggerAuthConfig(nil, "some-client", "secret", "cookie-secret")
	require.NoError(t, err)
	assert.False(t, cfg.Enabled(), "Enabled() must be false when provider is nil")
	assert.Empty(t, cfg.Endpoint.AuthURL)
	assert.Empty(t, cfg.Endpoint.TokenURL)
}

func TestBuildSwaggerAuthConfigNoClientID(t *testing.T) {
	// An empty client ID must disable the login flow regardless of provider.
	cfg, err := buildSwaggerAuthConfig(nil, "", "", "")
	require.NoError(t, err)
	assert.False(t, cfg.Enabled())
	assert.Nil(t, cfg.CookieSecret, "no cookie secret should be generated when client ID is absent")
}

func TestBuildSwaggerAuthConfigCookieSecret(t *testing.T) {
	provider := newFakeOIDCProvider(t)

	tests := []struct {
		name           string
		cookieSecretIn string
		assertSecret   func(t *testing.T, got []byte)
	}{
		{
			name:           "auto-generates when empty",
			cookieSecretIn: "",
			assertSecret: func(t *testing.T, got []byte) {
				t.Helper()
				assert.Len(t, got, 32, "auto-generated cookie secret should be 32 bytes")
			},
		},
		{
			name:           "uses provided secret as-is",
			cookieSecretIn: "explicit-secret-value",
			assertSecret: func(t *testing.T, got []byte) {
				t.Helper()
				assert.Equal(t, []byte("explicit-secret-value"), got)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := buildSwaggerAuthConfig(provider, "swagger-client", "client-secret", tt.cookieSecretIn)
			require.NoError(t, err)
			assert.True(t, cfg.Enabled(), "Enabled() must be true when provider and client ID are set")
			assert.Equal(t, provider.Endpoint().AuthURL, cfg.Endpoint.AuthURL)
			assert.Equal(t, provider.Endpoint().TokenURL, cfg.Endpoint.TokenURL)
			tt.assertSecret(t, cfg.CookieSecret)
		})
	}
}

func TestStripBearer(t *testing.T) {
	tests := []struct {
		name      string
		header    string
		wantToken string
		wantOK    bool
	}{
		{"empty header", "", "", false},
		{"missing scheme", "abc.def.ghi", "", false},
		{"missing space", "Bearerabc", "", false},
		{"canonical Bearer", "Bearer abc.def.ghi", "abc.def.ghi", true},
		{"lowercase bearer", "bearer abc.def.ghi", "abc.def.ghi", true},
		{"uppercase BEARER", "BEARER abc.def.ghi", "abc.def.ghi", true},
		{"mixed-case BeArEr", "BeArEr abc.def.ghi", "abc.def.ghi", true},
		{"non-Bearer scheme", "Basic abc.def.ghi", "", false},
		{"empty token after prefix", "Bearer ", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := stripBearer(tt.header)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantToken, got)
		})
	}
}
