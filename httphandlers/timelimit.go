package httphandlers

import (
	"net/http"

	"github.com/labstack/echo/v4"
)

// @ID				time-limit-update
// @Summary		Handles requests to update the time limit on an already running VICE app.
// @Description	Updates the time limit on a running VICE app for a user. The user
// @Description	must have access to the analysis. The time limit is increased by a
// @Description	pre-configured amount.
// @Produce		json
// @Param			analysis-id	path		string	true	"Analysis ID"
// @Success		200			{object}	incluster.TimeLimit
// @Failure		400			{object}	common.ErrorResponse
// @Failure		403			{object}	common.ErrorResponse
// @Failure		500			{object}	common.ErrorResponse
// @Router			/vice/{analysis-id}/time-limit [post]
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

	timelimit, err := h.incluster.UpdateTimeLimit(ctx, user, id)
	if err != nil {
		log.Error(err)
		return err
	}

	return c.JSON(http.StatusOK, timelimit)

}

// @ID				admin-time-limit-update
// @Summary		Updates the time limit on an analysis without requiring user information
// @Description	Updates the time limit on an analysis without requiring user information.
// @Produce		json
// @Param			analysis-id	path		string	true	"Analysis ID"
// @Success		200			{object}	incluster.TimeLimit
// @Failure		400			{object}	common.ErrorResponse
// @Failure		500			{object}	common.ErrorResponse
// @Router			/vice/admin/analyses/{analysis-id}/time-limit [post]
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

// @ID				get-time-limit
// @Summary		Gets the time limit for an analysis on behalf of a user.
// @Description	Gets the time limit for an analysis on behalf of a user.
// @Produce		json
// @Param			user		query		string	true	"Username"
// @Param			analysis-id	path		string	true	"Analysis ID"
// @Success		200			{object}	incluster.TimeLimit
// @Failure		400			{object}	common.ErrorResponse
// @Failure		403			{object}	common.ErrorResponse
// @Failure		500			{object}	common.ErrorResponse
// @Router			/vice/{analysis-id}/time-limit [get]
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

// @ID				admin-get-time-limit
// @Summary		Gets an analysis's time limit without requiring user info.
// @Description	Gets an analysis's time limit without requiring user info.
// @Produce		json
// @Param			analysis-id	path		string	true	"Analysis ID"
// @Success		200			{object}	incluster.TimeLimit
// @Failure		400			{object}	common.ErrorResponse
// @Failure		500			{object}	common.ErrorResponse
// @Router			/vice/admin/analyses/{analysis-id}/time-limit [get]
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
