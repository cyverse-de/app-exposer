package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/cyverse-de/app-exposer/operator"
	"github.com/labstack/echo/v4"
)

// wantsHTML reports whether the request prefers an HTML response. Browsers send
// an Accept header containing text/html; API clients send application/json, */*,
// or no Accept header at all (e.g. operatorclient), none of which match.
func wantsHTML(c echo.Context) bool {
	return strings.Contains(c.Request().Header.Get(echo.HeaderAccept), echo.MIMETextHTML)
}

// errorCodeMessage extracts the status code and a user-facing message from an
// error, mirroring Echo's own DefaultHTTPErrorHandler so the HTML and JSON
// branches agree on what to report.
func errorCodeMessage(err error) (int, string) {
	code, msg := http.StatusInternalServerError, ""
	var he *echo.HTTPError
	if errors.As(err, &he) {
		code = he.Code
		if s, ok := he.Message.(string); ok {
			msg = s
		} else if he.Message != nil {
			msg = fmt.Sprint(he.Message)
		}
	}
	if msg == "" {
		msg = http.StatusText(code)
	}
	return code, msg
}

// htmlErrorHandler renders a styled HTML error page for browser requests and
// delegates everything else to Echo's default handler, so the JSON API contract
// for machine clients is unchanged.
func htmlErrorHandler(fallback echo.HTTPErrorHandler) echo.HTTPErrorHandler {
	return func(err error, c echo.Context) {
		if c.Response().Committed || c.Request().Method == http.MethodHead || !wantsHTML(c) {
			fallback(err, c)
			return
		}
		code, msg := errorCodeMessage(err)
		html, rerr := operator.ErrorPageHTML(code, msg)
		if rerr != nil {
			log.Errorf("rendering error page: %v", rerr)
			fallback(err, c)
			return
		}
		if werr := c.HTML(code, html); werr != nil {
			log.Errorf("writing error page: %v", werr)
		}
	}
}
