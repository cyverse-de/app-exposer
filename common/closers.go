package common

import (
	"database/sql"
	"io"
	"net/http"
)

// LogClose closes c and logs any error at warn level with the supplied label
// for context. Intended for `defer common.LogClose("label", c)` patterns where
// the close error is diagnostic rather than control-flow. Prefer a named
// helper below when one exists; this is the fallback for arbitrary closers.
func LogClose(label string, c io.Closer) {
	if err := c.Close(); err != nil {
		Log.Warnf("closing %s: %v", label, err)
	}
}

// CloseBody closes an HTTP response body and logs any error. Safe to call
// via `defer common.CloseBody(resp)` after a successful http.Client.Do.
func CloseBody(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	LogClose("response body", resp.Body)
}

// CloseRows closes a *sql.Rows and logs any error. Meant for
// `defer common.CloseRows(rows)` in query-iteration paths where swallowing
// a Close error obscures driver-level problems like broken connections.
func CloseRows(rows *sql.Rows) {
	if rows == nil {
		return
	}
	LogClose("sql rows", rows)
}
