package instantlaunches

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4"
)

func SetupApp() (*App, sqlmock.Sqlmock, *echo.Echo, error) {
	mockdb, mock, err := sqlmock.New()
	if err != nil {
		return nil, mock, nil, fmt.Errorf("error connecting to mock database %s", err)
	}

	sqlxMockDB := sqlx.NewDb(mockdb, "sqlmock")
	e := echo.New()
	g := e.Group("/instantlaunches")

	ilInit := &Init{
		UserSuffix:      "@iplantcollaborative.org",
		MetadataBaseURL: "",
		PermissionsURL:  "",
	}
	app := New(sqlxMockDB, g, ilInit)
	return app, mock, e, nil
}

func TestLatestDefaults(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	rows := sqlmock.NewRows([]string{"id", "version", "mapping"}).
		AddRow("0", "0", "{}")

	mock.ExpectQuery(latestDefaultsQuery).WillReturnRows(rows)

	mapping, err := app.LatestDefaults(context.Background())
	assert.NoError(err, "error from LatestDefaults should be nil")
	assert.Equal("0", mapping.ID, "id should be 0")
	assert.Equal("0", mapping.Version, "version should be 0")
	assert.Equal(0, len(mapping.Mapping), "mapping should be empty")
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestGetLatestDefaults(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := map[string]*InstantLaunchSelector{
		"one": {
			Pattern: "*",
			Kind:    "glob",
			Default: InstantLaunch{
				ID:            "0",
				QuickLaunchID: "0",
				AddedBy:       "test",
				AddedOn:       "today",
			},
			Compatible: []InstantLaunch{},
		},
	}
	v, err := json.Marshal(expected)
	if err != nil {
		t.Fatalf("error unmarshalling expected value: %s", err)
	}

	rows := sqlmock.NewRows([]string{"id", "version", "mapping"}).
		AddRow("0", "0", v)

	mock.ExpectQuery(latestDefaultsQuery).WillReturnRows(rows)

	req, err := http.NewRequest("GET", "http://localhost/instantlaunches/defaults", nil)
	if err != nil {
		t.Fatalf("error creating new http request: %s", err)
	}
	rec := httptest.NewRecorder()

	c := router.NewContext(req, rec)

	err = app.LatestDefaultsHandler(c)
	if assert.NoError(err, "error from GetLatestDefaults should be nil") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := &DefaultInstantLaunchMapping{}
		err = json.Unmarshal(rec.Body.Bytes(), actual)
		if assert.NoError(err, "should be able to parse body") {
			assert.Equal("0", actual.ID, "id should be 0")
			assert.Equal("0", actual.Version, "version should be 0")
			assert.Equal(expected["one"], actual.Mapping["one"], "mapping should return expected value")
		}
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestUpdateLatestDefaults(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := &InstantLaunchMapping{
		"one": &InstantLaunchSelector{
			Pattern: "*",
			Kind:    "glob",
			Default: InstantLaunch{
				ID:            "0",
				QuickLaunchID: "0",
				AddedBy:       "test",
				AddedOn:       "today",
			},
			Compatible: []InstantLaunch{},
		},
	}

	v, err := json.Marshal(expected)
	if err != nil {
		t.Fatalf("error unmarshalling expected value: %s", err)
	}

	rows := sqlmock.NewRows([]string{"instant_launches"}).
		AddRow(v)

	mock.ExpectQuery("UPDATE ONLY default_instant_launches").WillReturnRows(rows)

	mapping, err := app.UpdateLatestDefaults(context.Background(), expected)
	assert.NoError(err, "error from UpdateLatestDefaults should be nil")
	assert.True(cmp.Equal(expected, mapping), "mappings should match")
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestUpdateLatestDefaultsHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := map[string]*InstantLaunchSelector{
		"one": {
			Pattern: "*",
			Kind:    "glob",
			Default: InstantLaunch{
				ID:            "0",
				QuickLaunchID: "0",
				AddedBy:       "test",
				AddedOn:       "today",
			},
			Compatible: []InstantLaunch{},
		},
	}

	v, err := json.Marshal(expected)
	if err != nil {
		t.Fatalf("error unmarshalling expected value: %s", err)
	}

	rows := sqlmock.NewRows([]string{"instant_launches"}).
		AddRow(v)

	mock.ExpectQuery("UPDATE ONLY default_instant_launches").WillReturnRows(rows)

	req := httptest.NewRequest("PUT", "http://localhost/instantlaunches/defaults", bytes.NewReader(v))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()

	c := router.NewContext(req, rec)

	err = app.UpdateLatestDefaultsHandler(c)
	if assert.NoError(err, "error from UpdateLatestDefaultsHandler") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := map[string]*InstantLaunchSelector{}
		err = json.Unmarshal(rec.Body.Bytes(), &actual)
		if assert.NoError(err, "should be able to parse body") {
			assert.Equal(1, len(actual))
			assert.True(cmp.Equal(expected["one"], actual["one"]))
		}
	}
}

func TestDeleteLatestDefaults(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	mock.ExpectExec("DELETE FROM ONLY default_instant_launches AS def").WillReturnResult(sqlmock.NewResult(0, 1))

	err = app.DeleteLatestDefaults(context.Background())
	assert.NoError(err, "delete shouldn't return an error")
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestDeleteLatestDefaultsHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	req := httptest.NewRequest("DELETE", "http://localhost/instantlaunches/defaults", nil)
	rec := httptest.NewRecorder()
	c := router.NewContext(req, rec)

	mock.ExpectExec("DELETE FROM ONLY default_instant_launches AS def").WillReturnResult(sqlmock.NewResult(0, 1))

	err = app.DeleteLatestDefaultsHandler(c)
	if assert.NoError(err, "shouldn't be an error") {
		assert.Equal(http.StatusOK, rec.Code)
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestAddLatestDefaults(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := &InstantLaunchMapping{
		"one": &InstantLaunchSelector{
			Pattern: "*",
			Kind:    "glob",
			Default: InstantLaunch{
				ID:            "0",
				QuickLaunchID: "0",
				AddedBy:       "test",
				AddedOn:       "today",
			},
			Compatible: []InstantLaunch{},
		},
	}

	v, err := json.Marshal(expected)
	if err != nil {
		t.Fatalf("error unmarshalling expected value: %s", err)
	}

	rows := sqlmock.NewRows([]string{"instant_launches"}).AddRow(v)

	testUser := fmt.Sprintf("test%s", app.UserSuffix)

	mock.ExpectQuery("INSERT INTO default_instant_launches").
		WithArgs(v, testUser).
		WillReturnRows(rows)

	actual, err := app.AddLatestDefaults(context.Background(), expected, testUser)
	if assert.NoError(err, "shouldn't be an error") {
		assert.True(cmp.Equal(expected, actual), "should be equal")
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestAddLatestDefaultsHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := &InstantLaunchMapping{
		"one": &InstantLaunchSelector{
			Pattern: "*",
			Kind:    "glob",
			Default: InstantLaunch{
				ID:            "0",
				QuickLaunchID: "0",
				AddedBy:       "test",
				AddedOn:       "today",
			},
			Compatible: []InstantLaunch{},
		},
	}

	v, err := json.Marshal(expected)
	if err != nil {
		t.Fatalf("error unmarshalling expected value: %s", err)
	}

	rows := sqlmock.NewRows([]string{"instant_launches"}).AddRow(v)

	testUser := fmt.Sprintf("test%s", app.UserSuffix)

	mock.ExpectQuery("INSERT INTO default_instant_launches").
		WithArgs(v, testUser).
		WillReturnRows(rows)

	req := httptest.NewRequest("POST", "http://localhost/instantlaunches/defaults?username=test", bytes.NewReader(v))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	c := router.NewContext(req, rec)

	err = app.AddLatestDefaultsHandler(c)

	if assert.NoError(err, "shouldn't cause an error") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := &InstantLaunchMapping{}
		err = json.Unmarshal(rec.Body.Bytes(), actual)
		if assert.NoError(err, "should be able to parse the response body") {
			assert.True(cmp.Equal(expected, actual), "should be equal")
		}
	}
}

func TestDefaultsByVersion(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := &DefaultInstantLaunchMapping{
		ID:      "0",
		Version: "0",
		Mapping: map[string]*InstantLaunchSelector{
			"one": {
				Kind:    "glob",
				Pattern: "*",
				Default: InstantLaunch{
					ID:            "0",
					QuickLaunchID: "0",
					AddedBy:       "admin",
					AddedOn:       "today",
				},
			},
		},
	}

	v, err := json.Marshal(expected.Mapping)
	assert.NoError(err, "should not error")

	mock.ExpectQuery("SELECT def.id, def.version, def.instant_launches as mapping FROM default_instant_launches def WHERE def.version =").
		WithArgs(0).
		WillReturnRows(
			sqlmock.NewRows([]string{"id", "version", "mapping"}).
				AddRow("0", "0", v),
		)

	actual, err := app.DefaultsByVersion(context.Background(), 0)
	if assert.NoError(err) {
		assert.True(cmp.Equal(expected, actual))
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestDefaultsByVersionHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := &DefaultInstantLaunchMapping{
		ID:      "0",
		Version: "0",
		Mapping: map[string]*InstantLaunchSelector{
			"one": {
				Kind:    "glob",
				Pattern: "*",
				Default: InstantLaunch{
					ID:            "0",
					QuickLaunchID: "0",
					AddedBy:       "admin",
					AddedOn:       "today",
				},
			},
		},
	}

	v, err := json.Marshal(expected.Mapping)
	assert.NoError(err, "should not error")

	mock.ExpectQuery("SELECT def.id, def.version, def.instant_launches as mapping FROM default_instant_launches def WHERE def.version =").
		WithArgs(0).
		WillReturnRows(
			sqlmock.NewRows([]string{"id", "version", "mapping"}).
				AddRow("0", "0", v),
		)
	req := httptest.NewRequest("GET", "http://localhost/instantlaunches/defaults/0", nil)
	rec := httptest.NewRecorder()
	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/defaults/:version")
	c.SetParamNames("version")
	c.SetParamValues("0")

	err = app.DefaultsByVersionHandler(c)
	if assert.NoError(err, "should not error") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := &DefaultInstantLaunchMapping{}
		err = json.Unmarshal(rec.Body.Bytes(), &actual)
		if assert.NoError(err, "should be able to parse body") {
			assert.Equal(1, len(actual.Mapping))
			assert.True(cmp.Equal(expected, actual), "should match")
		}
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestUpdateDefaultsByVersion(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := &InstantLaunchMapping{
		"one": &InstantLaunchSelector{
			Kind:    "glob",
			Pattern: "*",
			Default: InstantLaunch{
				ID:            "0",
				QuickLaunchID: "0",
				AddedBy:       "admin",
				AddedOn:       "today",
			},
		},
	}

	v, err := json.Marshal(expected)
	assert.NoError(err, "should not error")

	mock.ExpectQuery("UPDATE ONLY default_instant_launches AS def").
		WithArgs(v, 0).
		WillReturnRows(
			sqlmock.NewRows([]string{"instant_launches"}).
				AddRow(v),
		)

	actual, err := app.UpdateDefaultsByVersion(context.Background(), expected, 0)
	if assert.NoError(err, "should not error") {
		assert.True(cmp.Equal(expected, actual), "should be equal")
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestUpdateDefaultsByVersionHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := &InstantLaunchMapping{
		"one": &InstantLaunchSelector{
			Pattern: "*",
			Kind:    "glob",
			Default: InstantLaunch{
				ID:            "0",
				QuickLaunchID: "0",
				AddedBy:       "test",
				AddedOn:       "today",
			},
			Compatible: []InstantLaunch{},
		},
	}

	v, err := json.Marshal(expected)
	assert.NoError(err, "should not error")

	mock.ExpectQuery("UPDATE ONLY default_instant_launches AS def").
		WithArgs(v, 0).
		WillReturnRows(
			sqlmock.NewRows([]string{"instant_launches"}).
				AddRow(v),
		)

	req := httptest.NewRequest("PUT", "http://localhost/instantlaunches/defaults/0", bytes.NewReader(v))
	rec := httptest.NewRecorder()
	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/defaults/:version")
	c.SetParamNames("version")
	c.SetParamValues("0")

	err = app.UpdateDefaultsByVersionHandler(c)
	if assert.NoError(err, "should not error") {
		assert.Equal(http.StatusOK, rec.Code)
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestDeleteDefaultsByVersion(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	mock.ExpectExec("DELETE FROM ONLY default_instant_launches as def").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = app.DeleteDefaultsByVersion(context.Background(), 0)
	assert.NoError(err, "delete shouldn't return an error")
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestDeleteDefaultsByVersionHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	mock.ExpectExec("DELETE FROM ONLY default_instant_launches as def").
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := httptest.NewRequest("DELETE", "http://localhost/instantlaunches/defaults/0", nil)
	rec := httptest.NewRecorder()
	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/defaults/:version")
	c.SetParamNames("version")
	c.SetParamValues("0")

	err = app.DeleteDefaultsByVersionHandler(c)
	if assert.NoError(err, "shouldn't be an error") {
		assert.Equal(http.StatusOK, rec.Code)
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestListAllDefaults(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	mock.ExpectQuery(listAllDefaultsQuery).
		WillReturnRows(
			sqlmock.NewRows([]string{"id", "version", "mapping"}).
				AddRow("0", "0", "{}").
				AddRow("1", "1", `{"one":"two"}`),
		)

	listing, err := app.ListAllDefaults(context.Background())
	if assert.NoError(err, "should not return an error") {
		assert.Equal(2, len(listing.Defaults), "number of rows should be 2")
		assert.Equal("0", listing.Defaults[0].ID, "ID should be 0")
		assert.Equal("0", listing.Defaults[0].Version, "Version should be 0")
		assert.Equal("1", listing.Defaults[1].ID, "ID should be 1")
		assert.Equal("1", listing.Defaults[1].Version, "Version should be 1")
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}
