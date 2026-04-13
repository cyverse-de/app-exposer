package operatorclient

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"golang.org/x/oauth2"
)

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
	clients := make([]*Client, 0, len(configs))
	for _, cfg := range configs {
		c, err := NewClient(cfg, s.tokenSource)
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

	// Track capacity-check errors separately so we can distinguish
	// "all operators unreachable" from "all operators at capacity."
	var capacityErrors int

	for _, op := range clients {
		// Check capacity first.
		cap, err := op.Capacity(ctx)
		if err != nil {
			capacityErrors++
			log.Warnf("operator %s capacity check failed: %v", op.Name(), err)
			continue
		}

		// AvailableSlots: >0 = has capacity, 0 = at capacity, -1 = unlimited.
		if cap.AvailableSlots == 0 {
			log.Infof("operator %s at capacity (%d/%d)", op.Name(), cap.RunningAnalyses, cap.MaxAnalyses)
			continue
		}

		// Try to launch on this operator.
		if err := op.Launch(ctx, bundle); err != nil {
			if errors.Is(err, ErrCapacityExhausted) {
				// Race condition: capacity was available but filled before our launch.
				log.Infof("operator %s returned 409, trying next", op.Name())
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
