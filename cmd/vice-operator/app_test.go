package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

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
