package operator

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// HandleLandingPage serves the operator's branded landing page at /. It is
// intentionally unauthenticated so it still doubles as the health check; the
// API-docs link it carries goes through the existing Swagger auth gate.
func (o *Operator) HandleLandingPage(c echo.Context) error {
	// Buffer the template output before writing so we can return a 500 if
	// rendering fails instead of writing a partial response body with a 200 header.
	var buf strings.Builder
	if err := landingTemplate.Execute(&buf, nil); err != nil {
		log.Errorf("rendering landing page: %v", err)
		return echo.NewHTTPError(http.StatusInternalServerError, "failed to render landing page")
	}
	return c.HTML(http.StatusOK, buf.String())
}
