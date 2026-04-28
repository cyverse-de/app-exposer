package db

import (
	"context"
	"time"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/messaging/v12"
	"github.com/jmoiron/sqlx"
)

// ReconcilerDB is the narrow subset of *Database operations used by the
// background reconciliation worker. It exists so the reconciler can be
// unit-tested against a fake without pulling in a real Postgres. The
// production *Database satisfies this interface structurally.
type ReconcilerDB interface {
	// ListOperators returns every configured operator ordered by priority.
	ListOperators(ctx context.Context) ([]Operator, error)

	// ClaimAndReconcile selects one operator whose last-reconciled timestamp
	// is older than reconciliationTTL, locks its row, and invokes fn within
	// the same transaction. Callbacks receive the claimed operator and the
	// active transaction; callers must thread tx through any DB methods
	// used inside fn.
	ClaimAndReconcile(ctx context.Context, hostname string, reconciliationTTL time.Duration, fn func(tx *sqlx.Tx, op *Operator) error) error

	// GetLatestStatusByExternalID returns the most recent status recorded in
	// job_status_updates for the given external ID. Returns sql.ErrNoRows
	// when no status has been recorded yet.
	GetLatestStatusByExternalID(ctx context.Context, tx *sqlx.Tx, externalID constants.ExternalID) (messaging.JobState, error)

	// InsertJobStatusUpdate appends a new row to job_status_updates.
	InsertJobStatusUpdate(ctx context.Context, tx *sqlx.Tx, update *JobStatusUpdate) error

	// URI returns the connection string used to open the database. The
	// reconciler uses it to open a dedicated LISTEN connection; returning
	// "" disables NOTIFY-driven syncs and falls back to periodic polling.
	URI() string
}
