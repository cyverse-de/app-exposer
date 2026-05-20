package operator

import (
	"embed"
	"html/template"
)

//go:embed templates
var templateFS embed.FS

// mustParsePage parses a top-level page template together with the shared
// partials in templates/partials. The page is listed first so the returned
// template's name matches it, letting callers use Execute directly.
func mustParsePage(page string) *template.Template {
	return template.Must(template.ParseFS(templateFS, "templates/"+page, "templates/partials/*.html"))
}

// Page templates are parsed once at init time rather than on every request.
// Each shares the appbar, footer, and theme defined in templates/partials.
var (
	loadingTemplate = mustParsePage("loading.html")
	waitingTemplate = mustParsePage("waiting.html")
	landingTemplate = mustParsePage("landing.html")
)
