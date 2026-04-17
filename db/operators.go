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
	ID               uuid.UUID  `db:"id"`
	Name             string     `db:"name"`
	URL              string     `db:"url"`
	TLSSkipVerify    bool       `db:"tls_skip_verify"`
	Priority         int        `db:"priority"`
	LastReconciledAt *time.Time `db:"last_reconciled_at"`
	ReconciledBy     *string    `db:"reconciled_by"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"`
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
	}
}

// ListOperators returns all operators from the database, ordered by priority
// (lower values first) with creation time as tiebreaker.
func (d *Database) ListOperators(ctx context.Context) ([]Operator, error) {
	var ops []Operator
	const query = `
		SELECT id, name, url, tls_skip_verify, priority,
		       last_reconciled_at, reconciled_by, created_at, updated_at
		FROM operators
		ORDER BY priority ASC, created_at ASC
	`
	err := d.db.SelectContext(ctx, &ops, query)
	return ops, err
}

// ListOperatorSummaries returns the public (non-sensitive) fields of every
// operator, ordered by priority (lower values first) with creation time as
// tiebreaker. The returned type is operatorclient.OperatorConfig rather
// than a db-local struct because that type already carries the same four
// fields and serves as the canonical public shape; having a second struct
// here would invite drift. sqlx can scan directly into OperatorConfig via
// its db struct tags.
func (d *Database) ListOperatorSummaries(ctx context.Context) ([]operatorclient.OperatorConfig, error) {
	ops := make([]operatorclient.OperatorConfig, 0)
	const query = `
		SELECT name, url, tls_skip_verify, priority
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
		INSERT INTO operators (name, url, tls_skip_verify, priority)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, url, tls_skip_verify, priority,
		          last_reconciled_at, reconciled_by, created_at, updated_at
	`
	var created Operator
	err := d.db.QueryRowxContext(ctx, query, op.Name, op.URL, op.TLSSkipVerify, op.Priority).StructScan(&created)
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// DeleteOperatorByName deletes the operator with the given name. It is
// idempotent for operators with no associated jobs: deleting a non-existent
// operator succeeds silently. Deleting an operator that still has jobs
// referencing it will fail due to a foreign key constraint.
func (d *Database) DeleteOperatorByName(ctx context.Context, name string) error {
	const query = `DELETE FROM operators WHERE name = $1`
	_, err := d.db.ExecContext(ctx, query, name)
	return err
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
		SELECT id, name, url, tls_skip_verify, priority,
		       last_reconciled_at, reconciled_by, created_at, updated_at
		FROM operators
		WHERE last_reconciled_at IS NULL OR last_reconciled_at < $1
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
