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

// HandleSaveAndExit triggers the file transfer sidecar to upload outputs,
// then deletes all analysis resources.
func (o *Operator) HandleSaveAndExit(c echo.Context) error {
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	// Run file transfer and cleanup asynchronously so the caller doesn't block.
	go func() {
		bgCtx := context.Background()

		if err := o.triggerFileTransfer(bgCtx, analysisID, "/upload"); err != nil {
			log.Errorf("upload failed for %s: %v", analysisID, err)
		}

		if err := o.deleteAnalysisResources(bgCtx, analysisID); err != nil {
			log.Errorf("exit failed for %s: %v", analysisID, err)
		}
	}()

	return c.NoContent(http.StatusOK)
}

// HandleDownloadInputFiles triggers the file-transfer sidecar to download
// input files for an analysis.
func (o *Operator) HandleDownloadInputFiles(c echo.Context) error {
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	go func() {
		if err := o.triggerFileTransfer(context.Background(), analysisID, "/download"); err != nil {
			log.Errorf("download failed for %s: %v", analysisID, err)
		}
	}()

	return c.NoContent(http.StatusOK)
}

// HandleSaveOutputFiles triggers the file-transfer sidecar to upload
// output files for an analysis.
func (o *Operator) HandleSaveOutputFiles(c echo.Context) error {
	analysisID := c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "analysis-id is required")
	}

	go func() {
		if err := o.triggerFileTransfer(context.Background(), analysisID, "/upload"); err != nil {
			log.Errorf("upload failed for %s: %v", analysisID, err)
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("requesting transfer: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 399 {
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

	// Poll until the transfer finishes.
	for xferResp.Status != "completed" && xferResp.Status != "failed" {
		time.Sleep(5 * time.Second)

		statusURL := svcURL
		statusURL.Path = fmt.Sprintf("%s/%s", reqpath, xferResp.UUID)

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL.String(), nil)
		if err != nil {
			return fmt.Errorf("creating status request: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return fmt.Errorf("polling transfer status: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("reading status response: %w", err)
		}
		if err := json.Unmarshal(body, &xferResp); err != nil {
			return fmt.Errorf("unmarshalling status response: %w", err)
		}
	}

	if xferResp.Status == "failed" {
		return fmt.Errorf("file transfer failed for analysis %s", analysisID)
	}

	return nil
}
