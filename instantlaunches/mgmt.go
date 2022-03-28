package instantlaunches

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"
)

// AddInstantLaunchHandler is the HTTP handler for adding a new instant launch.
func (a *App) AddInstantLaunchHandler(c echo.Context) error {
	ctx := c.Request().Context()
	il, err := NewInstantLaunchFromJSON(c.Request().Body)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "cannot parse JSON")
	}

	if il.AddedBy == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "username was not set")
	}

	if !strings.HasSuffix(il.AddedBy, a.UserSuffix) {
		il.AddedBy = fmt.Sprintf("%s%s", il.AddedBy, a.UserSuffix)
	}

	newil, err := a.AddInstantLaunch(ctx, il.QuickLaunchID, il.AddedBy)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}

	return c.JSON(http.StatusOK, newil)
}

// GetInstantLaunchHandler is the HTTP handler for getting a specific Instant Launch
// by its UUID.
func (a *App) GetInstantLaunchHandler(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id is missing")
	}

	il, err := a.GetInstantLaunch(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}

	return c.JSON(http.StatusOK, il)

}

// FullInstantLaunchHandler is the HTTP handler for getting a full description of
// an instant launch, including its quick launch, submission, and basic app info.
func (a *App) FullInstantLaunchHandler(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id is missing")
	}

	il, err := a.FullInstantLaunch(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}

	return c.JSON(http.StatusOK, il)
}

// UpdateInstantLaunchHandler is the HTTP handler for updating an instant launch.
func (a *App) UpdateInstantLaunchHandler(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusNotFound, "id is missing")
	}

	updated, err := NewInstantLaunchFromJSON(c.Request().Body)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "cannot parse JSON")
	}

	newvalue, err := a.UpdateInstantLaunch(ctx, id, updated.QuickLaunchID)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}

	return c.JSON(http.StatusOK, newvalue)
}

// DeleteInstantLaunchHandler is the HTTP handler for deleting an Instant Launch
// based on its UUID.
func (a *App) DeleteInstantLaunchHandler(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusNotFound, "id is missing")
	}

	err := a.DeleteInstantLaunch(ctx, id)
	return err

}

// ListInstantLaunchesHandler is the HTTP handler for listing all of the
// registered Instant Launches.
func (a *App) ListInstantLaunchesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	list, err := a.ListInstantLaunches(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}

	return c.JSON(http.StatusOK, list)
}

// FullListInstantLaunchesHandler is the HTTP handler for performing a full
// listing of all registered instant launches.
func (a *App) FullListInstantLaunchesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	list, err := a.FullListInstantLaunches(ctx)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}
	return c.JSON(http.StatusOK, list)
}

// ListViablePublicQuickLaunchesHandler is the HTTP handler for getting a listing
// of public quick launches that are associated with apps that are currently
// public. This should help us avoid situations where we accidentally list public
// quick launches for apps that have been deleted or are otherwise no longer public.
func (a *App) ListViablePublicQuickLaunchesHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user must be set")
	}

	if !strings.HasSuffix(user, a.UserSuffix) {
		user = fmt.Sprintf("%s%s", user, a.UserSuffix)
	}

	list, err := a.ListViablePublicQuickLaunches(ctx, user)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}
	return c.JSON(http.StatusOK, list)
}
