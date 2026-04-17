package apps

import (
	"context"

	"github.com/cyverse-de/app-exposer/constants"
)

// AnalysisStatusLookup is the narrow subset of *Apps used by the quota
// enforcer. Defined here so the enforcer can be unit-tested with a fake
// without pulling in a real Postgres. *Apps satisfies this interface
// structurally.
type AnalysisStatusLookup interface {
	// GetAnalysisStatus returns the analysis's current status string
	// (e.g. "Submitted", "Running", "Completed"). Returns sql.ErrNoRows
	// if no row exists for the given analysis ID.
	GetAnalysisStatus(ctx context.Context, analysisID constants.AnalysisID) (string, error)
}

// OperatorNameLookup is the narrow subset of *Apps used by the reconciler
// to back-fill missing operator_name records. Defined here so the
// reconciler can be unit-tested with a fake. *Apps satisfies this
// interface structurally.
type OperatorNameLookup interface {
	// GetOperatorName returns the operator name currently recorded for
	// the analysis, or "" with nil error if no row exists or the column
	// is NULL.
	GetOperatorName(ctx context.Context, analysisID constants.AnalysisID) (string, error)

	// SetOperatorName records the operator running the analysis.
	// Internally retries a handful of times if the jobs row isn't yet
	// visible (handles the launch/commit race).
	SetOperatorName(ctx context.Context, analysisID constants.AnalysisID, operatorName string) error
}
