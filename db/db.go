package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/cyverse-de/jex-adapter/logging"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

var log = logging.Log.WithFields(logrus.Fields{"package": "db"})

// DatabaseAccessor is an interface for iteracting with a database. Its main
// reason for existing is to abstract out whether a transaction is being used.
type DatabaseAccessor interface {
	QueryRowxContext(context.Context, string, ...interface{}) *sqlx.Row
	QueryxContext(context.Context, string, ...interface{}) (*sqlx.Rows, error)
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
}

type Database struct {
	db DatabaseAccessor
}

func New(db DatabaseAccessor) *Database {
	return &Database{
		db: db,
	}
}

func (d *Database) SetMillicoresReserved(context context.Context, externalID string, millicoresReserved float64) error {
	log = log.WithFields(logrus.Fields{"context": "set millicores reserved", "externalID": externalID, "millicoresReserved": millicoresReserved})

	const stmt = `
		UPDATE jobs 
		SET millicores_reserved = $2
		FROM ( SELECT job_id FROM job_steps WHERE external_id = $1 ) AS sub 
		WHERE jobs.id = sub.job_id;
	`

	converted := int64(millicoresReserved)
	log.Debugf("converted millicores values %d", converted)

	time.Sleep(30 * time.Second)

	result, err := d.db.ExecContext(context, stmt, externalID, converted)
	if err != nil {
		log.Error(err)
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		log.Error(err)
		return err
	}

	log.Debugf("rows affected %d", rowsAffected)

	return err
}
