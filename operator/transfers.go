package operator

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/labstack/echo/v4"
)

// fileTransfersPort is the port used by the file-transfer sidecar container.
const fileTransfersPort = int32(60001)

// Transfer-goroutine lifecycle bounds. maxTransferLifetime caps how long any
// single save/download/upload goroutine may live — if the sidecar stops
// responding the goroutine is terminated deterministically instead of
// lingering for the pod's remaining lifetime. pollInterval grows from
// initialPollInterval to maxPollInterval in pollIntervalStep increments,
// trading a small amount of tail latency for a linear reduction in
// request volume against the sidecar.
const (
	maxTransferLifetime = time.Hour
	initialPollInterval = 5 * time.Second
	maxPollInterval     = 15 * time.Second
	pollIntervalStep    = 5 * time.Second
)

// transferHTTPClient is used for requests to the file-transfer sidecar.
// It has a per-request timeout to prevent goroutines from blocking forever
// if the sidecar hangs or the connection stalls.
var transferHTTPClient = &http.Client{Timeout: 30 * time.Second}

// sleepCtx waits for d or until ctx is canceled. Returns true if d elapsed,
// false if ctx canceled first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// HandleSaveAndExit triggers the file transfer sidecar to upload outputs,
// then deletes all analysis resources.
//
//	@Summary		Save outputs and exit
//	@Description	Triggers the file-transfer sidecar to upload output files,
//	@Description	then deletes all K8s resources for the analysis. Runs asynchronously.
//	@Tags			transfers
//	@Param			analysis-id	path	string	true	"The analysis ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Router			/analyses/{analysis-id}/save-and-exit [post]
func (o *Operator) HandleSaveAndExit(c echo.Context) error {
	analysisID, err := requiredParam(c, "analysis-id")
	if err != nil {
		return err
	}

	log.Infof("save-and-exit requested for analysis %s", analysisID)

	// Run file transfer and cleanup asynchronously so the caller doesn't block.
	// A detached background context is used because the HTTP request context
	// will be cancelled once this handler returns, which would abort the
	// transfer; we bound it with a hard lifetime so a stuck sidecar can't
	// keep the goroutine alive indefinitely.
	//
	// TODO: surface upload failures back to analysis status. The handler has
	// already responded 200 by the time the goroutine runs, so the user has
	// no visibility into a failed transfer today beyond the log line below.
	// That's a larger change (separate notification path or analysis-level
	// status field) and is tracked separately.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), maxTransferLifetime)
		defer cancel()

		if err := o.triggerFileTransfer(bgCtx, analysisID, "/upload"); err != nil {
			log.Errorf("upload failed for analysis %s, proceeding with resource cleanup anyway: %v", analysisID, err)
		} else {
			log.Infof("upload complete for analysis %s, proceeding with cleanup", analysisID)
		}

		if err := o.deleteAnalysisResources(bgCtx, analysisID); err != nil {
			log.Errorf("cleanup failed for analysis %s: %v", analysisID, err)
		} else {
			log.Infof("cleanup complete for analysis %s", analysisID)
		}
	}()

	return c.NoContent(http.StatusOK)
}

// HandleDownloadInputFiles triggers the file-transfer sidecar to download
// input files for an analysis.
//
//	@Summary		Download input files
//	@Description	Triggers the file-transfer sidecar to download input files
//	@Description	for the analysis. Runs asynchronously.
//	@Tags			transfers
//	@Param			analysis-id	path	string	true	"The analysis ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Router			/analyses/{analysis-id}/download-input-files [post]
func (o *Operator) HandleDownloadInputFiles(c echo.Context) error {
	return o.handleAsyncTransfer(c, "download-input-files", "/download")
}

// HandleSaveOutputFiles triggers the file-transfer sidecar to upload
// output files for an analysis.
//
//	@Summary		Save output files
//	@Description	Triggers the file-transfer sidecar to upload output files
//	@Description	for the analysis. Runs asynchronously.
//	@Tags			transfers
//	@Param			analysis-id	path	string	true	"The analysis ID"
//	@Success		200
//	@Failure		400	{object}	common.ErrorResponse
//	@Router			/analyses/{analysis-id}/save-output-files [post]
func (o *Operator) HandleSaveOutputFiles(c echo.Context) error {
	return o.handleAsyncTransfer(c, "save-output-files", "/upload")
}

// handleAsyncTransfer validates the analysis-id param, starts a file transfer
// in a background goroutine, and returns 200 immediately. The caller (user)
// does not block on the transfer.
func (o *Operator) handleAsyncTransfer(c echo.Context, action, transferPath string) error {
	analysisID, err := requiredParam(c, "analysis-id")
	if err != nil {
		return err
	}

	log.Infof("%s requested for analysis %s", action, analysisID)

	// Same lifetime-bounding rationale as HandleSaveAndExit above: the HTTP
	// request context is gone once this handler returns, so we need a
	// fresh context, but it must have an upper bound so a stuck sidecar
	// can't leak a goroutine for the life of the pod.
	go func() {
		bgCtx, cancel := context.WithTimeout(context.Background(), maxTransferLifetime)
		defer cancel()

		if err := o.triggerFileTransfer(bgCtx, analysisID, transferPath); err != nil {
			log.Errorf("%s failed for %s: %v", action, analysisID, err)
		} else {
			log.Infof("%s succeeded for analysis %s", action, analysisID)
		}
	}()

	return c.NoContent(http.StatusOK)
}

// triggerFileTransfer finds the analysis Service by label and POSTs to the
// file-transfer sidecar to initiate a transfer, then polls until complete.
func (o *Operator) triggerFileTransfer(ctx context.Context, analysisID, reqpath string) error {
	opts := analysisLabelSelector(analysisID)
	svcClient := o.clientset.CoreV1().Services(o.namespace)
	svcList, err := svcClient.List(ctx, opts)
	if err != nil {
		return err
	}
	if len(svcList.Items) == 0 {
		return fmt.Errorf("no service found for analysis %s", analysisID)
	}

	svc := svcList.Items[0]
	svcURL := url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s.%s:%d", svc.Name, svc.Namespace, fileTransfersPort),
		Path:   reqpath,
	}

	// Request the transfer.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, svcURL.String(), nil)
	if err != nil {
		return fmt.Errorf("creating transfer request: %w", err)
	}
	resp, err := transferHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("requesting transfer: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("transfer request returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading transfer response: %w", err)
	}

	var xferResp struct {
		UUID   string `json:"uuid"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(body, &xferResp); err != nil {
		return fmt.Errorf("unmarshalling transfer response: %w", err)
	}

	log.Infof("file transfer started for analysis %s (uuid %s)", analysisID, xferResp.UUID)

	// Poll until the transfer finishes. The overall lifetime cap comes from
	// the caller's context (bounded by maxTransferLifetime at the goroutine
	// entry point); the per-poll interval grows from initialPollInterval
	// to maxPollInterval so a long transfer doesn't pound the sidecar with
	// 720 requests the way the old fixed-5s-cap loop did.
	pollInterval := initialPollInterval
	startTime := time.Now()
	lastLogged := startTime

	for xferResp.Status != "completed" && xferResp.Status != "failed" {
		// Context-aware sleep: if the goroutine's deadline fires or the
		// caller cancels, we bail out of the loop promptly instead of
		// finishing the current 5s sleep first.
		if !sleepCtx(ctx, pollInterval) {
			return fmt.Errorf("file transfer canceled for analysis %s after %s: %w", analysisID, time.Since(startTime).Truncate(time.Second), ctx.Err())
		}

		// Bump the interval towards the ceiling so long-running transfers
		// don't stay at the aggressive startup cadence.
		if pollInterval < maxPollInterval {
			pollInterval += pollIntervalStep
			if pollInterval > maxPollInterval {
				pollInterval = maxPollInterval
			}
		}

		// Log progress at most once per minute regardless of poll cadence.
		if elapsed := time.Since(lastLogged); elapsed >= time.Minute {
			log.Infof("file transfer in progress for analysis %s (uuid %s, %s elapsed)",
				analysisID, xferResp.UUID, time.Since(startTime).Truncate(time.Second))
			lastLogged = time.Now()
		}

		// JoinPath appends the transfer UUID to the base path (e.g. /upload/<uuid>).
		statusURL := *svcURL.JoinPath(xferResp.UUID)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL.String(), nil)
		if err != nil {
			return fmt.Errorf("creating status request: %w", err)
		}
		resp, err := transferHTTPClient.Do(req)
		if err != nil {
			return fmt.Errorf("polling transfer status: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close() // inline close: defer inside a loop defers until function return, not loop iteration
		if err != nil {
			return fmt.Errorf("reading status response: %w", err)
		}
		if err := json.Unmarshal(body, &xferResp); err != nil {
			return fmt.Errorf("unmarshalling status response: %w", err)
		}
	}

	if xferResp.Status == "failed" {
		log.Errorf("file transfer failed for analysis %s (uuid %s)", analysisID, xferResp.UUID)
		return fmt.Errorf("file transfer failed for analysis %s", analysisID)
	}

	log.Infof("file transfer complete for analysis %s (uuid %s)", analysisID, xferResp.UUID)
	return nil
}
