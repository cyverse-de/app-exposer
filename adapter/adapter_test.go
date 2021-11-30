package adapter

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cyverse-de/jex-adapter/db"
	"github.com/cyverse-de/jex-adapter/millicores"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"gopkg.in/cyverse-de/model.v4"

	"github.com/DATA-DOG/go-sqlmock"
)

func getTestConfig() *viper.Viper {
	cfg := viper.New()
	cfg.Set("condor.log_path", "/tmp/")
	cfg.Set("condor.filter_files", "output-last-stderr")
	cfg.Set("irods.base", "/iplant/home")
	return cfg
}

type TestMessenger struct{}

func (t *TestMessenger) Stop(id string) error {
	return nil
}

func (t *TestMessenger) Launch(job *model.Job) error {
	return nil
}

var a *JEXAdapter
var mock sqlmock.Sqlmock

func initTestAdapter(t *testing.T) (*JEXAdapter, sqlmock.Sqlmock) {
	if a == nil || mock == nil {
		var err error
		var mockconn *sql.DB
		cfg := getTestConfig()
		msger := &TestMessenger{}
		mockconn, mock, err = sqlmock.New()
		if err != nil {
			t.Fatalf("error opening mocked database connection: %s", err)
		}
		dbconn := sqlx.NewDb(mockconn, "postgres")
		dbase := db.New(dbconn)
		detector := millicores.New(dbase, 4000.0)
		a = New(cfg, detector, msger)
	}
	return a, mock
}
func TestHomeHandler(t *testing.T) {
	a, _ := initTestAdapter(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	if assert.NoError(t, a.HomeHandler(c)) {
		assert.Equal(t, rec.Body.String(), "Welcome to the JEX.\n")
	}
}

func TestStopAdapter(t *testing.T) {
	a, _ := initTestAdapter(t)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)
	c.SetPath("/stop/:invocation_id")
	c.SetParamNames("invocation_id")
	c.SetParamValues("c654e8bb-d535-4f7a-bd0f-aff0f0c189b1")

	if assert.NoError(t, a.StopHandler(c)) {
		assert.Equal(t, rec.Code, http.StatusOK)
	}
}

func TestLaunchHandler(t *testing.T) {
	a, mock := initTestAdapter(t)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(testCondorLaunchJSON))
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	mock.ExpectExec("UPDATE jobs").WillReturnResult(sqlmock.NewResult(1, 1))

	if assert.NoError(t, a.LaunchHandler(c)) {
		assert.Equal(t, rec.Code, http.StatusOK)
		assert.NoError(t, mock.ExpectationsWereMet())
	}
}

func TestCondorLaunchDefaultMillicores(t *testing.T) {
	a, mock := initTestAdapter(t)

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(testCondorCustomLaunchJSON))
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	mock.ExpectExec("UPDATE jobs").
		WithArgs("07b04ce2-7757-4b21-9e15-0b4c2f44be26", 64000.0).
		WillReturnResult(sqlmock.NewResult(1, 1))

	if assert.NoError(t, a.LaunchHandler(c)) {
		assert.Equal(t, rec.Code, http.StatusOK)
		assert.NoError(t, mock.ExpectationsWereMet())
	}
}
