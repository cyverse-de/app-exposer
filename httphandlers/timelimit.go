package httphandlers

import (
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/labstack/gommon/log"
)

// TimeLimitUpdateHandler handles requests to update the time limit on an already running VICE app.
func (h *HTTPHandlers) TimeLimitUpdateHandler(c echo.Context) error {
	ctx := c.Request().Context()
	log.Info("update time limit called")

	var (
		err  error
		id   string
		user string
	)

	// user is required
	user = c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusForbidden, "user is not set")
	}

	// id is required
	id = c.Param("analysis-id")
	if id == "" {
		idErr := echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
		log.Error(idErr)
		return idErr
	}

	outputMap, err := h.incluster.UpdateTimeLimit(ctx, user, id)
	if err != nil {
		log.Error(err)
		return err
	}

	return c.JSON(http.StatusOK, outputMap)

}

// AdminTimeLimitUpdateHandler is basically the same as VICETimeLimitUpdate
// except that it doesn't require user information in the request.
func (h *HTTPHandlers) AdminTimeLimitUpdateHandler(c echo.Context) error {
	ctx := c.Request().Context()
	var (
		err  error
		id   string
		user string
	)
	// id is required
	id = c.Param("analysis-id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
	}

	user, _, err = h.apps.GetUserByAnalysisID(ctx, id)
	if err != nil {
		return err
	}

	outputMap, err := h.incluster.UpdateTimeLimit(ctx, user, id)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, outputMap)
}

// GetTimeLimitHandler implements the handler for getting the current time limit from the database.
func (h *HTTPHandlers) GetTimeLimitHandler(c echo.Context) error {
	ctx := c.Request().Context()
	log.Info("get time limit called")

	var (
		err        error
		analysisID string
		user       string
		userID     string
	)

	// user is required
	user = c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusForbidden, "user is not set")
	}

	// analysisID is required
	analysisID = c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
	}

	// Could use this to get the username, but we need to not break other services.
	_, userID, err = h.apps.GetUserByAnalysisID(ctx, analysisID)
	if err != nil {
		return err
	}

	outputMap, err := h.incluster.GetTimeLimit(ctx, userID, analysisID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, outputMap)
}

// AdminGetTimeLimitHandler is the same as VICEGetTimeLimit but doesn't require
// any user information in the request.
func (h *HTTPHandlers) AdminGetTimeLimitHandler(c echo.Context) error {
	ctx := c.Request().Context()
	log.Info("get time limit called")

	var (
		err        error
		analysisID string
		userID     string
	)

	// analysisID is required
	analysisID = c.Param("analysis-id")
	if analysisID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id parameter is empty")
	}

	// Could use this to get the username, but we need to not break other services.
	_, userID, err = h.apps.GetUserByAnalysisID(ctx, analysisID)
	if err != nil {
		return err
	}

	outputMap, err := h.incluster.GetTimeLimit(ctx, userID, analysisID)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, outputMap)
}
