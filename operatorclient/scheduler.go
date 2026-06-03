package operatorclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"sync"

	"github.com/google/uuid"
	"golang.org/x/oauth2"
)

// isTransientLaunchError reports whether a launch failure represents a
// transient operator-side problem (network blip, 5xx) that warrants trying
// the next operator, rather than a client-side bug that should abort. Used
// by LaunchAnalysis to extend the 409-fallthrough behavior to the cases
// where retrying on a peer is likely to succeed.
func isTransientLaunchError(err error) bool {
	if err == nil {
		return false
	}
	// Our own HTTPStatusError exposes a Transient predicate that returns
	// true for 5xx; anything 4xx (other than 409, which the caller has
	// already mapped to ErrCapacityExhausted) is a request we built
	// wrong and should not be retried.
	var se *HTTPStatusError
	if errors.As(err, &se) {
		return se.Transient()
	}
	// Context cancellation is a policy signal from the caller; it means
	// abandon everything, not try the next operator.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Network-level failures (DNS, connection refused, read/write timeouts)
	// didn't reach the operator's application code at all — try the next.
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		var netErr net.Error
		if errors.As(urlErr.Err, &netErr) {
			return true
		}
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

// Sentinel errors for scheduler failure modes.
var (
	// ErrNoOperators means the scheduler has no operators configured.
	ErrNoOperators = errors.New("no operators configured")

	// ErrAllOperatorsExhausted means every operator was either at capacity
	// or returned a 409 Conflict during the launch attempt.
	ErrAllOperatorsExhausted = errors.New("all operators at capacity")

	// ErrAllOperatorsDraining means every operator in the pool is draining
	// (accepting_launches=false), so none can take a new launch. Distinct
	// from ErrAllOperatorsExhausted so callers can tell a deliberate drain
	// apart from a genuine capacity shortfall.
	ErrAllOperatorsDraining = errors.New("all operators draining (not accepting launches)")

	// ErrNoCompatibleOperator means every operator with capacity reported
	// a GPU vendor incompatible with the analysis's request. Distinct from
	// ErrAllOperatorsExhausted so callers can tell a routing-policy failure
	// apart from a capacity failure.
	ErrNoCompatibleOperator = errors.New("no operator with compatible GPU vendor")

	// ErrNoCompatibleModel means the analysis requested specific GPU
	// model(s), at least one operator was skipped for advertising a
	// SupportedGPUModels list that does not intersect them, and no operator
	// could run it. Distinct from ErrNoCompatibleOperator so the caller can
	// tell "wrong vendor" apart from "right vendor, wrong model".
	ErrNoCompatibleModel = errors.New("no operator advertises a compatible GPU model")
)

// vendorCompatible reports whether an operator advertising vendor `have`
// can run a bundle requesting vendor `want`. An empty `want` means the
// bundle has no GPU requirement (any operator works). An empty `have`
// means the operator pre-dates the GPUVendor field or has no GPU
// configured; treated as compatible for backwards compatibility.
func vendorCompatible(want, have string) bool {
	if want == "" || have == "" {
		return true
	}
	return want == have
}

// modelCompatible reports whether an operator advertising `have` GPU
// models can run a bundle requesting any of `want`. An empty `want`
// means the bundle has no GPU model preference (any operator works).
// An empty `have` means the operator does not filter on model (pre-
// upgrade or model-agnostic by config); treated as compatible for
// backwards compatibility.
func modelCompatible(want, have []string) bool {
	if len(want) == 0 || len(have) == 0 {
		return true
	}
	supported := make(map[string]struct{}, len(have))
	for _, m := range have {
		supported[m] = struct{}{}
	}
	for _, m := range want {
		if _, ok := supported[m]; ok {
			return true
		}
	}
	return false
}

// Scheduler manages a priority-ordered list of operator clients and
// routes analyses to the first operator with available capacity.
type Scheduler struct {
	mu          sync.RWMutex
	operators   []*Client
	tokenSource oauth2.TokenSource
}

// NewScheduler creates an empty Scheduler. The reconciler's first
// SyncOperators call populates it from the database, which is the source
// of truth for operator configuration. The token source is used to
// authenticate requests to all operators; pass nil to disable auth.
func NewScheduler(ts oauth2.TokenSource) *Scheduler {
	return &Scheduler{tokenSource: ts}
}

// SetTokenSource updates the token source used for authenticating requests
// to operators. Subsequent calls to Sync will use the new token source.
func (s *Scheduler) SetTokenSource(ts oauth2.TokenSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenSource = ts
}

// Sync replaces the scheduler's current operator clients with a new list
// built from the DB-backed admin summaries (each carrying its UUID). This
// allows runtime updates from the database without a restart. Each client
// remembers its id so callers can look it up via ClientByID even after a
// rename.
func (s *Scheduler) Sync(summaries []OperatorAdminSummary) error {
	// Snapshot the token source under a read-lock so that a concurrent
	// SetTokenSource call cannot race with our read of s.tokenSource.
	s.mu.RLock()
	ts := s.tokenSource
	s.mu.RUnlock()

	clients := make([]*Client, 0, len(summaries))
	for _, summary := range summaries {
		c, err := NewClient(summary, ts)
		if err != nil {
			return fmt.Errorf("creating client for operator %q: %w", summary.Name, err)
		}
		clients = append(clients, c)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.operators = clients
	log.Debugf("scheduler operator list replaced: %d operator(s)", len(clients))
	return nil
}

// LaunchAnalysis sends the bundle to the first operator that has capacity.
// Returns the id and name of the operator that accepted the analysis, or
// an error if no operator could accept it. Returning the id directly lets
// callers persist the analysis→operator linkage by UUID without a
// follow-up name→id lookup that could race with a concurrent rename.
//
// The scheduling strategy is simple: operators are tried in priority order.
// The first operator with available capacity gets the analysis. This
// minimizes usage of later (potentially more expensive) clusters.
func (s *Scheduler) LaunchAnalysis(ctx context.Context, bundle *AnalysisBundle) (uuid.UUID, string, error) {
	s.mu.RLock()
	clients := slices.Clone(s.operators)
	s.mu.RUnlock()

	if len(clients) == 0 {
		return uuid.Nil, "", ErrNoOperators
	}

	// Track capacity-check errors, transient launch failures, vendor
	// mismatches, and GPU-model mismatches separately so we can
	// distinguish "all operators unreachable" / "all operators at
	// capacity" / "no compatible vendor" / "no compatible model" and
	// preserve the last underlying error for diagnostics.
	var (
		capacityErrors   int
		transientErrors  int
		vendorMismatches int
		modelMismatches  int
		drainingSkips    int
		lastTransient    error
	)

	wantVendor := bundle.RequestedGPUVendor()
	wantModels := bundle.RequestedGPUModels()

	for _, op := range clients {
		// Skip operators that are draining: they stay in the pool to serve
		// their existing analyses but must not receive new launches.
		if !op.AcceptingLaunches() {
			drainingSkips++
			log.Infof("operator %s not accepting launches (draining); skipping", op.Name())
			continue
		}

		// Check capacity first.
		cap, err := op.Capacity(ctx)
		if err != nil {
			capacityErrors++
			log.Warnf("operator %s capacity check failed: %v", op.Name(), err)
			continue
		}

		if !cap.HasCapacity() {
			log.Infof("operator %s at capacity (%d/%d)", op.Name(), cap.RunningAnalyses, cap.MaxAnalyses)
			continue
		}

		// Skip operators whose GPU vendor doesn't match the bundle's
		// requested vendor. An empty wantVendor (no GPU requirement)
		// or empty cap.GPUVendor (operator pre-dates the field or has
		// no GPU) is treated as compatible.
		if !vendorCompatible(wantVendor, cap.GPUVendor) {
			vendorMismatches++
			log.Infof("operator %s vendor mismatch (analysis needs %s, operator is %s); skipping",
				op.Name(), wantVendor, cap.GPUVendor)
			continue
		}

		// Skip operators whose advertised GPU model set doesn't
		// intersect the bundle's requested models. Empty wantModels
		// (no model preference) and empty cap.SupportedGPUModels
		// (operator pre-dates the field or doesn't filter) both mean
		// "any" and pass through.
		if !modelCompatible(wantModels, cap.SupportedGPUModels) {
			modelMismatches++
			log.Infof("operator %s model mismatch (analysis needs %v, operator supports %v); skipping",
				op.Name(), wantModels, cap.SupportedGPUModels)
			continue
		}

		// Try to launch on this operator. Capacity races and transient
		// operator-side failures fall through to the next operator; only
		// errors that look like a bug in the request we built abort.
		if err := op.Launch(ctx, bundle); err != nil {
			if errors.Is(err, ErrCapacityExhausted) {
				// Race condition: capacity was available but filled before our launch.
				log.Infof("operator %s returned 409, trying next; this usually means it reached capacity after the check but before our job was submitted", op.Name())
				continue
			}
			if isTransientLaunchError(err) {
				transientErrors++
				lastTransient = err
				log.Warnf("operator %s launch failed transiently, trying next: %v", op.Name(), err)
				continue
			}
			return uuid.Nil, "", fmt.Errorf("launch on operator %s failed: %w", op.Name(), err)
		}

		log.Infof("analysis %s launched on operator %s (id=%s)", bundle.AnalysisID, op.Name(), op.ID())
		return op.ID(), op.Name(), nil
	}

	// Every operator is draining: none could take the launch by policy, not
	// because of capacity or health. Surface this distinctly so the caller
	// isn't misled into reporting a capacity shortfall.
	if drainingSkips == len(clients) {
		return uuid.Nil, "", fmt.Errorf("all %d operators draining for analysis %s: %w",
			len(clients), bundle.AnalysisID, ErrAllOperatorsDraining)
	}

	// Every operator's capacity check failed (all returned errors).
	if capacityErrors == len(clients) {
		return uuid.Nil, "", fmt.Errorf("all %d operators failed capacity check: %w", len(clients), ErrAllOperatorsExhausted)
	}

	// No operator advertised a compatible GPU vendor. Distinct from the
	// at-capacity / unhealthy buckets so callers can surface a routing-
	// policy error rather than a misleading "at capacity" message. Draining
	// operators are folded into the accounted-for total so the precise error
	// still fires when the non-draining operators are all vendor mismatches.
	if wantVendor != "" && vendorMismatches > 0 && capacityErrors+vendorMismatches+drainingSkips == len(clients) {
		return uuid.Nil, "", fmt.Errorf("no operator with vendor %s for analysis %s: %w",
			wantVendor, bundle.AnalysisID, ErrNoCompatibleOperator)
	}

	// No operator advertised a compatible GPU model. Surface this distinctly
	// so callers can tell "right vendor, wrong model" apart from
	// ErrNoCompatibleOperator and ErrAllOperatorsExhausted.
	if len(wantModels) > 0 && modelMismatches > 0 && capacityErrors+vendorMismatches+modelMismatches+drainingSkips == len(clients) {
		return uuid.Nil, "", fmt.Errorf("no operator with model %v for analysis %s: %w",
			wantModels, bundle.AnalysisID, ErrNoCompatibleModel)
	}

	// Every operator that passed capacity check then failed the launch for
	// transient reasons. Surface the last underlying error so the caller
	// can distinguish it from a real "all at capacity" situation.
	if transientErrors > 0 && capacityErrors+transientErrors+vendorMismatches+modelMismatches+drainingSkips == len(clients) {
		return uuid.Nil, "", fmt.Errorf("all %d operators unhealthy; last error: %w", len(clients), lastTransient)
	}

	return uuid.Nil, "", ErrAllOperatorsExhausted
}

// Clients returns a copy of all operator clients so callers can iterate for
// aggregation (e.g. listing analyses across all clusters) without mutating
// the scheduler's internal state.
func (s *Scheduler) Clients() []*Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return slices.Clone(s.operators)
}

// ClientByName returns the operator client with the given name, or nil.
//
// Deprecated: Use ClientByID. Operator names are mutable, so a concurrent
// rename can leave a name-keyed lookup looking for a stale value; ClientByID
// uses the immutable UUID and is rename-safe.
func (s *Scheduler) ClientByName(name string) *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, op := range s.operators {
		if op.Name() == name {
			return op
		}
	}
	return nil
}

// ClientByID returns the operator client with the given UUID, or nil.
// Preferred over ClientByName because the id is stable across renames —
// a client looked up by id won't go stale just because the operator's
// name was changed.
func (s *Scheduler) ClientByID(id uuid.UUID) *Client {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, op := range s.operators {
		if op.ID() == id {
			return op
		}
	}
	return nil
}
