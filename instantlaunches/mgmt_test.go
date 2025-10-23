// nolint
package instantlaunches

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
)

func TestAddInstantLaunch(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	rows := sqlmock.NewRows([]string{"id", "quick_launch_id", "added_by", "added_on"}).
		AddRow("0", "0", "test@iplantcollaborative.org", "today")

	mock.ExpectQuery("INSERT INTO instant_launches").WillReturnRows(rows)

	actual, err := app.AddInstantLaunch(context.Background(), "0", "test@iplantcollaborative.org")
	assert.NoError(err, "error should be nil")
	assert.Equal("0", actual.ID, "id should be 0")
	assert.Equal("0", actual.QuickLaunchID, "quick_launch_id should be 0")
	assert.Equal("test@iplantcollaborative.org", actual.AddedBy, "added_by should be test@iplantcollaborative.org")
	assert.Equal("today", actual.AddedOn, "added_on should be set to 'today'")
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestAddInstantLaunchHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := &InstantLaunch{
		QuickLaunchID: "0",
		AddedBy:       "test@iplantcollaborative.org",
	}

	v, err := json.Marshal(expected)
	assert.NoError(err, "should not error")

	rows := sqlmock.NewRows([]string{"id", "quick_launch_id", "added_by", "added_on"}).
		AddRow("0", expected.QuickLaunchID, expected.AddedBy, "today")

	mock.ExpectQuery("INSERT INTO instant_launches").WillReturnRows(rows)

	req := httptest.NewRequest("PUT", "http://localhost/instantlaunches?user=test", bytes.NewReader(v))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	c := router.NewContext(req, rec)

	err = app.AddInstantLaunchHandler(c)
	if assert.NoError(err, "should not error") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := &InstantLaunch{}
		err = json.Unmarshal(rec.Body.Bytes(), &actual)
		if assert.NoError(err, "should be able to parse body") {
			assert.True(cmp.Equal(expected.QuickLaunchID, actual.QuickLaunchID), "should be equal")
			assert.True(cmp.Equal(expected.AddedBy, actual.AddedBy), "should be equal")
		}
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestGetInstantLaunch(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	rows := sqlmock.NewRows([]string{"id", "quick_launch_id", "added_by", "added_on"}).
		AddRow("0", "0", "test@iplantcollaborative.org", "today")

	mock.ExpectQuery("SELECT i.id, i.quick_launch_id, i.added_by, i.added_on FROM instant_launches i").
		WillReturnRows(rows)

	actual, err := app.GetInstantLaunch(context.Background(), "0")
	assert.NoError(err, "error should be nil")
	assert.Equal("0", actual.ID, "id should be 0")
	assert.Equal("0", actual.QuickLaunchID, "quick_launch_id should be 0")
	assert.Equal("test@iplantcollaborative.org", actual.AddedBy, "added_by should be test@iplantcollaborative.org")
	assert.Equal("today", actual.AddedOn, "added_on should be set to 'today'")
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestGetInstantLaunchHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := &InstantLaunch{
		ID:            "0",
		QuickLaunchID: "0",
		AddedBy:       "test@iplantcollaborative.org",
		AddedOn:       "today",
	}

	rows := sqlmock.NewRows([]string{"id", "quick_launch_id", "added_by", "added_on"}).
		AddRow(expected.ID, expected.QuickLaunchID, expected.AddedBy, expected.AddedOn)

	mock.ExpectQuery("SELECT i.id, i.quick_launch_id, i.added_by, i.added_on FROM instant_launches i").
		WithArgs("0").
		WillReturnRows(rows)

	req := httptest.NewRequest("GET", "http://localhost/instantlaunches/0", nil)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/:id")
	c.SetParamNames("id")
	c.SetParamValues("0")

	err = app.GetInstantLaunchHandler(c)
	if assert.NoError(err, "should not error") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := &InstantLaunch{}
		err = json.Unmarshal(rec.Body.Bytes(), &actual)
		if assert.NoError(err, "should be able to parse body") {
			assert.Equal(expected.ID, actual.ID, "should be equal")
			assert.Equal(expected.QuickLaunchID, actual.QuickLaunchID, "should be equal")
			assert.Equal(expected.AddedBy, actual.AddedBy, "should be equal")
			assert.Equal(expected.AddedOn, actual.AddedOn, "should be equal")
		}
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestUpdateInstantLaunch(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	rows := sqlmock.NewRows([]string{"id", "quick_launch_id", "added_by", "added_on"}).
		AddRow("0", "0", "test@iplantcollaborative.org", "today")

	mock.ExpectQuery("UPDATE ONLY instant_launches").
		WillReturnRows(rows)

	actual, err := app.UpdateInstantLaunch(context.Background(), "0", "0")
	assert.NoError(err, "error should be nil")
	assert.Equal("0", actual.ID, "id should be 0")
	assert.Equal("0", actual.QuickLaunchID, "quick_launch_id should be 0")
	assert.Equal("test@iplantcollaborative.org", actual.AddedBy, "added_by should be test@iplantcollaborative.org")
	assert.Equal("today", actual.AddedOn, "added_on should be set to 'today'")
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestUpdateInstantLaunchHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	rows := sqlmock.NewRows([]string{"id", "quick_launch_id", "added_by", "added_on"}).
		AddRow("0", "0", "test@iplantcollaborative.org", "today")
	rows2 := sqlmock.NewRows([]string{"id", "quick_launch_id", "added_by", "added_on"}).
		AddRow("0", "0", "test@iplantcollaborative.org", "today")

	mock.ExpectQuery("SELECT i.id, i.quick_launch_id, i.added_by, i.added_on FROM instant_launches i").
		WillReturnRows(rows)
	mock.ExpectQuery("UPDATE ONLY instant_launches").
		WillReturnRows(rows2)

	expected := &InstantLaunch{
		QuickLaunchID: "0",
		AddedBy:       "test@iplantcollaborative.org",
	}

	v, err := json.Marshal(expected)
	assert.NoError(err, "should not error")

	req := httptest.NewRequest("POST", "http://localhost/instantlaunches/0?user=test", bytes.NewReader(v))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/:id")
	c.SetParamNames("id")
	c.SetParamValues("0")

	err = app.UpdateInstantLaunchHandler(c)
	if assert.NoError(err, "error should be nil") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := &InstantLaunch{}
		err = json.Unmarshal(rec.Body.Bytes(), &actual)
		if assert.NoError(err, "should be able to parse body") {
			assert.Equal(expected.QuickLaunchID, actual.QuickLaunchID, "should be equal")
			assert.Equal(expected.AddedBy, actual.AddedBy, "should be equal")
		}
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestDeleteInstantLaunch(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	mock.ExpectExec("DELETE FROM instant_launches").
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = app.DeleteInstantLaunch(context.Background(), "0")
	assert.NoError(err, "error should be nil")
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestDeleteInstantLaunchHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	rows := sqlmock.NewRows([]string{"id", "quick_launch_id", "added_by", "added_on"}).
		AddRow("0", "0", "test@iplantcollaborative.org", "today")
	mock.ExpectQuery("SELECT i.id, i.quick_launch_id, i.added_by, i.added_on FROM instant_launches i").
		WillReturnRows(rows)
	mock.ExpectExec("DELETE FROM instant_launches").
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := httptest.NewRequest("DELETE", "http://localhost/instantlaunches/0?user=test", nil)
	rec := httptest.NewRecorder()

	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/:id")
	c.SetParamNames("id")
	c.SetParamValues("0")

	err = app.DeleteInstantLaunchHandler(c)
	if assert.NoError(err, "should not error") {
		assert.Equal(http.StatusOK, rec.Code)
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestListInstantLaunches(t *testing.T) {
	assert := assert.New(t)

	app, mock, _, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := []InstantLaunch{
		{
			ID:            "0",
			QuickLaunchID: "0",
			AddedBy:       "test@iplantcollaborative.org",
			AddedOn:       "today",
		},
		{
			ID:            "1",
			QuickLaunchID: "1",
			AddedBy:       "test1@iplantcollaborative.org",
			AddedOn:       "yesterday",
		},
	}

	rows := sqlmock.NewRows([]string{"id", "quick_launch_id", "added_by", "added_on"}).
		AddRow(expected[0].ID, expected[0].QuickLaunchID, expected[0].AddedBy, expected[0].AddedOn).
		AddRow(expected[1].ID, expected[1].QuickLaunchID, expected[1].AddedBy, expected[1].AddedOn)

	mock.ExpectQuery("SELECT i.id, i.quick_launch_id, i.added_by, i.added_on FROM instant_launches i").
		WillReturnRows(rows)

	actual, err := app.ListInstantLaunches(context.Background())
	assert.NoError(err, "error should be nil")
	if assert.True(len(actual) > 0 && len(actual) == len(expected), "length is wrong") {
		for index := range expected {
			assert.True(cmp.Equal(expected[index], actual[index]), "should be equal")
		}
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}

func TestListInstantLaunchesHandler(t *testing.T) {
	assert := assert.New(t)

	app, mock, router, err := SetupApp()
	if err != nil {
		t.Fatalf("error setting up app: %s", err)
	}
	defer app.DB.Close()

	expected := []InstantLaunch{
		{
			ID:            "0",
			QuickLaunchID: "0",
			AddedBy:       "test@iplantcollaborative.org",
			AddedOn:       "today",
		},
		{
			ID:            "1",
			QuickLaunchID: "1",
			AddedBy:       "test1@iplantcollaborative.org",
			AddedOn:       "yesterday",
		},
	}

	rows := sqlmock.NewRows([]string{"id", "quick_launch_id", "added_by", "added_on"}).
		AddRow(expected[0].ID, expected[0].QuickLaunchID, expected[0].AddedBy, expected[0].AddedOn).
		AddRow(expected[1].ID, expected[1].QuickLaunchID, expected[1].AddedBy, expected[1].AddedOn)

	mock.ExpectQuery("SELECT i.id, i.quick_launch_id, i.added_by, i.added_on FROM instant_launches i").
		WillReturnRows(rows)

	req := httptest.NewRequest("GET", "http://localhost/instantlaunches/0", nil)
	rec := httptest.NewRecorder()

	c := router.NewContext(req, rec)
	c.SetPath("/instantlaunches/:id")
	c.SetParamNames("id")
	c.SetParamValues("0")

	err = app.ListInstantLaunchesHandler(c)
	if assert.NoError(err, "error should be nil") {
		assert.Equal(http.StatusOK, rec.Code)

		actual := []InstantLaunch{}
		err = json.Unmarshal(rec.Body.Bytes(), &actual)
		assert.NoError(err, "failed to unmarshal the response body")
		if assert.True(len(actual) > 0 && len(actual) == len(expected), "length is wrong") {
			for index := range expected {
				assert.True(cmp.Equal(expected[index], actual[index]), "should be equal")
			}
		}
	}
	assert.NoError(mock.ExpectationsWereMet(), "expectations were not met")
}
