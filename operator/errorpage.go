package operator

import (
	"net/http"
	"strings"
)

// errorPageData is the template data for the styled HTML error page.
type errorPageData struct {
	Code    int
	Title   string // http.StatusText(code), e.g. "Forbidden"
	Message string // specific message; blank when it would only duplicate Title
}

// ErrorPageHTML renders the branded error page for the given status code and
// message. The caller is responsible for writing it with the matching status
// code. The message is dropped when it merely repeats the status text so the
// heading and body don't say the same thing twice.
func ErrorPageHTML(code int, message string) (string, error) {
	title := http.StatusText(code)
	if message == title {
		message = ""
	}
	var buf strings.Builder
	if err := errorTemplate.Execute(&buf, errorPageData{Code: code, Title: title, Message: message}); err != nil {
		return "", err
	}
	return buf.String(), nil
}
