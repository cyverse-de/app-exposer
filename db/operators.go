package db

import (
	"context"
	"time"

	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Operator models the operators table.
type Operator struct {
	ID                    uuid.UUID  `db:"id"`
	Name                  string     `db:"name"`
	URL                   string     `db:"url"`
	Insecure              bool       `db:"insecure"`
	AuthUser              string     `db:"auth_user"`
	AuthPasswordEncrypted string     `db:"auth_password_encrypted"`
	LastReconciledAt      *time.Time `db:"last_reconciled_at"`
	ReconciledBy          *string    `db:"reconciled_by"`
	CreatedAt             time.Time  `db:"created_at"`
	UpdatedAt             time.Time  `db:"updated_at"`
}

// JobStatusUpdate models a row in the job_status_updates table.
type JobStatusUpdate struct {
	ExternalID       string `db:"external_id"`
	Message          string `db:"message"`
	Status           string `db:"status"`
	SentFrom         string `db:"sent_from"`
	SentFromHostname string `db:"sent_from_hostname"`
	SentOn           int64  `db:"sent_on"`
}

// ToOperatorConfig converts a DB Operator model to the operatorclient.OperatorConfig type.
func (o *Operator) ToOperatorConfig(password string) operatorclient.OperatorConfig {
	return operatorclient.OperatorConfig{
		Name:     o.Name,
		URL:      o.URL,
		Username: o.AuthUser,
		Password: password,
		Insecure: o.Insecure,
	}
}

// ListOperators returns all operators from the database, ordered by creation time.
func (d *Database) ListOperators(ctx context.Context) ([]Operator, error) {
	var ops []Operator
	const query = `
		SELECT id, name, url, insecure, auth_user, auth_password_encrypted,
		       last_reconciled_at, reconciled_by, created_at, updated_at
		FROM operators
		ORDER BY created_at ASC
	`
	err := d.db.SelectContext(ctx, &ops, query)
	return ops, err
}

// ClaimAndReconcile atomically claims an operator that hasn't been reconciled
// recently and calls the provided function while the row lock is held. On
// success it updates last_reconciled_at before committing the transaction.
// The FOR UPDATE SKIP LOCKED clause ensures that concurrent replicas never
// claim the same operator.
func (d *Database) ClaimAndReconcile(ctx context.Context, hostname string, timeout time.Duration, fn func(tx *sqlx.Tx, op *Operator) error) error {
	tx, err := d.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is a no-op

	const claimQuery = `
		SELECT id, name, url, insecure, auth_user, auth_password_encrypted,
		       last_reconciled_at, reconciled_by, created_at, updated_at
		FROM operators
		WHERE last_reconciled_at IS NULL OR last_reconciled_at < $1
		ORDER BY last_reconciled_at ASC NULLS FIRST
		FOR UPDATE SKIP LOCKED
		LIMIT 1
	`
	cutoff := time.Now().Add(-timeout)
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
func (d *Database) GetAnalysisStatus(ctx context.Context, tx *sqlx.Tx, analysisID string) (string, error) {
	var status string
	const query = "SELECT status FROM jobs WHERE id = $1"
	err := tx.GetContext(ctx, &status, query, analysisID)
	return status, err
}
