package operator

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoginPageHTML(t *testing.T) {
	authLink := "https://keycloak.example.org/auth?client_id=abc&redirect_uri=https%3A%2F%2Fop%2Fdocs%2Fcallback&state=xyz"

	html, err := LoginPageHTML(authLink)
	require.NoError(t, err)

	for _, s := range []string{
		"Log in",
		"Powered by CyVerse at The University of Arizona",
		`viewBox="0 0 157.37 37.79"`,
	} {
		assert.Contains(t, html, s)
	}

	// The auth link lands in the href, HTML-escaped (& -> &amp;) by the
	// attribute-context escaper.
	assert.Contains(t, html,
		`href="https://keycloak.example.org/auth?client_id=abc&amp;redirect_uri=https%3A%2F%2Fop%2Fdocs%2Fcallback&amp;state=xyz"`)
}
