package httphandlers

import (
	"net/http"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

// TestValidateOperatorFields covers the present/absent matrix for the
// validation helper used by both create and update. Empty/whitespace and
// non-HTTP(S) URLs must return 400; nil pointers must short-circuit so a
// PATCH that omits a field doesn't fabricate a validation error.
func TestValidateOperatorFields(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	tests := []struct {
		name    string
		nameArg *string
		urlArg  *string
		wantErr bool
		// wantStatus is checked only when wantErr is true.
		wantStatus int
	}{
		{
			name:    "both nil short-circuits (partial update with neither field)",
			nameArg: nil,
			urlArg:  nil,
			wantErr: false,
		},
		{
			name:    "valid name only",
			nameArg: strPtr("cluster-a"),
			urlArg:  nil,
			wantErr: false,
		},
		{
			name:    "valid url only",
			nameArg: nil,
			urlArg:  strPtr("https://op.example.com"),
			wantErr: false,
		},
		{
			name:    "valid name and url",
			nameArg: strPtr("cluster-a"),
			urlArg:  strPtr("https://op.example.com"),
			wantErr: false,
		},
		{
			name:       "empty name rejected",
			nameArg:    strPtr(""),
			urlArg:     nil,
			wantErr:    true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "whitespace name rejected (matches table CHECK constraint)",
			nameArg:    strPtr("   "),
			urlArg:     nil,
			wantErr:    true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty url rejected",
			nameArg:    nil,
			urlArg:     strPtr(""),
			wantErr:    true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "whitespace url rejected",
			nameArg:    nil,
			urlArg:     strPtr("   "),
			wantErr:    true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "non-HTTP scheme rejected",
			nameArg:    nil,
			urlArg:     strPtr("ftp://op.example.com"),
			wantErr:    true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing host rejected",
			nameArg:    nil,
			urlArg:     strPtr("https://"),
			wantErr:    true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:    "http scheme accepted",
			nameArg: nil,
			urlArg:  strPtr("http://op.example.com"),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateOperatorFields(tt.nameArg, tt.urlArg)
			if tt.wantErr {
				if assert.Error(t, err) {
					httpErr, ok := err.(*echo.HTTPError)
					if assert.True(t, ok, "expected *echo.HTTPError, got %T", err) {
						assert.Equal(t, tt.wantStatus, httpErr.Code)
					}
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
