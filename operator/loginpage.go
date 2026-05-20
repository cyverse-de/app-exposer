package operator

import "strings"

// loginPageData is the template data for the Swagger UI login page.
type loginPageData struct {
	AuthLink string
}

// LoginPageHTML renders the branded Swagger UI login page. authLink is the
// OIDC authorization URL the login button points at; html/template escapes it
// in the href context.
func LoginPageHTML(authLink string) (string, error) {
	var buf strings.Builder
	if err := loginTemplate.Execute(&buf, loginPageData{AuthLink: authLink}); err != nil {
		return "", err
	}
	return buf.String(), nil
}
