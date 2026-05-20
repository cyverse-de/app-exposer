package operator

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestErrorPageHTML(t *testing.T) {
	// Markers from each shared partial confirm the chrome rendered, not just
	// that a string came back.
	const (
		footer = "Powered by CyVerse at The University of Arizona"
		logo   = `viewBox="0 0 157.37 37.79"`
	)

	tests := []struct {
		name            string
		code            int
		message         string
		wantContains    []string
		wantNotContains []string
	}{
		{
			name:         "forbidden with specific message",
			code:         http.StatusForbidden,
			message:      "insufficient privileges",
			wantContains: []string{"403", "Forbidden", "<p>insufficient privileges</p>", footer, logo},
		},
		{
			name:            "message omitted when it only repeats the status text",
			code:            http.StatusNotFound,
			message:         "Not Found",
			wantContains:    []string{"404", "Not Found"},
			wantNotContains: []string{"<p>Not Found</p>"},
		},
		{
			name:            "generic 500",
			code:            http.StatusInternalServerError,
			message:         "Internal Server Error",
			wantContains:    []string{"500", "Internal Server Error"},
			wantNotContains: []string{"<p>Internal Server Error</p>"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			html, err := ErrorPageHTML(tt.code, tt.message)
			require.NoError(t, err)
			for _, s := range tt.wantContains {
				assert.Contains(t, html, s)
			}
			for _, s := range tt.wantNotContains {
				assert.NotContains(t, html, s)
			}
		})
	}
}
