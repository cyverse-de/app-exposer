package db

import (
	"context"
	"time"

	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/messaging/v12"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Operator models the operators table.
type Operator struct {
	ID                uuid.UUID  `db:"id"`
	Name              string     `db:"name"`
	URL               string     `db:"url"`
	TLSSkipVerify     bool       `db:"tls_skip_verify"`
	Priority          int        `db:"priority"`
	BaseURL           *string    `db:"base_url"`
	AcceptingLaunches bool       `db:"accepting_launches"`
	Deactivated       bool       `db:"deactivated"`
	LastReconciledAt  *time.Time `db:"last_reconciled_at"`
	ReconciledBy      *string    `db:"reconciled_by"`
	CreatedAt         time.Time  `db:"created_at"`
	UpdatedAt         time.Time  `db:"updated_at"`
}

// JobStatusUpdate models a row in the job_status_updates table. The
// Status field carries a messaging.JobState so the DE-wide vocabulary
// is enforced by the compiler at the write boundary (the messaging
// package already defines it; adopting the upstream type avoids a
// parallel enum here).
type JobStatusUpdate struct {
	ExternalID       constants.ExternalID `db:"external_id"`
	Message          string               `db:"message"`
	Status           messaging.JobState   `db:"status"`
	SentFrom         string               `db:"sent_from"`
	SentFromHostname string               `db:"sent_from_hostname"`
	SentOn           int64                `db:"sent_on"`
}

// ToOperatorConfig projects the DB's full Operator row down to the public
// OperatorConfig shape. Priority is preserved — earlier versions of this
// method dropped it, which happened to work only because ListOperators
// returns rows in priority order and the scheduler trusted slice order.
// Carrying Priority through the type system makes the invariant explicit.
func (o *Operator) ToOperatorConfig() operatorclient.OperatorConfig {
	return operatorclient.OperatorConfig{
		Name:          o.Name,
		URL:           o.URL,
		TLSSkipVerify: o.TLSSkipVerify,
		Priority:      o.Priority,
		BaseURL:       o.BaseURL,
	}
}

// ToOperatorAdminSummary projects the DB's full Operator row down to the
// admin-facing summary shape.
// Reuses ToOperatorConfig for the embedded config so the projection stays
// in lock-step if OperatorConfig gains a new field. Used by handlers that
// return a single row's identity to admin clients — notably create and
// update, where the caller needs the row's id without an extra list call.
func (o *Operator) ToOperatorAdminSummary() operatorclient.OperatorAdminSummary {
	return operatorclient.OperatorAdminSummary{
		ID:                o.ID,
		OperatorConfig:    o.ToOperatorConfig(),
		AcceptingLaunches: o.AcceptingLaunches,
		Deactivated:       o.Deactivated,
	}
}

// ListOperators returns the operators eligible for the live scheduler pool,
// ordered by priority (lower values first) with creation time as tiebreaker.
// Deactivated operators are excluded entirely: they take no launches, are not
// reconciled, and drop out of every scheduler.Clients() fan-out (listing,
// search, quota, capacity). Drained operators (accepting_launches=false) are
// retained here — they stay in service and are skipped only at launch time.
func (d *Database) ListOperators(ctx context.Context) ([]Operator, error) {
	var ops []Operator
	const query = `
		SELECT id, name, url, tls_skip_verify, priority, base_url,
		       accepting_launches, deactivated,
		       last_reconciled_at, reconciled_by, created_at, updated_at
		FROM operators
		WHERE deactivated = false
		ORDER BY priority ASC, created_at ASC
	`
	err := d.db.SelectContext(ctx, &ops, query)
	return ops, err
}

// GetOperatorByID returns the operator row with the given id, or sql.ErrNoRows
// when no such row exists. Returning the typed not-found lets callers map the
// miss to a 404 without string-matching the error.
func (d *Database) GetOperatorByID(ctx context.Context, id uuid.UUID) (*Operator, error) {
	var op Operator
	const query = `
		SELECT id, name, url, tls_skip_verify, priority, base_url,
		       accepting_launches, deactivated,
		       last_reconciled_at, reconciled_by, created_at, updated_at
		FROM operators
		WHERE id = $1
	`
	if err := d.db.GetContext(ctx, &op, query, id); err != nil {
		return nil, err
	}
	return &op, nil
}

// ListOperatorAdminSummaries returns the admin-listing fields of every
// operator, ordered by priority (lower values first) with creation time as
// tiebreaker. Including id lets admin clients address an operator by its
// stable UUID rather than by name, which is important for PATCH where the
// operator may be renamed.
func (d *Database) ListOperatorAdminSummaries(ctx context.Context) ([]operatorclient.OperatorAdminSummary, error) {
	ops := make([]operatorclient.OperatorAdminSummary, 0)
	const query = `
		SELECT id, name, url, tls_skip_verify, priority, base_url,
		       accepting_launches, deactivated
		FROM operators
		ORDER BY priority ASC, created_at ASC
	`
	err := d.db.SelectContext(ctx, &ops, query)
	return ops, err
}

// InsertOperator inserts a new operator into the database and returns the
// created row with DB-generated fields (id, created_at, updated_at) populated.
func (d *Database) InsertOperator(ctx context.Context, op *Operator) (*Operator, error) {
	const query = `
		INSERT INTO operators (name, url, tls_skip_verify, priority, base_url)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id, name, url, tls_skip_verify, priority, base_url,
		          accepting_launches, deactivated,
		          last_reconciled_at, reconciled_by, created_at, updated_at
	`
	var created Operator
	err := d.db.QueryRowxContext(ctx, query, op.Name, op.URL, op.TLSSkipVerify, op.Priority, op.BaseURL).StructScan(&created)
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// DeleteOperatorByID deletes the operator with the given UUID. It is
// idempotent for operators with no associated jobs: deleting a non-existent
// operator succeeds silently because DELETE simply affects zero rows.
// Deleting an operator that still has jobs referencing it will fail due to
// the jobs.operator_id FK with ON DELETE RESTRICT.
func (d *Database) DeleteOperatorByID(ctx context.Context, id uuid.UUID) error {
	const query = `DELETE FROM operators WHERE id = $1`
	_, err := d.db.ExecContext(ctx, query, id)
	return err
}

// OperatorUpdate carries the optional fields for a partial-update of an
// operator row. A nil pointer leaves the column unchanged. The name, url,
// tls_skip_verify, priority, accepting_launches, and deactivated columns are
// covered by the AFTER UPDATE NOTIFY trigger so scheduler-relevant changes
// resync immediately; base_url is not (it is consumed by the apps service, not
// app-exposer's scheduler). Reconciliation columns are intentionally
// reconciler-only and not exposed here.
//
// Because UpdateOperatorByID applies fields with COALESCE, a nil pointer
// means "leave unchanged" — there is no way to clear base_url back to NULL
// once set. That is intentional: base_url is required from creation onward,
// so the only NULL rows are legacy operators that predate the column.
type OperatorUpdate struct {
	Name              *string
	URL               *string
	TLSSkipVerify     *bool
	Priority          *int
	BaseURL           *string
	AcceptingLaunches *bool
	Deactivated       *bool
}

// UpdateOperatorByID applies a partial update to the operator row identified
// by id and returns the resulting row. Unset (nil) fields in upd are left
// unchanged via COALESCE on typed-NULL parameters. updated_at is maintained
// by a BEFORE UPDATE trigger on the table, so the SQL does not set it.
//
// Returns sql.ErrNoRows if no operator with the given id exists. Returns
// the underlying *pq.Error (with Code 23505) on UNIQUE collisions for name
// or url so the handler layer can map it to 409 Conflict.
//
// The AFTER UPDATE OF (name, url, tls_skip_verify, priority,
// accepting_launches, deactivated) trigger on the operators table fires
// NOTIFY operator_changed automatically, which the reconciler's pq.Listener
// picks up to drive an immediate scheduler resync — no explicit pg_notify
// call is required here.
func (d *Database) UpdateOperatorByID(ctx context.Context, id uuid.UUID, upd OperatorUpdate) (*Operator, error) {
	const query = `
		UPDATE operators
		SET name               = COALESCE($2, name),
		    url                = COALESCE($3, url),
		    tls_skip_verify    = COALESCE($4, tls_skip_verify),
		    priority           = COALESCE($5, priority),
		    base_url           = COALESCE($6, base_url),
		    accepting_launches = COALESCE($7, accepting_launches),
		    deactivated        = COALESCE($8, deactivated)
		WHERE id = $1
		RETURNING id, name, url, tls_skip_verify, priority, base_url,
		          accepting_launches, deactivated,
		          last_reconciled_at, reconciled_by, created_at, updated_at
	`
	var updated Operator
	err := d.db.QueryRowxContext(ctx, query, id, upd.Name, upd.URL, upd.TLSSkipVerify, upd.Priority, upd.BaseURL, upd.AcceptingLaunches, upd.Deactivated).StructScan(&updated)
	if err != nil {
		return nil, err
	}
	return &updated, nil
}

// ClaimAndReconcile atomically claims an operator that hasn't been reconciled
// recently and calls the provided function while the row lock is held. On
// success it updates last_reconciled_at before committing the transaction.
// The FOR UPDATE SKIP LOCKED clause ensures that concurrent replicas never
// claim the same operator.
func (d *Database) ClaimAndReconcile(ctx context.Context, hostname string, reconciliationTTL time.Duration, fn func(tx *sqlx.Tx, op *Operator) error) error {
	tx, err := d.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	const claimQuery = `
		SELECT id, name, url, tls_skip_verify, priority, base_url,
		       accepting_launches, deactivated,
		       last_reconciled_at, reconciled_by, created_at, updated_at
		FROM operators
		WHERE deactivated = false
		  AND (last_reconciled_at IS NULL OR last_reconciled_at < $1)
		ORDER BY last_reconciled_at ASC NULLS FIRST
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`
	cutoff := time.Now().Add(-reconciliationTTL)
	var op Operator
	if err := tx.GetContext(ctx, &op, claimQuery, cutoff); err != nil {
		return err
	}

	// Run the caller's reconciliation logic while the lock is held.
	if err := fn(tx, &op); err != nil {
		return err
	}

	// Mark the operator as reconciled.
	const updateQuery = `
		UPDATE operators
		SET last_reconciled_at = now(),
		    reconciled_by = $2,
		    updated_at = now()
		WHERE id = $1
	`
	if _, err := tx.ExecContext(ctx, updateQuery, op.ID, hostname); err != nil {
		return err
	}

	return tx.Commit()
}

// InsertJobStatusUpdate inserts a new row into the job_status_updates table.
func (d *Database) InsertJobStatusUpdate(ctx context.Context, tx *sqlx.Tx, update *JobStatusUpdate) error {
	const query = `
		INSERT INTO job_status_updates (external_id, message, status, sent_from, sent_from_hostname, sent_on)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := tx.ExecContext(ctx, query,
		update.ExternalID,
		update.Message,
		update.Status,
		update.SentFrom,
		update.SentFromHostname,
		update.SentOn,
	)
	return err
}

// GetAnalysisStatus returns the current status of an analysis from the jobs table.
func (d *Database) GetAnalysisStatus(ctx context.Context, tx *sqlx.Tx, analysisID constants.AnalysisID) (string, error) {
	var status string
	const query = "SELECT status FROM jobs WHERE id = $1"
	err := tx.GetContext(ctx, &status, query, analysisID)
	return status, err
}

// GetLatestStatusByExternalID returns the most recent status from the
// job_status_updates table for the given external ID. This is more accurate
// than querying the jobs table directly because there can be lag between
// when a status update is recorded and when the jobs table is updated.
func (d *Database) GetLatestStatusByExternalID(ctx context.Context, tx *sqlx.Tx, externalID constants.ExternalID) (messaging.JobState, error) {
	var status messaging.JobState
	const query = `
		SELECT status FROM job_status_updates
		WHERE external_id = $1
		ORDER BY sent_on DESC
		LIMIT 1
	`
	err := tx.GetContext(ctx, &status, query, externalID)
	return status, err
}
