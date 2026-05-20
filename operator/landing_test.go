package operator

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleLandingPage(t *testing.T) {
	// The landing page is static, but it pulls the appbar logo, footer, and
	// theme from the shared partials. Asserting on a marker from each confirms
	// the partials parsed and rendered, not just that the page returned 200.
	wantContains := []string{
		"VICE Operator",                                  // page heading
		"API Documentation",                              // docs link text
		"/docs/index.html",                               // docs link target
		"Powered by CyVerse at The University of Arizona", // footer partial
		`viewBox="0 0 157.37 37.79"`,                      // brand/logo partial
	}

	op := &Operator{}

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, op.HandleLandingPage(c))
	assert.Equal(t, http.StatusOK, rec.Code)

	body := rec.Body.String()
	for _, s := range wantContains {
		assert.Contains(t, body, s)
	}
}
