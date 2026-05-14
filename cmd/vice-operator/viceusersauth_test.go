package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/cyverse-de/go-mod/viceauth"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsAllowedHost(t *testing.T) {
	cfg := &ViceUsersAuthConfig{BaseDomain: "cyverse.run"}
	tests := []struct {
		host string
		want bool
	}{
		{"a1234abcd.cyverse.run", true},
		{"cyverse.run", false},     // base domain itself, no subdomain
		{"a.b.cyverse.run", false}, // nested subdomain
		{"a1234.cyverse.run.evil.com", false},
		{"xcyverse.run", false}, // suffix without the dot boundary
		{"evil.com", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			assert.Equal(t, tt.want, cfg.isAllowedHost(tt.host))
		})
	}
}

// runCallback invokes the callback handler against a synthetic request and
// returns the recorder. echo.HTTPError results are run through the default
// error handler so the recorder reflects the status the client would see.
func runCallback(t *testing.T, cfg *ViceUsersAuthConfig, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/vice-users/callback?"+rawQuery, nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	if err := handleViceUsersCallback(cfg)(c); err != nil {
		e.HTTPErrorHandler(err, c)
	}
	return rec
}

func TestHandleViceUsersCallback(t *testing.T) {
	codec := viceauth.NewCodec([]byte("test-secret"))
	cfg := &ViceUsersAuthConfig{StateCodec: codec, BaseDomain: "cyverse.run"}

	state := func(t *testing.T, c *viceauth.Codec, origin string) string {
		t.Helper()
		s, err := c.Encode(viceauth.StateClaims{StateID: "sid", Origin: origin})
		require.NoError(t, err)
		return s
	}

	validState := state(t, codec, "https://a1234abcd.cyverse.run:4343/foo?x=1")

	tests := []struct {
		name     string
		query    url.Values
		wantCode int
	}{
		{
			name:     "valid bounce",
			query:    url.Values{"code": {"abc"}, "state": {validState}},
			wantCode: http.StatusTemporaryRedirect,
		},
		{
			name:     "keycloak error param",
			query:    url.Values{"error": {"access_denied"}, "error_description": {"nope"}},
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "missing code",
			query:    url.Values{"state": {validState}},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing state",
			query:    url.Values{"code": {"abc"}},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "bad signature",
			query:    url.Values{"code": {"abc"}, "state": {state(t, viceauth.NewCodec([]byte("other-secret")), "https://a1234abcd.cyverse.run/")}},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "off-domain origin",
			query:    url.Values{"code": {"abc"}, "state": {state(t, codec, "https://evil.com/")}},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "nested subdomain origin",
			query:    url.Values{"code": {"abc"}, "state": {state(t, codec, "https://a.b.cyverse.run/")}},
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "non-https origin",
			query:    url.Values{"code": {"abc"}, "state": {state(t, codec, "http://a1234abcd.cyverse.run/")}},
			wantCode: http.StatusBadRequest,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := runCallback(t, cfg, tt.query.Encode())
			require.Equal(t, tt.wantCode, rec.Code)

			if tt.wantCode != http.StatusTemporaryRedirect {
				return
			}
			// On a successful bounce the browser is sent back to the app's
			// own URL with code + state re-attached and the original query
			// preserved.
			loc, err := url.Parse(rec.Header().Get("Location"))
			require.NoError(t, err)
			assert.Equal(t, "https", loc.Scheme)
			assert.Equal(t, "a1234abcd.cyverse.run:4343", loc.Host)
			assert.Equal(t, "/foo", loc.Path)
			assert.Equal(t, "abc", loc.Query().Get("code"))
			assert.Equal(t, validState, loc.Query().Get("state"))
			assert.Equal(t, "1", loc.Query().Get("x"))
		})
	}
}
