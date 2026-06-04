package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestDB returns a *Database backed by sqlmock so query-shape and
// argument-binding can be asserted without a live PostgreSQL instance.
// QueryMatcherEqual normalizes whitespace on both expected and actual
// statements internally, so multi-line SQL constants match without manual
// pre-processing — and a regex match that could mask unintended SQL
// drift is avoided.
func newTestDB(t *testing.T) (*Database, sqlmock.Sqlmock) {
	t.Helper()
	rawDB, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	sqlxDB := sqlx.NewDb(rawDB, "postgres")
	return New(sqlxDB, ""), mock
}

func TestDeleteOperatorByID(t *testing.T) {
	id := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	const expectedSQL = `DELETE FROM operators WHERE id = $1`

	tests := []struct {
		name        string
		execErr     error
		rowsCount   int64
		wantErrText string
	}{
		{
			name:      "deletes existing row",
			rowsCount: 1,
		},
		{
			// DELETE is idempotent: a missing UUID returns no error and
			// zero rows affected. Callers should not treat this as a 404.
			name:      "missing id is silent (zero rows)",
			rowsCount: 0,
		},
		{
			name:        "FK violation surfaces from driver",
			execErr:     &pq.Error{Code: "23503", Message: "violates foreign key constraint"},
			wantErrText: "violates foreign key constraint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, mock := newTestDB(t)
			expect := mock.ExpectExec(expectedSQL).WithArgs(id.String())
			if tt.execErr != nil {
				expect.WillReturnError(tt.execErr)
			} else {
				expect.WillReturnResult(sqlmock.NewResult(0, tt.rowsCount))
			}

			err := d.DeleteOperatorByID(context.Background(), id)

			if tt.wantErrText != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrText)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpdateOperatorByID(t *testing.T) {
	id := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	now := time.Now()

	const expectedSQL = `UPDATE operators SET name = COALESCE($2, name), url = COALESCE($3, url), tls_skip_verify = COALESCE($4, tls_skip_verify), priority = COALESCE($5, priority), base_url = COALESCE($6, base_url), accepting_launches = COALESCE($7, accepting_launches), deactivated = COALESCE($8, deactivated) WHERE id = $1 RETURNING id, name, url, tls_skip_verify, priority, base_url, accepting_launches, deactivated, last_reconciled_at, reconciled_by, created_at, updated_at`

	// returnedColumns matches the RETURNING list so StructScan can populate
	// the *Operator struct on the success path.
	returnedColumns := []string{
		"id", "name", "url", "tls_skip_verify", "priority", "base_url",
		"accepting_launches", "deactivated",
		"last_reconciled_at", "reconciled_by", "created_at", "updated_at",
	}

	strPtr := func(s string) *string { return &s }
	boolPtr := func(b bool) *bool { return &b }
	intPtr := func(i int) *int { return &i }

	tests := []struct {
		name string
		upd  OperatorUpdate
		// wantArgs is the argument slice in the order the production query
		// binds them: [id, name, url, tls_skip_verify, priority, base_url,
		// accepting_launches, deactivated]. Any driver.Value here is matched
		// literally; nil represents SQL NULL.
		wantArgs []driver.Value
		// dbErr is what sqlmock returns; nil means "succeed and return a row".
		dbErr error
	}{
		{
			name:     "single field — priority only",
			upd:      OperatorUpdate{Priority: intPtr(7)},
			wantArgs: []driver.Value{id.String(), nil, nil, nil, int64(7), nil, nil, nil},
		},
		{
			name:     "rename only",
			upd:      OperatorUpdate{Name: strPtr("renamed")},
			wantArgs: []driver.Value{id.String(), "renamed", nil, nil, nil, nil, nil, nil},
		},
		{
			name:     "base_url only",
			upd:      OperatorUpdate{BaseURL: strPtr("https://a.cyverse.run")},
			wantArgs: []driver.Value{id.String(), nil, nil, nil, nil, "https://a.cyverse.run", nil, nil},
		},
		{
			name:     "drain — accepting_launches only",
			upd:      OperatorUpdate{AcceptingLaunches: boolPtr(false)},
			wantArgs: []driver.Value{id.String(), nil, nil, nil, nil, nil, false, nil},
		},
		{
			name:     "deactivate — deactivated only",
			upd:      OperatorUpdate{Deactivated: boolPtr(true)},
			wantArgs: []driver.Value{id.String(), nil, nil, nil, nil, nil, nil, true},
		},
		{
			name: "all fields",
			upd: OperatorUpdate{
				Name:              strPtr("a"),
				URL:               strPtr("https://a.example.com"),
				TLSSkipVerify:     boolPtr(true),
				Priority:          intPtr(2),
				BaseURL:           strPtr("https://a.cyverse.run"),
				AcceptingLaunches: boolPtr(false),
				Deactivated:       boolPtr(true),
			},
			wantArgs: []driver.Value{id.String(), "a", "https://a.example.com", true, int64(2), "https://a.cyverse.run", false, true},
		},
		{
			name:     "no row matches — surfaces sql.ErrNoRows from RETURNING",
			upd:      OperatorUpdate{Priority: intPtr(1)},
			wantArgs: []driver.Value{id.String(), nil, nil, nil, int64(1), nil, nil, nil},
			dbErr:    sql.ErrNoRows,
		},
		{
			name:     "unique violation — surfaces *pq.Error 23505",
			upd:      OperatorUpdate{Name: strPtr("dup")},
			wantArgs: []driver.Value{id.String(), "dup", nil, nil, nil, nil, nil, nil},
			dbErr:    &pq.Error{Code: "23505", Message: "duplicate key value violates unique constraint"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, mock := newTestDB(t)

			expect := mock.ExpectQuery(expectedSQL).
				WithArgs(tt.wantArgs...)

			if tt.dbErr != nil {
				expect.WillReturnError(tt.dbErr)
			} else {
				rows := sqlmock.NewRows(returnedColumns).
					AddRow(id, "n", "https://u", false, 0, sql.NullString{}, true, false, sql.NullTime{}, sql.NullString{}, now, now)
				expect.WillReturnRows(rows)
			}

			got, err := d.UpdateOperatorByID(context.Background(), id, tt.upd)

			switch {
			case errors.Is(tt.dbErr, sql.ErrNoRows):
				assert.ErrorIs(t, err, sql.ErrNoRows)
				assert.Nil(t, got)
			case tt.dbErr != nil:
				require.Error(t, err)
				var pqErr *pq.Error
				assert.True(t, errors.As(err, &pqErr), "error should be a *pq.Error")
				assert.Equal(t, pq.ErrorCode("23505"), pqErr.Code)
				assert.Nil(t, got)
			default:
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, id, got.ID)
			}

			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
