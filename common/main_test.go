package common

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

// MockResponseWriter is a test implementation of http.ResponseWriter.
type MockResponseWriter struct {
	HeaderMap  http.Header
	Body       []byte
	StatusCode int
}

// Header returns the header map for the response.
func (w *MockResponseWriter) Header() http.Header {
	return w.HeaderMap
}

// Write updates the response body. All of the code that we're testing writes the entire response
// body at once, so this function will simply overwrite the existing contents.
func (w *MockResponseWriter) Write(bytes []byte) (int, error) {
	w.Body = bytes
	return len(bytes), nil
}

// WriteHeader records the response status code.
func (w *MockResponseWriter) WriteHeader(statusCode int) {
	w.StatusCode = statusCode
}

// VerifyStatusCode verifies that the correct status code was set.
func (w *MockResponseWriter) VerifyStatusCode(t *testing.T, expected int) {
	if w.StatusCode != expected {
		t.Errorf("status was %d, not %d", w.StatusCode, expected)
	}
}

// VerifyHeader verifies that a single-value HTTP header was set correctly.
func (w *MockResponseWriter) VerifyHeader(t *testing.T, headerName, expected string) {
	actual := w.Header().Get(headerName)
	if expected != actual {
		t.Errorf("%s header was %s, not %s", headerName, actual, expected)
	}
}

// VerifyBody verifies that the response body was set correctly.
func (w *MockResponseWriter) VerifyBody(t *testing.T, expected string) {
	actual := string(w.Body)
	if expected != actual {
		t.Errorf("unexpected response body: %s\nexpected: %s", actual, expected)
	}
}

// NewMockResponseWriter returns a new mock response writer.
func NewMockResponseWriter() *MockResponseWriter {
	return &MockResponseWriter{
		HeaderMap:  make(http.Header),
		Body:       nil,
		StatusCode: 0,
	}
}

func TestError(t *testing.T) {
	w := NewMockResponseWriter()
	Error(w, "something bad happened", http.StatusBadRequest)

	// Validate the response.
	w.VerifyStatusCode(t, http.StatusBadRequest)
	w.VerifyHeader(t, "Content-Type", "application/json")
	w.VerifyBody(t, ErrorResponse{Message: "something bad happened"}.Error())
}

func TestDetailedErrorWithPlainError(t *testing.T) {
	w := NewMockResponseWriter()
	e := fmt.Errorf("something bad happened")
	DetailedError(w, e, http.StatusInternalServerError)

	// Validate the response.
	w.VerifyStatusCode(t, http.StatusInternalServerError)
	w.VerifyHeader(t, "Content-Type", "application/json")
	w.VerifyBody(t, ErrorResponse{Message: e.Error()}.Error())
}

func TestDetailedError(t *testing.T) {
	w := NewMockResponseWriter()
	e := ErrorResponse{
		Message: "something bogus this way comes",
		Details: &map[string]interface{}{
			"bogosity_factor":     42,
			"max_bogosity_factor": 2,
		},
	}
	DetailedError(w, e, http.StatusBadRequest)

	// Validate the response.
	w.VerifyStatusCode(t, http.StatusBadRequest)
	w.VerifyHeader(t, "Content-Type", "application/json")
	w.VerifyBody(t, e.Error())
}

func TestDetailedErrorWithCode(t *testing.T) {
	w := NewMockResponseWriter()
	e := ErrorResponse{
		Message:   "something bogus this way comes",
		ErrorCode: "ERR_BOGOSITY_OVERLOAD",
		Details: &map[string]interface{}{
			"bogosity_factor":             42,
			"max_bogosity_factor":         nil,
			"default_max_bogosity_factor": 2,
		},
	}
	DetailedError(w, e, http.StatusBadRequest)

	// Validate the response.
	w.VerifyStatusCode(t, http.StatusBadRequest)
	w.VerifyHeader(t, "Content-Type", "application/json")
	w.VerifyBody(t, e.Error())
}

func TestFilterMap(t *testing.T) {
	t.Run("empty values returns empty map", func(t *testing.T) {
		result := FilterMap(url.Values{})
		assert.Empty(t, result)
	})

	t.Run("single value per key", func(t *testing.T) {
		values := url.Values{
			"foo": []string{"bar"},
			"baz": []string{"qux"},
		}
		result := FilterMap(values)
		assert.Equal(t, map[string]string{"foo": "bar", "baz": "qux"}, result)
	})

	t.Run("only first value is kept when multiple values present for a key", func(t *testing.T) {
		values := url.Values{
			"key": []string{"first", "second", "third"},
		}
		result := FilterMap(values)
		assert.Equal(t, map[string]string{"key": "first"}, result)
	})
}
