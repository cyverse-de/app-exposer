package db

import (
	"context"
	"database/sql"

	"github.com/jmoiron/sqlx"
)

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

func (d *Database) SetMillicoresReserved(context context.Context, externalID string, millicoresReserved float32) error {
	const stmt = `UPDATE jobs j SET millicores_reserved = $2 WHERE id = (SELECT job_id FROM job_steps WHERE external_id = $1);`
	_, err := d.db.ExecContext(context, stmt, externalID, millicoresReserved)
	return err
}
