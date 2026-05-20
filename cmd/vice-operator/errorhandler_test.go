package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

func TestHTMLErrorHandler(t *testing.T) {
	tests := []struct {
		name            string
		accept          string
		method          string
		err             error
		wantStatus      int
		wantContentType string // substring match
		wantContains    []string
		wantEmptyBody   bool
	}{
		{
			name:            "browser GET renders styled HTML",
			accept:          "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			method:          http.MethodGet,
			err:             echo.NewHTTPError(http.StatusForbidden, "insufficient privileges"),
			wantStatus:      http.StatusForbidden,
			wantContentType: echo.MIMETextHTML,
			wantContains:    []string{"403", "Forbidden", "insufficient privileges", "Powered by CyVerse at The University of Arizona"},
		},
		{
			name:            "json client keeps JSON",
			accept:          echo.MIMEApplicationJSON,
			method:          http.MethodGet,
			err:             echo.NewHTTPError(http.StatusForbidden, "insufficient privileges"),
			wantStatus:      http.StatusForbidden,
			wantContentType: echo.MIMEApplicationJSON,
			wantContains:    []string{`"message":"insufficient privileges"`},
		},
		{
			name:            "empty Accept keeps JSON",
			accept:          "",
			method:          http.MethodGet,
			err:             echo.NewHTTPError(http.StatusBadRequest, "invalid state"),
			wantStatus:      http.StatusBadRequest,
			wantContentType: echo.MIMEApplicationJSON,
			wantContains:    []string{`"message":"invalid state"`},
		},
		{
			name:          "HEAD with text/html sends no body",
			accept:        echo.MIMETextHTML,
			method:        http.MethodHead,
			err:           echo.NewHTTPError(http.StatusNotFound, "nope"),
			wantStatus:    http.StatusNotFound,
			wantEmptyBody: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := echo.New()
			handler := htmlErrorHandler(e.HTTPErrorHandler)

			req := httptest.NewRequest(tt.method, "/", nil)
			if tt.accept != "" {
				req.Header.Set(echo.HeaderAccept, tt.accept)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler(tt.err, c)

			assert.Equal(t, tt.wantStatus, rec.Code)
			if tt.wantContentType != "" {
				assert.Contains(t, rec.Header().Get(echo.HeaderContentType), tt.wantContentType)
			}
			body := rec.Body.String()
			if tt.wantEmptyBody {
				assert.Empty(t, body)
			}
			for _, s := range tt.wantContains {
				assert.Contains(t, body, s)
			}
		})
	}
}
