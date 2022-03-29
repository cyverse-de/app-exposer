//nolint
package instantlaunches

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestUserMapping(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	rows := sqlmock.NewRows([]string{"id", "version", "mapping"}).
		AddRow("0", "0", "{}")

	mock.ExpectQuery("SELECT u.id, u.version, u.instant_launches as mapping FROM user_instant_launches u").
		WithArgs("test").
		WillReturnRows(rows)

	actual, err := app.UserMapping(context.Background(), "test")
	assert.NoError(err, "should not error")
	assert.Equal("0", actual.ID, "id should be 0")
	assert.Equal("0", actual.Version, "version should be 0")
	assert.True(
		cmp.Equal(
			&UserInstantLaunchMapping{
				ID:      "0",
				Version: "0",
				Mapping: InstantLaunchMapping{},
			},
			actual,
		),
		"should be equal",
	)
	assert.NoError(mock.ExpectationsWereMet(), "expectataions were not met")
}

func TestUserMappingHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	rows := sqlmock.NewRows([]string{"id", "version", "mapping"}).
		AddRow("0", "0", "{}")

	expectedUsername := fmt.Sprintf("test%s", app.UserSuffix)

	mock.ExpectQuery("SELECT u.id, u.version, u.instant_launches as mapping FROM user_instant_launches u").
		WithArgs(expectedUsername).
		WillReturnRows(rows)

	req := httptest.NewRequest("GET", "http://localhost/instantlaunches/test", nil)
	rec := httptest.NewRecorder()
	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/:username")
	c.SetParamNames("username")
	c.SetParamValues("test")

	err = app.UserMappingHandler(c)
	if assert.NoError(err, "should not error") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := &UserInstantLaunchMapping{}
		err = json.Unmarshal(rec.Body.Bytes(), &actual)
		assert.NoError(err, "failed to unmarshal the response body")
		assert.Equal("0", actual.ID, "id should be 0")
		assert.Equal("0", actual.Version, "version should be 0")
		assert.True(
			cmp.Equal(
				&UserInstantLaunchMapping{
					ID:      "0",
					Version: "0",
					Mapping: InstantLaunchMapping{},
				},
				actual,
			),
			"should be equal",
		)
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestUpdateUserMapping(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := &InstantLaunchMapping{
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
	assert.NoError(err, "no errors expected")

	rows := sqlmock.NewRows([]string{"instant_launches"}).
		AddRow(v)
	mock.ExpectQuery("UPDATE ONLY user_instant_launches").
		WithArgs(v, fmt.Sprintf("test%s", app.UserSuffix)).
		WillReturnRows(rows)

	actual, err := app.UpdateUserMapping(context.Background(), fmt.Sprintf("test%s", app.UserSuffix), expected)
	if assert.NoError(err, "no errors expected") {
		assert.True(cmp.Equal(expected, actual), "should be equal")
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectataions were not met")
}

func TestUpdateUserMappingHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := &InstantLaunchMapping{
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
	assert.NoError(err, "no errors expected")

	expectedUsername := fmt.Sprintf("test%s", app.UserSuffix)

	rows := sqlmock.NewRows([]string{"instant_launches"}).
		AddRow(v)
	mock.ExpectQuery("UPDATE ONLY user_instant_launches").
		WithArgs(v, expectedUsername).
		WillReturnRows(rows)

	req := httptest.NewRequest("POST", "http://localhost/instantlaunches/test", bytes.NewReader(v))
	rec := httptest.NewRecorder()
	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/:username")
	c.SetParamNames("username")
	c.SetParamValues("test")

	err = app.UpdateUserMappingHandler(c)
	if assert.NoError(err, "should not error") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := &InstantLaunchMapping{}
		err = json.Unmarshal(rec.Body.Bytes(), &actual)
		assert.NoError(err, "failed to unmarshal the response body")
		assert.True(cmp.Equal(expected, actual), "should be equal")
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")

}

func TestDeleteUserMapping(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	mock.ExpectExec("DELETE FROM ONLY user_instant_launches").
		WithArgs("test").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = app.DeleteUserMapping(context.Background(), "test")
	assert.NoError(err, "should not error")
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestDeleteUserMappingHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expectedUsername := fmt.Sprintf("test%s", app.UserSuffix)

	mock.ExpectExec("DELETE FROM ONLY user_instant_launches").
		WithArgs(expectedUsername).
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := httptest.NewRequest("DELETE", "http://localhost/instantlaunches/test", nil)
	rec := httptest.NewRecorder()
	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/:username")
	c.SetParamNames("username")
	c.SetParamValues("test")

	err = app.DeleteUserMappingHandler(c)
	if assert.NoError(err, "should not error") {
		assert.Equal(http.StatusOK, rec.Code)
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestAddUserMapping(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := &InstantLaunchMapping{
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
	assert.NoError(err, "no errors expected")

	rows := sqlmock.NewRows([]string{"instant_launches"}).AddRow(v)
	mock.ExpectQuery("INSERT INTO user_instant_launches").
		WillReturnRows(rows)

	actual, err := app.AddUserMapping(context.Background(), "test", expected)
	if assert.NoError(err, "should not error") {
		assert.True(cmp.Equal(expected, actual), "should be equal")
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestAddUserMappingHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := &InstantLaunchMapping{
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

	expectedUsername := fmt.Sprintf("test%s", app.UserSuffix)

	v, err := json.Marshal(expected)
	assert.NoError(err, "no errors expected")

	rows := sqlmock.NewRows([]string{"instant_launches"}).AddRow(v)
	mock.ExpectQuery("INSERT INTO user_instant_launches").
		WithArgs(v, expectedUsername).
		WillReturnRows(rows)

	req := httptest.NewRequest("PUT", "http://localhost/instantlaunches/test", bytes.NewReader(v))
	rec := httptest.NewRecorder()
	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/:username")
	c.SetParamNames("username")
	c.SetParamValues("test")

	err = app.AddUserMappingHandler(c)
	if assert.NoError(err, "should not error") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := &InstantLaunchMapping{}
		err = json.Unmarshal(rec.Body.Bytes(), &actual)
		assert.NoError(err, "failed to unmarshal the response body")
		assert.True(cmp.Equal(expected, actual), "should be equal")
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestAllUserMappings(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expectedMapping := InstantLaunchMapping{
		"one": &InstantLaunchSelector{
			Kind:    "glob",
			Pattern: "*",
			Default: InstantLaunch{
				ID:            "0",
				QuickLaunchID: "0",
				AddedBy:       "test",
				AddedOn:       "today",
			},
		},
	}

	expected := []UserInstantLaunchMapping{
		{
			ID:      "0",
			Version: "0",
			UserID:  "0",
			Mapping: expectedMapping,
		},
	}

	v, err := json.Marshal(expectedMapping)
	assert.NoError(err, "no errors expected")

	rows := sqlmock.NewRows([]string{"id", "version", "user_id", "mapping"}).AddRow("0", "0", "0", v)
	mock.ExpectQuery("SELECT u.id, u.version, u.user_id, u.instant_launches as mapping FROM user_instant_launches u").
		WithArgs("test").
		WillReturnRows(rows)

	actual, err := app.AllUserMappings(context.Background(), "test")
	if assert.NoError(err, "should not return error") {
		assert.True(cmp.Equal(expected, actual), "should be equal")
	}

	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")

}

func TestAllUserMappingsHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expectedMapping := InstantLaunchMapping{
		"one": &InstantLaunchSelector{
			Kind:    "glob",
			Pattern: "*",
			Default: InstantLaunch{
				ID:            "0",
				QuickLaunchID: "0",
				AddedBy:       "test",
				AddedOn:       "today",
			},
		},
	}

	expected := []UserInstantLaunchMapping{
		{
			ID:      "0",
			Version: "0",
			UserID:  "0",
			Mapping: expectedMapping,
		},
	}

	v, err := json.Marshal(expectedMapping)
	assert.NoError(err, "no errors expected")

	expectedUsername := fmt.Sprintf("test%s", app.UserSuffix)

	rows := sqlmock.NewRows([]string{"id", "version", "user_id", "mapping"}).AddRow("0", "0", "0", v)
	mock.ExpectQuery("SELECT u.id, u.version, u.user_id, u.instant_launches as mapping FROM user_instant_launches u").
		WithArgs(expectedUsername).
		WillReturnRows(rows)

	req := httptest.NewRequest("GET", "http://localhost/instantlaunches/test", bytes.NewReader(v))
	rec := httptest.NewRecorder()
	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/:username")
	c.SetParamNames("username")
	c.SetParamValues("test")

	err = app.AllUserMappingsHandler(c)
	if assert.NoError(err, "shouldn't be an error") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := []UserInstantLaunchMapping{}
		err = json.Unmarshal(rec.Body.Bytes(), &actual)
		assert.NoError(err, "failed to unmarshal the response body")
		assert.True(cmp.Equal(expected, actual), "should be equal")

	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestUserMappingsByVersion(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expectedMapping := InstantLaunchMapping{
		"one": &InstantLaunchSelector{
			Kind:    "glob",
			Pattern: "*",
			Default: InstantLaunch{
				ID:            "0",
				QuickLaunchID: "0",
				AddedBy:       "test",
				AddedOn:       "today",
			},
		},
	}

	expected := UserInstantLaunchMapping{
		ID:      "0",
		Version: "0",
		UserID:  "0",
		Mapping: expectedMapping,
	}

	v, err := json.Marshal(expectedMapping)
	assert.NoError(err, "no errors expected")

	rows := sqlmock.NewRows([]string{"id", "version", "user_id", "mapping"}).AddRow("0", "0", "0", v)
	mock.ExpectQuery("SELECT u.id, u.version, u.instant_launches as mapping FROM user_instant_launches u").
		WithArgs("test", 0).
		WillReturnRows(rows)

	actual, err := app.UserMappingsByVersion(context.Background(), "test", 0)
	if assert.NoError(err, "no error expected") {
		assert.True(cmp.Equal(expected, actual), "should be equal")
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")

}

func TestUserMappingsByVersionHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expectedMapping := InstantLaunchMapping{
		"one": &InstantLaunchSelector{
			Kind:    "glob",
			Pattern: "*",
			Default: InstantLaunch{
				ID:            "0",
				QuickLaunchID: "0",
				AddedBy:       "test",
				AddedOn:       "today",
			},
		},
	}

	expected := &UserInstantLaunchMapping{
		ID:      "0",
		Version: "0",
		UserID:  "0",
		Mapping: expectedMapping,
	}

	v, err := json.Marshal(expectedMapping)
	assert.NoError(err, "no errors expected")

	expectedUsername := fmt.Sprintf("test%s", app.UserSuffix)

	rows := sqlmock.NewRows([]string{"id", "version", "user_id", "mapping"}).AddRow("0", "0", "0", v)
	mock.ExpectQuery("SELECT u.id, u.version, u.instant_launches as mapping FROM user_instant_launches u").
		WithArgs(expectedUsername, 0).
		WillReturnRows(rows)

	req := httptest.NewRequest("GET", "http://localhost/instantlaunches/test/0", bytes.NewReader(v))
	rec := httptest.NewRecorder()
	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/:username/:version")
	c.SetParamNames("username", "version")
	c.SetParamValues("test", "0")

	err = app.UserMappingsByVersionHandler(c)
	if assert.NoError(err, "shouldn't be an error") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := &UserInstantLaunchMapping{}
		err = json.Unmarshal(rec.Body.Bytes(), &actual)
		assert.NoError(err, "failed to unmarshal the response body")
		assert.True(cmp.Equal(expected, actual), "should be equal")

	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestUpdateUserMappingsByVersion(t *testing.T) {
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
				AddedBy:       "test",
				AddedOn:       "today",
			},
		},
	}

	v, err := json.Marshal(expected)
	assert.NoError(err, "no errors expected")

	rows := sqlmock.NewRows([]string{"instant_launches"}).AddRow(v)
	mock.ExpectQuery("UPDATE ONLY user_instant_launches AS def").
		WithArgs(v, 0, "test").
		WillReturnRows(rows)

	actual, err := app.UpdateUserMappingsByVersion(context.Background(), "test", 0, expected)
	if assert.NoError(err, "no error expected") {
		assert.True(cmp.Equal(expected, actual), "should be equal")
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestUpdateUserMappingsByVersionHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
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
				AddedBy:       "test",
				AddedOn:       "today",
			},
		},
	}

	v, err := json.Marshal(expected)
	assert.NoError(err, "no errors expected")

	expectedUsername := fmt.Sprintf("test%s", app.UserSuffix)

	rows := sqlmock.NewRows([]string{"instant_launches"}).AddRow(v)
	mock.ExpectQuery("UPDATE ONLY user_instant_launches AS def").
		WithArgs(v, 0, expectedUsername).
		WillReturnRows(rows)

	req := httptest.NewRequest("POST", "http://localhost/instantlaunches/test/0", bytes.NewReader(v))
	rec := httptest.NewRecorder()
	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/:username/:version")
	c.SetParamNames("username", "version")
	c.SetParamValues("test", "0")

	err = app.UpdateUserMappingsByVersionHandler(c)
	if assert.NoError(err, "shouldn't be an error") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := &InstantLaunchMapping{}
		err = json.Unmarshal(rec.Body.Bytes(), &actual)
		assert.NoError(err, "failed to unmarshal the response body")
		assert.True(cmp.Equal(expected, actual), "should be equal")
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestDeleteUserMappingsByVersion(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	mock.ExpectExec("DELETE FROM ONLY user_instant_launches AS def USING users WHERE def.user_id = users.id").
		WithArgs("test", 0).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = app.DeleteUserMappingsByVersion(context.Background(), "test", 0)
	assert.NoError(err, "no error expected")
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestDeleteUserMappingsByVersionHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	mock.ExpectExec("DELETE FROM ONLY user_instant_launches AS def USING users WHERE def.user_id = users.id").
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := httptest.NewRequest("DELETE", "http://localhost/instantlaunches/test/0", nil)
	rec := httptest.NewRecorder()
	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/:username/:version")
	c.SetParamNames("username", "version")
	c.SetParamValues("test", "0")

	err = app.DeleteUserMappingsByVersionHandler(c)
	if assert.NoError(err, "shouldn't be an error") {
		assert.Equal(http.StatusOK, rec.Code)
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}
