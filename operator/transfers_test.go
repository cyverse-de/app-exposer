package operator

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// rewriteTransport redirects every outbound request to target, preserving
// path and query. Lets the transfer client talk to an httptest.Server
// instead of the in-cluster sidecar hostname the production code builds.
type rewriteTransport struct {
	target *url.URL
}

func (r *rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme = r.target.Scheme
	req.URL.Host = r.target.Host
	return http.DefaultTransport.RoundTrip(req)
}

// triggerFileTransferFixture wires the package-level transferHTTPClient
// at a test server and disables real sleeping between polls. Returns a
// cleanup that both tears down the test server and restores the package
// state so other tests observe production behavior.
func triggerFileTransferFixture(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	srvURL, err := url.Parse(srv.URL)
	require.NoError(t, err)

	prevClient := transferHTTPClient
	prevSleep := pollSleep
	transferHTTPClient = &http.Client{
		Transport: &rewriteTransport{target: srvURL},
		Timeout:   5 * time.Second,
	}
	// No-op sleep that still honors ctx cancellation so the "context
	// canceled mid-loop" test path stays exercised.
	pollSleep = func(ctx context.Context, _ time.Duration) bool {
		if err := ctx.Err(); err != nil {
			return false
		}
		return true
	}

	t.Cleanup(func() {
		transferHTTPClient = prevClient
		pollSleep = prevSleep
		srv.Close()
	})
	return srv
}

// createTransferService registers a Service the transfer code will find
// via its analysis-id label and use to build the sidecar URL.
func createTransferService(t *testing.T, op *Operator, analysisID, svcName string) {
	t.Helper()
	_, err := op.clientset.CoreV1().Services(op.namespace).Create(context.Background(), &apiv1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcName,
			Namespace: op.namespace,
			Labels:    map[string]string{"analysis-id": analysisID},
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
}

func TestTriggerFileTransferMissingService(t *testing.T) {
	// No Service for the analysis — transfer code should surface a
	// descriptive error without hitting the HTTP layer.
	op, _, _ := newTestOperator(t, 10)

	err := op.triggerFileTransfer(context.Background(), "an-missing", "/upload")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no service found")
}

func TestTriggerFileTransferCompletedFirstPoll(t *testing.T) {
	// Happy path: POST returns a transfer UUID with status "in_progress",
	// first subsequent GET returns "completed". The loop should exit
	// after one poll iteration.
	var pollCount int32
	handler := http.NewServeMux()
	handler.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `{"uuid":"xfer-1","status":"in_progress"}`)
			return
		}
		http.NotFound(w, r)
	})
	handler.HandleFunc("/upload/xfer-1", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&pollCount, 1)
		_, _ = io.WriteString(w, `{"uuid":"xfer-1","status":"completed"}`)
	})

	triggerFileTransferFixture(t, handler)

	op, _, _ := newTestOperator(t, 10)
	createTransferService(t, op, "an-ok", "svc-ok")

	err := op.triggerFileTransfer(context.Background(), "an-ok", "/upload")
	require.NoError(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&pollCount), "should exit the loop after one completed poll")
}

func TestTriggerFileTransferFailedStatus(t *testing.T) {
	// Sidecar reports status:"failed" — the loop exits and an error is
	// returned naming the failed analysis so callers distinguish this
	// from a hang or network fault.
	handler := http.NewServeMux()
	handler.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `{"uuid":"xfer-2","status":"in_progress"}`)
			return
		}
		http.NotFound(w, r)
	})
	handler.HandleFunc("/upload/xfer-2", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"uuid":"xfer-2","status":"failed"}`)
	})

	triggerFileTransferFixture(t, handler)

	op, _, _ := newTestOperator(t, 10)
	createTransferService(t, op, "an-fail", "svc-fail")

	err := op.triggerFileTransfer(context.Background(), "an-fail", "/upload")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file transfer failed for analysis an-fail")
}

func TestTriggerFileTransferContextCancel(t *testing.T) {
	// Sidecar never completes. The loop must observe ctx.Done() via
	// pollSleep and return an error that carries ctx.Err().
	handler := http.NewServeMux()
	handler.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `{"uuid":"xfer-3","status":"in_progress"}`)
			return
		}
		http.NotFound(w, r)
	})
	handler.HandleFunc("/upload/xfer-3", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"uuid":"xfer-3","status":"in_progress"}`)
	})

	triggerFileTransferFixture(t, handler)

	op, _, _ := newTestOperator(t, 10)
	createTransferService(t, op, "an-cancel", "svc-cancel")

	// Start the loop in the background so we can cancel it.
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- op.triggerFileTransfer(ctx, "an-cancel", "/upload")
	}()
	// Let a few poll iterations run, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		require.Error(t, err)
		assert.Contains(t, err.Error(), "canceled", "error must indicate cancellation, got: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("triggerFileTransfer did not return after context cancel")
	}
}

func TestTriggerFileTransferMalformedStatusJSON(t *testing.T) {
	// Sidecar returns garbage for a status poll. Loop must exit with a
	// descriptive decode error rather than wedging on the bad response.
	var pollCount int32
	handler := http.NewServeMux()
	handler.HandleFunc("/upload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `{"uuid":"xfer-4","status":"in_progress"}`)
			return
		}
		http.NotFound(w, r)
	})
	handler.HandleFunc("/upload/xfer-4", func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&pollCount, 1)
		_, _ = io.WriteString(w, `not-json`)
	})

	triggerFileTransferFixture(t, handler)

	op, _, _ := newTestOperator(t, 10)
	createTransferService(t, op, "an-bad", "svc-bad")

	err := op.triggerFileTransfer(context.Background(), "an-bad", "/upload")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unmarshalling")
	assert.Equal(t, int32(1), atomic.LoadInt32(&pollCount), "should not keep polling after a decode failure")
}

func TestTriggerFileTransferInitialRequestFailure(t *testing.T) {
	// The initial POST that starts the transfer fails with a non-2xx
	// status. The loop must never start — the error must surface
	// immediately with the HTTP status baked in.
	handler := http.NewServeMux()
	handler.HandleFunc("/upload", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "sidecar broken", http.StatusInternalServerError)
	})
	triggerFileTransferFixture(t, handler)

	op, _, _ := newTestOperator(t, 10)
	createTransferService(t, op, "an-500", "svc-500")

	err := op.triggerFileTransfer(context.Background(), "an-500", "/upload")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transfer request returned 500")
}

// Silence the unused-import linter guard for strings if the file is
// edited in the future to not need it.
var _ = strings.Builder{}
