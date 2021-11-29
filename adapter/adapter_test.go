package adapter

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cyverse-de/jex-adapter/db"
	"github.com/cyverse-de/jex-adapter/millicores"
	"github.com/jmoiron/sqlx"
	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"gopkg.in/cyverse-de/model.v4"

	_ "github.com/proullon/ramsql/driver"
)

func getTestConfig() *viper.Viper {
	cfg := viper.New()
	cfg.Set("condor.log_path", "/tmp/")
	cfg.Set("condor.filter_files", "output-last-stderr")
	cfg.Set("irods.base", "/iplant/home")
	return cfg
}

func getTestDatabaseConn(t *testing.T) *sqlx.DB {
	db, err := sqlx.Open("ramsql", "TestDatabase")
	if err != nil {
		t.Fatalf("error opening test database: %s\n", err)
	}
	defer db.Close()

	batch := []string{
		`CREATE TABLE jobs (id VARCHAR(36) PRIMARY KEY, millicores INT);`,
		`INSERT INTO jobs (id) VALUES ('c654e8bb-d535-4f7a-bd0f-aff0f0c189b1');`,
	}

	for _, b := range batch {
		_, err = db.Exec(b)
		if err != nil {
			t.Fatalf("error executing sql command: %s\n", err)
		}
	}

	return db
}

type TestMessenger struct{}

func (t *TestMessenger) Stop(id string) error {
	return nil
}

func (t *TestMessenger) Launch(job *model.Job) error {
	return nil
}

func initTestAdapter(t *testing.T) *JEXAdapter {
	cfg := getTestConfig()
	msger := &TestMessenger{}
	dbconn := getTestDatabaseConn(t)
	dbase := db.New(dbconn)
	detector := millicores.New(dbase, 4000.0)
	a := New(cfg, detector, msger)
	return a
}
func TestHomeHandler(t *testing.T) {
	a := initTestAdapter(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	e := echo.New()
	c := e.NewContext(req, rec)

	if assert.NoError(t, a.HomeHandler(c)) {
		assert.Equal(t, rec.Body.String(), "Welcome to the JEX.\n")
	}
}
