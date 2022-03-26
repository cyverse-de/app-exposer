package instantlaunches

import (
	"bytes"
	"database/sql"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"path"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/labstack/echo/v4"
	"github.com/valyala/fastjson"
)

var log = common.Log

func handleError(err error, statusCode int) error {
	log.Error(err)
	return echo.NewHTTPError(statusCode, err.Error())
}

// InstantLaunchExists returns true if the id passed in exists in the database
func (a *App) InstantLaunchExists(id string) (bool, error) {
	var count int
	err := a.DB.Get(&count, "SELECT COUNT(*) FROM instant_launches WHERE id = $1;", id)
	return count > 0, err
}

func (a *App) listAVUs(c echo.Context) ([]byte, *http.Response, error) {
	ctx := c.Request().Context()

	log.Debug("in ListMetadataHandler")

	user := c.QueryParam("user")
	if user == "" {
		return nil, nil, echo.NewHTTPError(http.StatusBadRequest, "user is missing")
	}

	attr := c.QueryParam("attribute")
	value := c.QueryParam("value")
	unit := c.QueryParam("unit")

	svc, err := url.Parse(a.MetadataBaseURL)
	if err != nil {
		return nil, nil, handleError(err, http.StatusBadRequest)
	}

	svc.Path = path.Join(svc.Path, "/avus")
	query := svc.Query()
	query.Add("user", user)
	query.Add("target-type", "instant_launch")

	if attr != "" {
		query.Add("attribute", attr)
	}

	if value != "" {
		query.Add("value", value)
	}

	if unit != "" {
		query.Add("unit", unit)
	}
	svc.RawQuery = query.Encode()

	log.Debug(fmt.Sprintf("metadata endpoint: GET %s", svc.String()))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, svc.String(), nil)
	if err != nil {
		return nil, nil, handleError(err, http.StatusInternalServerError)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, handleError(err, http.StatusInternalServerError)
	}

	log.Debug(fmt.Sprintf("metadata endpoint: %s, status code: %d", svc.String(), resp.StatusCode))

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, resp, handleError(err, http.StatusInternalServerError)
	}

	return body, resp, nil
}

// FullListMetadataHandler returns a list of instant launches with the quick launches
// embedded, meaning that the submission field is included.
func (a *App) FullListMetadataHandler(c echo.Context) error {
	log.Debug("in FullListMetadataHandler")
	avuBody, _, err := a.listAVUs(c)
	if err != nil {
		return err
	}

	var p fastjson.Parser

	val, err := p.ParseBytes(avuBody)
	if err != nil {
		return err
	}

	avus := val.GetArray("avus")
	if avus == nil {
		avus = []*fastjson.Value{}
	}

	var targetIDs []string

	for _, avu := range avus {
		targetIDs = append(targetIDs, string(avu.GetStringBytes("target_id")))
	}

	fullListing, err := a.ListFullInstantLaunchesByIDs(targetIDs)
	if err != nil {
		if err == sql.ErrNoRows {
			return echo.NewHTTPError(http.StatusNotFound, "no instant launches found")
		}
		return err
	}

	return c.JSON(http.StatusOK, fullListing)
}

// ListMetadataHandler lists all of the instant launch metadata
// based on the attributes and values contained in the body.
func (a *App) ListMetadataHandler(c echo.Context) error {
	body, resp, err := a.listAVUs(c)
	if err != nil {
		return err
	}

	return c.Blob(resp.StatusCode, resp.Header.Get("content-type"), body)
}

// GetMetadataHandler returns all of the metadata associated with an instant launch.
func (a *App) GetMetadataHandler(c echo.Context) error {
	ctx := c.Request().Context()

	log.Debug("int GetMetadataHandler")

	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id is missing")
	}

	user := c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user is missing")
	}

	exists, err := a.InstantLaunchExists(id)
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("instant launch UUID %s not found", id))
	}

	svc, err := url.Parse(a.MetadataBaseURL)
	if err != nil {
		return handleError(err, http.StatusBadRequest)
	}

	svc.Path = path.Join(svc.Path, "/avus", "instant_launch", id)
	query := svc.Query()
	query.Add("user", user)
	svc.RawQuery = query.Encode()

	log.Debug(fmt.Sprintf("metadata endpoint: GET %s", svc.String()))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, svc.String(), nil)
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	log.Debug(fmt.Sprintf("metadata endpoint: %s, status code: %d", svc.String(), resp.StatusCode))

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	return c.Blob(resp.StatusCode, resp.Header.Get("content-type"), body)
}

// AddOrUpdateMetadataHandler adds or updates one or more AVUs on an instant
// launch.
func (a *App) AddOrUpdateMetadataHandler(c echo.Context) error {
	ctx := c.Request().Context()

	log.Debug("in AddOrUpdateMetadataHandler")

	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id is missing")
	}

	user := c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user is missing")
	}

	exists, err := a.InstantLaunchExists(id)
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("instant launch UUID %s not found", id))
	}

	inBody, err := ioutil.ReadAll(c.Request().Body)
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	svc, err := url.Parse(a.MetadataBaseURL)
	if err != nil {
		return handleError(err, http.StatusBadRequest)
	}

	svc.Path = path.Join(svc.Path, "/avus", "instant_launch", id)
	query := svc.Query()
	query.Add("user", user)
	svc.RawQuery = query.Encode()

	log.Debug(fmt.Sprintf("metadata endpoint: POST %s", svc.String()))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, svc.String(), bytes.NewReader(inBody))
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	req.Header.Set("content-type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	log.Debug(fmt.Sprintf("metadata endpoint: %s, status code: %d", svc.String(), resp.StatusCode))

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	return c.Blob(resp.StatusCode, resp.Header.Get("content-type"), body)
}

// SetAllMetadataHandler sets all of the AVUs associated with an instant
// launch to the set contained in the body of the request.
func (a *App) SetAllMetadataHandler(c echo.Context) error {
	ctx := c.Request().Context()

	log.Debug("in SetAllMetadataHandler")

	id := c.Param("id")
	if id == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "id is missing")
	}

	user := c.QueryParam("user")
	if user == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "user is missing")
	}

	exists, err := a.InstantLaunchExists(id)
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	if !exists {
		return echo.NewHTTPError(http.StatusNotFound, fmt.Sprintf("instant launch UUID %s not found", id))
	}

	inBody, err := ioutil.ReadAll(c.Request().Body)
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	svc, err := url.Parse(a.MetadataBaseURL)
	if err != nil {
		return handleError(err, http.StatusBadRequest)
	}

	svc.Path = path.Join(svc.Path, "/avus", "instant_launch", id)
	query := svc.Query()
	query.Add("user", user)
	svc.RawQuery = query.Encode()

	log.Debug(fmt.Sprintf("metadata endpoint: PUT %s", svc.String()))

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, svc.String(), bytes.NewReader(inBody))
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	req.Header.Set("content-type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	log.Debug(fmt.Sprintf("metadata endpoint: %s, status code: %d", svc.String(), resp.StatusCode))

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return handleError(err, http.StatusInternalServerError)
	}

	return c.Blob(resp.StatusCode, resp.Header.Get("content-type"), body)
}
