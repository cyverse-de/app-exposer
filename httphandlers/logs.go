package httphandlers

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	apiv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VICELogEntry contains the data returned for each log request.
type VICELogEntry struct {
	SinceTime string   `json:"since_time"`
	Lines     []string `json:"lines"`
}

// LogsHandler handles requests to access the analysis container logs for a pod in a running
// VICE app. Needs the 'id' and 'pod-name' mux Vars.
//
//	@ID				logs
//	@Summary		Return the logs for a running analysis
//	@Description	Handlers requests to access the container logs for a pod in a running
//	@Description	VICE app.
//	@Produce		json
//	@Param			previous	query		bool	false	"Whether to return previously terminated container logs"
//	@Param			since		query		int64	false	"The number of seconds in the past to begin showing logs"
//	@Param			since-time	query		int64	false	"The number of seconds since the epoch to begin showing logs"
//	@Param			tail-lines	query		int64	false	"The number of lines from the end of the log to show"
//	@Param			timestamps	query		bool	false	"Whether to display timestamps at the beginning of each log line"
//	@Param			container	query		string	false	"The name of the container to display logs from"
//	@Success		200			{object}	VICELogEntry
//	@Failure		400			{object}	common.ErrorResponse
//	@Failure		403			{object}	common.ErrorResponse
//	@Failure		500			{object}	common.ErrorResponse
//	@Router			/vice/{analysis-id}/logs [get]
func (h *HTTPHandlers) LogsHandler(c echo.Context) error {
	var (
		err        error
		id         string
		since      int64
		sinceTime  int64
		podName    string
		container  string
		previous   bool
		tailLines  int64
		timestamps bool
		user       string
		logOpts    *apiv1.PodLogOptions
	)

	ctx := c.Request().Context()

	// id is required
	id = c.Param("analysis-id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
	}

	// user is required
	user = c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusForbidden, "user is not set")
	}

	externalIDs, err := h.incluster.GetExternalIDs(ctx, user, id)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	if len(externalIDs) < 1 {
		return echo.NewHTTPError(http.StatusInternalServerError, fmt.Sprintf("no external-ids found for analysis-id %s", id))
	}

	//Just use the first external-id for now.
	externalID := externalIDs[0]

	logOpts = &apiv1.PodLogOptions{}

	// previous is optional
	if c.QueryParam("previous") != "" {
		if previous, err = strconv.ParseBool(c.QueryParam("previous")); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		logOpts.Previous = previous
	}

	// since is optional
	if c.QueryParam("since") != "" {
		if since, err = strconv.ParseInt(c.QueryParam("since"), 10, 64); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		logOpts.SinceSeconds = &since
	}

	if c.QueryParam("since-time") != "" {
		if sinceTime, err = strconv.ParseInt(c.QueryParam("since-time"), 10, 64); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		convertedSinceTime := metav1.Unix(sinceTime, 0)
		logOpts.SinceTime = &convertedSinceTime
	}

	// tail-lines is optional
	if c.QueryParam("tail-lines") != "" {
		if tailLines, err = strconv.ParseInt(c.QueryParam("tail-lines"), 10, 64); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		logOpts.TailLines = &tailLines
	}

	// follow needs to be false for now since upstream services end up using a full thread to process
	// a stream of updates
	logOpts.Follow = false

	// timestamps is optional
	if c.QueryParam("timestamps") != "" {
		if timestamps, err = strconv.ParseBool(c.QueryParam("timestamps")); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, err.Error())
		}

		logOpts.Timestamps = timestamps
	}

	// container is optional, but should have a default value of "analysis"
	if c.QueryParam("container") != "" {
		container = c.QueryParam("container")
	} else {
		container = "analysis"
	}

	logOpts.Container = container

	// We're getting a list of pods associated with the first external-id for the analysis,
	// but we're only going to use the first pod for now.
	podList, err := h.incluster.GetPods(ctx, externalID)
	if err != nil {
		return err
	}

	if len(podList) < 1 {
		return fmt.Errorf("no pods found for analysis %s with external ID %s", id, externalID)
	}

	podName = podList[0].Name

	// Finally, actually get the logs and write the response out
	podLogs := h.clientset.CoreV1().Pods(h.incluster.ViceNamespace).GetLogs(podName, logOpts)

	logReadCloser, err := podLogs.Stream(ctx)
	if err != nil {
		return err
	}
	defer logReadCloser.Close()

	bodyBytes, err := io.ReadAll(logReadCloser)
	if err != nil {
		return err
	}

	bodyLines := strings.Split(string(bodyBytes), "\n")
	newSinceTime := fmt.Sprintf("%d", time.Now().Unix())

	return c.JSON(http.StatusOK, &VICELogEntry{
		SinceTime: newSinceTime,
		Lines:     bodyLines,
	})
}
