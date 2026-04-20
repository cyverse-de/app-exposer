package operatorclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"sync"

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
	// true for 5xx; anything 4xx (other than 409, which is ErrCapacityExhausted
	// above this check) is a request we built wrong and should not be retried.
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
)

// Scheduler manages a priority-ordered list of operator clients and
// routes analyses to the first operator with available capacity.
type Scheduler struct {
	mu          sync.RWMutex
	operators   []*Client
	tokenSource oauth2.TokenSource
}

// NewScheduler creates a Scheduler from operator configs. The token source is
// used to authenticate requests to all operators; pass nil to disable auth.
// Operators are tried in the order they appear in the configs slice
// (config order = priority order).
func NewScheduler(configs []OperatorConfig, ts oauth2.TokenSource) (*Scheduler, error) {
	clients := make([]*Client, 0, len(configs))
	for _, cfg := range configs {
		c, err := NewClient(cfg, ts)
		if err != nil {
			return nil, fmt.Errorf("creating client for operator %q: %w", cfg.Name, err)
		}
		clients = append(clients, c)
	}

	return &Scheduler{operators: clients, tokenSource: ts}, nil
}

// SetTokenSource updates the token source used for authenticating requests
// to operators. Subsequent calls to Sync will use the new token source.
func (s *Scheduler) SetTokenSource(ts oauth2.TokenSource) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokenSource = ts
}

// Sync replaces the scheduler's current operator clients with a new list.
// This allows runtime updates from the database without a restart.
func (s *Scheduler) Sync(configs []OperatorConfig) error {
	// Snapshot the token source under a read-lock so that a concurrent
	// SetTokenSource call cannot race with our read of s.tokenSource.
	s.mu.RLock()
	ts := s.tokenSource
	s.mu.RUnlock()

	clients := make([]*Client, 0, len(configs))
	for _, cfg := range configs {
		c, err := NewClient(cfg, ts)
		if err != nil {
			return fmt.Errorf("creating client for operator %q: %w", cfg.Name, err)
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
// Returns the name of the operator that accepted the analysis, or an error
// if no operator could accept it.
//
// The scheduling strategy is simple: operators are tried in config order
// (priority order). The first operator with available capacity gets the
// analysis. This minimizes usage of later (potentially more expensive)
// clusters.
func (s *Scheduler) LaunchAnalysis(ctx context.Context, bundle *AnalysisBundle) (string, error) {
	s.mu.RLock()
	clients := slices.Clone(s.operators)
	s.mu.RUnlock()

	if len(clients) == 0 {
		return "", ErrNoOperators
	}

	// Track capacity-check errors and transient launch failures separately
	// so we can distinguish "all operators unreachable" from "all operators
	// at capacity" and preserve the last underlying error for diagnostics.
	var (
		capacityErrors  int
		transientErrors int
		lastTransient   error
	)

	for _, op := range clients {
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
			return "", fmt.Errorf("launch on operator %s failed: %w", op.Name(), err)
		}

		log.Infof("analysis %s launched on operator %s", bundle.AnalysisID, op.Name())
		return op.Name(), nil
	}

	// Every operator's capacity check failed (all returned errors).
	if capacityErrors == len(clients) {
		return "", fmt.Errorf("all %d operators failed capacity check: %w", len(clients), ErrAllOperatorsExhausted)
	}

	// Every operator that passed capacity check then failed the launch for
	// transient reasons. Surface the last underlying error so the caller
	// can distinguish it from a real "all at capacity" situation.
	if transientErrors > 0 && capacityErrors+transientErrors == len(clients) {
		return "", fmt.Errorf("all %d operators unhealthy; last error: %w", len(clients), lastTransient)
	}

	return "", ErrAllOperatorsExhausted
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
