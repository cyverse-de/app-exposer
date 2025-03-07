package db

import (
	"context"
	"database/sql"
	"time"

	"github.com/cockroachdb/apd"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel"
)

var log = common.Log

const otelName = "github.com/cyverse-de/jex-adapter/db"

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

func (d *Database) SetMillicoresReservedByAnalysisID(ctx context.Context, analysisID string, millicoresReserved *apd.Decimal) error {
	var err error

	ctx, span := otel.Tracer(otelName).Start(ctx, "SetMillicoresReservedByAnalysisID")
	defer span.End()

	log = log.WithFields(logrus.Fields{
		"context":            "set millicores reserved",
		"analysisID":         analysisID,
		"millicoresReserved": millicoresReserved.String(),
	})

	const stmt = `
		UPDATE jobs
		SET millicores_reserved = $2
		WHERE jobs.id = $1
	`

	log.Infof("job ID is %s", analysisID)

	converted, err := millicoresReserved.Int64()
	if err != nil {
		return err
	}
	log.Debugf("converted millicores values %d", converted)

	result, err := d.db.ExecContext(ctx, stmt, analysisID, converted)
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

func (d *Database) SetMillicoresReserved(context context.Context, externalID string, millicoresReserved *apd.Decimal) error {
	var (
		err   error
		jobID string
	)

	ctx, span := otel.Tracer(otelName).Start(context, "SetMillicoresReserved")
	defer span.End()

	const jobIDQuery = `
		SELECT job_id
		FROM job_steps
		WHERE external_id = $1;
	`
	log.Debug("looking up job ID")
	for i := 0; i < 30; i++ {
		if err = d.db.QueryRowxContext(ctx, jobIDQuery, externalID).Scan(&jobID); err != nil {
			if err == sql.ErrNoRows {
				time.Sleep(2 * time.Second)
				continue
			} else {
				log.Error(err)
				return err
			}
		}
	}
	log.Debug("done looking up job ID")

	return d.SetMillicoresReservedByAnalysisID(ctx, jobID, millicoresReserved)
}
