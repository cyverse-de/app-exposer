package instantlaunches

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

// UserMappingHandler is the echo handler for the http API that returns the user's
// instant launch mappings.
func (a *App) UserMappingHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.Param("username")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user was not set")
	}

	if !strings.HasSuffix(user, a.UserSuffix) {
		user = fmt.Sprintf("%s%s", user, a.UserSuffix)
	}

	m, err := a.UserMapping(ctx, user)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}
	return c.JSON(http.StatusOK, m)
}

// UpdateUserMappingHandler is the echo handler for the HTTP API that updates the user's
// instant launch mapping.
func (a *App) UpdateUserMappingHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.Param("username")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user was not set")
	}

	if !strings.HasSuffix(user, a.UserSuffix) {
		user = fmt.Sprintf("%s%s", user, a.UserSuffix)
	}

	newdefaults, err := InstantLaunchMappingFromJSON(c.Request().Body)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "cannot parse JSON")
	}

	updated, err := a.UpdateUserMapping(ctx, user, newdefaults)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}

	return c.JSON(http.StatusOK, updated)
}

// DeleteUserMappingHandler is the handler for the admin-only operation that removes
// the latest mapping for the user.
func (a *App) DeleteUserMappingHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.Param("username")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user was not set")
	}
	if !strings.HasSuffix(user, a.UserSuffix) {
		user = fmt.Sprintf("%s%s", user, a.UserSuffix)
	}
	return a.DeleteUserMapping(ctx, user)
}

// AddUserMappingHandler is the HTTP handler for adding a new user mapping to the database.
func (a *App) AddUserMappingHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.Param("username")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user was not set")
	}

	if !strings.HasSuffix(user, a.UserSuffix) {
		user = fmt.Sprintf("%s%s", user, a.UserSuffix)
	}

	newvalue, err := InstantLaunchMappingFromJSON(c.Request().Body)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "cannot parse JSON")
	}

	retval, err := a.AddUserMapping(ctx, user, newvalue)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, retval)
}

// AllUserMappingsHandler is the echo handler for the http API that returns the user's
// instant launch mappings.
func (a *App) AllUserMappingsHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.Param("username")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user was not set")
	}

	if !strings.HasSuffix(user, a.UserSuffix) {
		user = fmt.Sprintf("%s%s", user, a.UserSuffix)
	}

	m, err := a.AllUserMappings(ctx, user)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}

	return c.JSON(http.StatusOK, m)
}

// UserMappingsByVersionHandler is the echo handler for the http API that returns a specific
// version of the user's instant launch mappings.
func (a *App) UserMappingsByVersionHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.Param("username")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user was not set")
	}

	if !strings.HasSuffix(user, a.UserSuffix) {
		user = fmt.Sprintf("%s%s", user, a.UserSuffix)
	}

	version, err := strconv.ParseInt(c.Param("version"), 10, 0)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "cannot process version")
	}

	m, err := a.UserMappingsByVersion(ctx, user, int(version))
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}

	return c.JSON(http.StatusOK, m)
}

// UpdateUserMappingsByVersionHandler is the echo handler for the HTTP API that allows callers
// to update a user's instant launches for a specific version.
func (a *App) UpdateUserMappingsByVersionHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.Param("username")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user was not set")
	}

	if !strings.HasSuffix(user, a.UserSuffix) {
		user = fmt.Sprintf("%s%s", user, a.UserSuffix)
	}

	version, err := strconv.ParseInt(c.Param("version"), 10, 0)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "cannot process version")
	}

	// I'm not sure why, but this stuff seems to break echo's c.Bind() function
	// so we handle the unmarshalling without it here.
	update, err := InstantLaunchMappingFromJSON(c.Request().Body)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "cannot parse JSON")
	}

	newversion, err := a.UpdateUserMappingsByVersion(ctx, user, int(version), update)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, err.Error())
		}
		return err
	}

	return c.JSON(http.StatusOK, newversion)
}

// DeleteUserMappingsByVersionHandler is the echo handler for the HTTP API that allows callers
// delete a user's instant launch mappings at a specific version.
func (a *App) DeleteUserMappingsByVersionHandler(c echo.Context) error {
	ctx := c.Request().Context()
	user := c.Param("username")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user was not set")
	}

	if !strings.HasSuffix(user, a.UserSuffix) {
		user = fmt.Sprintf("%s%s", user, a.UserSuffix)
	}

	version, err := strconv.ParseInt(c.Param("version"), 10, 0)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "cannot process version")
	}

	return a.DeleteUserMappingsByVersion(ctx, user, int(version))
}
