package instantlaunches

import (
	"database/sql"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/labstack/echo/v4"
)

// suffixUsername takes a possibly-already-suffixed username, strips any suffix, and adds the provided one, to ensure proper suffixing
func suffixUsername(username, suffix string) string {
	re, _ := regexp.Compile(`@.*$`)
	return fmt.Sprintf("%s@%s", re.ReplaceAllString(username, ""), strings.Trim(suffix, "@"))
}

// checkUserMatches ensures that `first` and `second` match when both suffixed the same.
func checkUserMatches(first, second, suffix string) bool {
	return (suffixUsername(first, suffix) == suffixUsername(second, suffix))
}

// AddInstantLaunchHandler is the HTTP handler for adding a new instant launch
// as a regular user
func (a *App) AddInstantLaunchHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user query parameter must be set")
	}

	il, err := NewInstantLaunchFromJSON(c.Request().Body)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "cannot parse JSON")
	}

	if il.AddedBy == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "username was not set")
	}

	if !checkUserMatches(il.AddedBy, user, a.UserSuffix) {
		return echo.NewHTTPError(http.StatusBadRequest, "not authorized to create instant launches as another user")
	}

	if !strings.HasSuffix(il.AddedBy, a.UserSuffix) {
		il.AddedBy = suffixUsername(il.AddedBy, a.UserSuffix)
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

// AdminAddInstantLaunchHandler is the HTTP handler for adding a new instant launch as an admin.
func (a *App) AdminAddInstantLaunchHandler(c echo.Context) error {
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

// UpdateInstantLaunchHandler is the HTTP handler for updating an instant launch as a regular user
func (a *App) UpdateInstantLaunchHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user query parameter must be set")
	}

	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusNotFound, "id is missing")
	}

	il, err := a.GetInstantLaunch(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}

	updated, err := NewInstantLaunchFromJSON(c.Request().Body)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "cannot parse JSON")
	}

	if !checkUserMatches(il.AddedBy, user, a.UserSuffix) || !checkUserMatches(il.AddedBy, updated.AddedBy, a.UserSuffix) {
		return echo.NewHTTPError(http.StatusBadRequest, "not authorized to edit other users' instant launches")
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

// AdminUpdateInstantLaunchHandler is the HTTP handler for updating an instant launch as an admin.
func (a *App) AdminUpdateInstantLaunchHandler(c echo.Context) error {
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
// based on its UUID as a regular user
func (a *App) DeleteInstantLaunchHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user query parameter must be set")
	}

	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusNotFound, "id is missing")
	}

	il, err := a.GetInstantLaunch(ctx, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}

	if !checkUserMatches(il.AddedBy, user, a.UserSuffix) {
		return echo.NewHTTPError(http.StatusBadRequest, "not authorized to delete other users' instant launches")
	}

	return a.DeleteInstantLaunch(ctx, id)
}

// AdminDeleteInstantLaunchHandler is the HTTP handler for deleting an Instant Launch
// based on its UUID as an admin
func (a *App) AdminDeleteInstantLaunchHandler(c echo.Context) error {
	ctx := c.Request().Context()
	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusNotFound, "id is missing")
	}

	return a.DeleteInstantLaunch(ctx, id)
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
