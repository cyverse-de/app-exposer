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

// transferHTTPClient is used for requests to the file-transfer sidecar.
// It has a per-request timeout to prevent goroutines from blocking forever
// if the sidecar hangs or the connection stalls.
var transferHTTPClient = &http.Client{Timeout: 30 * time.Second}

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
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/save-and-exit [post]
func (o *Operator) HandleSaveAndExit(c echo.Context) error {
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	log.Infof("save-and-exit requested for analysis %s", analysisID)

	// Run file transfer and cleanup asynchronously so the caller doesn't block.
	// A fresh background context is used because the HTTP request context will
	// be cancelled once this handler returns, which would abort the transfer.
	go func() {
		bgCtx := context.Background()

		if err := o.triggerFileTransfer(bgCtx, analysisID, "/upload"); err != nil {
			log.Errorf("upload failed for analysis %s, aborting resource cleanup: %v", analysisID, err)
			return
		}

		log.Infof("upload complete for analysis %s, proceeding with cleanup", analysisID)

		if err := o.deleteAnalysisResources(bgCtx, analysisID); err != nil {
			log.Errorf("cleanup failed for analysis %s: %v", analysisID, err)
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
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/download-input-files [post]
func (o *Operator) HandleDownloadInputFiles(c echo.Context) error {
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	log.Infof("download-input-files requested for analysis %s", analysisID)

	// Use a fresh context: the HTTP request context cancels when this handler returns.
	go func() {
		if err := o.triggerFileTransfer(context.Background(), analysisID, "/download"); err != nil {
			log.Errorf("download failed for %s: %v", analysisID, err)
		} else {
			log.Infof("download succeeded for analysis %s", analysisID)
		}
	}()

	return c.NoContent(http.StatusOK)
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
//	@Security		BasicAuth
//	@Router			/analyses/{analysis-id}/save-output-files [post]
func (o *Operator) HandleSaveOutputFiles(c echo.Context) error {
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	log.Infof("save-output-files requested for analysis %s", analysisID)

	// Use a fresh context: the HTTP request context cancels when this handler returns.
	go func() {
		if err := o.triggerFileTransfer(context.Background(), analysisID, "/upload"); err != nil {
			log.Errorf("upload failed for %s: %v", analysisID, err)
		} else {
			log.Infof("upload succeeded for analysis %s", analysisID)
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

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
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

	// Poll until the transfer finishes, with an upper bound to prevent infinite loops.
	const maxPollIterations = 720 // 1 hour at 5s intervals
	pollCount := 0
	for xferResp.Status != "completed" && xferResp.Status != "failed" {
		pollCount++
		if pollCount >= maxPollIterations {
			return fmt.Errorf("file transfer timed out for analysis %s after %d seconds", analysisID, pollCount*5)
		}

		time.Sleep(5 * time.Second)

		// Log progress every ~60s (every 12 iterations at 5s intervals).
		if pollCount%12 == 0 {
			log.Infof("file transfer in progress for analysis %s (uuid %s, %ds elapsed)",
				analysisID, xferResp.UUID, pollCount*5)
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
