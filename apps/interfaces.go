package apps

import "context"

// AnalysisStatusLookup is the narrow subset of *Apps used by the quota
// enforcer. Defined here so the enforcer can be unit-tested with a fake
// without pulling in a real Postgres. *Apps satisfies this interface
// structurally.
type AnalysisStatusLookup interface {
	// GetAnalysisStatus returns the analysis's current status string
	// (e.g. "Submitted", "Running", "Completed"). Returns sql.ErrNoRows
	// if no row exists for the given analysis ID.
	GetAnalysisStatus(ctx context.Context, analysisID string) (string, error)
}
