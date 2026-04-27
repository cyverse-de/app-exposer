package reconciler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/cyverse-de/app-exposer/apps"
	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/constants"
	"github.com/cyverse-de/app-exposer/db"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/cyverse-de/messaging/v12"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/sirupsen/logrus"
	"golang.org/x/oauth2"
)

var log = common.Log.WithFields(logrus.Fields{"package": "reconciler"})

// Reconciler manages the background process for syncing VICE analysis status
// from remote operators into the DE database.
type Reconciler struct {
	db          db.ReconcilerDB
	apps        apps.OperatorNameLookup
	scheduler   *operatorclient.Scheduler
	tokenSource oauth2.TokenSource
	hostname    string
	ip          string
}

// New creates a new Reconciler. The database handle supplies both the pooled
// SQL connection for reconciliation queries and (via ReconcilerDB.URI) the
// connection string used to open a dedicated PostgreSQL LISTEN channel for
// operator-change notifications; a URI of "" disables NOTIFY-driven syncs and
// falls back to periodic polling. The token source is passed through to the
// scheduler for authenticating requests to operator instances. appsLookup may
// be nil — in that case the per-pod operator_name back-fill in ReconcileNext
// is skipped.
func New(database db.ReconcilerDB, appsLookup apps.OperatorNameLookup, scheduler *operatorclient.Scheduler, ts oauth2.TokenSource) *Reconciler {
	hostname, err := os.Hostname()
	if err != nil {
		// Fall back to empty hostname — reconciled_by just becomes "",
		// which still lets reconciliation proceed and is visible in logs.
		log.Warnf("os.Hostname failed, reconciled_by will be empty: %v", err)
	}
	return &Reconciler{
		db:          database,
		apps:        appsLookup,
		scheduler:   scheduler,
		tokenSource: ts,
		hostname:    hostname,
		ip:          getLocalIP(),
	}
}

func getLocalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	for _, address := range addrs {
		if ipnet, ok := address.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				return ipnet.IP.String()
			}
		}
	}
	return "127.0.0.1"
}

// Backoff bounds for the LISTEN retry loop. The outer retry only fires
// when pq.Listener.Listen() itself fails at setup (bad channel name,
// missing privilege, etc.); post-setup DB blips are handled by pq's own
// reconnect machinery. Capped at DefaultSyncInterval so we never sit
// idle for longer than the fallback polling cadence.
const (
	initialListenerBackoff = 5 * time.Second
	maxListenerBackoff     = DefaultSyncInterval
)

const (
	// DefaultSyncInterval is the periodic-poll cadence used as a fallback
	// when NOTIFY-driven syncs are unavailable.
	DefaultSyncInterval = 5 * time.Minute
	// DefaultReconcileInterval is the per-operator reconciliation cadence;
	// tighter than the sync cadence because reconciliation runs per
	// operator while sync just refreshes the operator list.
	DefaultReconcileInterval = 30 * time.Second
	// DefaultClaimTTL bounds how soon a previously-reconciled operator is
	// eligible for re-reconciliation; must be comfortably larger than
	// DefaultReconcileInterval to avoid back-to-back claims on one operator.
	DefaultClaimTTL = 60 * time.Second
)

// startListener returns a channel that fires whenever this process should
// re-sync operators from the DB. It opens a PostgreSQL LISTEN on the
// "operator_changed" channel in a background goroutine with retry-and-
// backoff, so a transient Listen() failure at startup does not silently
// disable NOTIFY-driven syncs for the life of the process.
//
// The returned channel never closes; callers should select on it in the
// same loop that handles ctx.Done(). If the database handle reports an
// empty URI, the channel is returned inert (operator change notifications
// are disabled and only the periodic syncTicker drives SyncOperators).
func (r *Reconciler) startListener(ctx context.Context) <-chan struct{} {
	ch := make(chan struct{}, 1) // buffered so sends never block

	if r.db.URI() == "" {
		log.Warn("no database URI configured; operator change notifications disabled, falling back to periodic sync only")
		return ch
	}

	go r.listenerLoop(ctx, ch)
	return ch
}

// listenerLoop owns the pq.Listener lifecycle. It retries failed Listen()
// calls with exponential backoff and guarantees listener.Close() is called
// on every exit path — pq.NewListener spawns an internal reconnect
// goroutine as part of its constructor, so skipping Close leaks it.
func (r *Reconciler) listenerLoop(ctx context.Context, ch chan<- struct{}) {
	backoff := initialListenerBackoff

	for {
		if ctx.Err() != nil {
			return
		}

		listener := pq.NewListener(r.db.URI(), 10*time.Second, time.Minute, r.reportListenerProblem(ch))
		if err := listener.Listen("operator_changed"); err != nil {
			// Usual cause: the NOTIFY channel name is misconfigured or
			// the role lacks LISTEN privilege. Close so pq.Listener's
			// internal reconnect goroutine stops; otherwise we leak one
			// goroutine per retry attempt. Any Close error is diagnostic
			// (the connection is already failing) — log and move on.
			if closeErr := listener.Close(); closeErr != nil {
				log.Warnf("closing pq listener after failed Listen: %v", closeErr)
			}
			log.Warnf(
				"LISTEN on operator_changed failed; periodic sync continues, will retry in %s: %v",
				backoff, err,
			)
			if !sleepCtx(ctx, backoff) {
				return
			}
			backoff = nextBackoff(backoff, maxListenerBackoff)
			continue
		}

		log.Info("listening for operator_changed notifications from PostgreSQL")
		backoff = initialListenerBackoff // reset after any successful Listen

		r.pumpNotifications(ctx, listener, ch)
		if closeErr := listener.Close(); closeErr != nil {
			log.Warnf("closing pq listener after pump exit: %v", closeErr)
		}
		if ctx.Err() != nil {
			return
		}
		// Pump exited without context cancel, which means pq closed the
		// Notify channel. Fall through to re-establish.
	}
}

// reportListenerProblem returns the callback pq.NewListener invokes on
// connection-state transitions. It trips a resync on reconnect so changes
// missed during the disconnect window propagate immediately.
func (r *Reconciler) reportListenerProblem(ch chan<- struct{}) func(pq.ListenerEventType, error) {
	return func(ev pq.ListenerEventType, err error) {
		if err != nil {
			log.Errorf("pg listener: %v", err)
		}
		switch ev {
		case pq.ListenerEventDisconnected:
			log.Warn("pg listener disconnected; will reconnect automatically")
		case pq.ListenerEventReconnected:
			log.Info("pg listener reconnected")
			// Trigger a sync on reconnect in case we missed notifications.
			select {
			case ch <- struct{}{}:
			default:
			}
		default:
		}
	}
}

// pumpNotifications forwards every NOTIFY event onto ch until the context
// is canceled or pq closes the Notify channel. Sends to ch are
// non-blocking because ch is buffered size-1 and rapid bursts should
// collapse into at most one pending sync.
func (r *Reconciler) pumpNotifications(ctx context.Context, listener *pq.Listener, ch chan<- struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-listener.Notify:
			if !ok {
				return
			}
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

// sleepCtx waits for d or until ctx is canceled. Returns true when the
// wait completed normally, false when the context canceled — lets callers
// use it as a loop guard without checking ctx separately.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// nextBackoff doubles cur, capped at maxBackoff.
func nextBackoff(cur, maxBackoff time.Duration) time.Duration {
	next := cur * 2
	if next > maxBackoff {
		return maxBackoff
	}
	return next
}

// Run starts the reconciliation loop. It periodically refreshes the operator
// list, reconciles remote clusters, and reacts to PostgreSQL NOTIFY signals
// for immediate operator sync.
func (r *Reconciler) Run(ctx context.Context) {
	log.Info("starting reconciliation worker")

	// Propagate the token source to the scheduler so all operator clients
	// created during Sync use it for authentication.
	r.scheduler.SetTokenSource(r.tokenSource)

	// Initial sync of operators from DB.
	if err := r.SyncOperators(ctx); err != nil {
		log.Errorf("initial operator sync failed: %v", err)
	}

	notifyCh := r.startListener(ctx)
	syncTicker := time.NewTicker(DefaultSyncInterval)
	reconcileTicker := time.NewTicker(DefaultReconcileInterval)

	defer syncTicker.Stop()
	defer reconcileTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("stopping reconciliation worker")
			return

		case <-notifyCh:
			log.Info("operator change notification received; syncing")
			if err := r.SyncOperators(ctx); err != nil {
				log.Errorf("operator sync (notify-triggered) failed: %v", err)
			}

		case <-syncTicker.C:
			if err := r.SyncOperators(ctx); err != nil {
				log.Errorf("operator sync failed: %v", err)
			}

		case <-reconcileTicker.C:
			if err := r.ReconcileNext(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
				log.Errorf("reconciliation failed: %v", err)
			}
		}
	}
}

// SyncOperators fetches operators from the DB and updates the scheduler.
func (r *Reconciler) SyncOperators(ctx context.Context) error {
	ops, err := r.db.ListOperators(ctx)
	if err != nil {
		return err
	}

	if len(ops) == 0 {
		log.Info("no operators found in database")
	}

	configs := make([]operatorclient.OperatorConfig, 0, len(ops))
	names := make([]string, 0, len(ops))
	for _, op := range ops {
		configs = append(configs, op.ToOperatorConfig())
		names = append(names, op.Name)
	}

	if err := r.scheduler.Sync(configs); err != nil {
		return err
	}

	log.Infof("synced %d operator(s) to scheduler: %v", len(configs), names)
	return nil
}

// ReconcileNext claims one operator due for reconciliation and processes
// all analyses in its cluster. The claim and timestamp update are
// coordinated atomically by the database.
func (r *Reconciler) ReconcileNext(ctx context.Context) error {
	return r.db.ClaimAndReconcile(ctx, r.hostname, DefaultClaimTTL, func(tx *sqlx.Tx, op *db.Operator) error {
		log.Infof("reconciling operator %q", op.Name)

		client := r.scheduler.ClientByName(op.Name)
		if client == nil {
			clients := r.scheduler.Clients()
			known := make([]string, 0, len(clients))
			for _, c := range clients {
				known = append(known, c.Name())
			}
			return fmt.Errorf("operator %q not found in scheduler (scheduler has %d operator(s): %v)", op.Name, len(known), known)
		}

		// Fetch bulk status from operator.
		info, err := client.Listing(ctx, nil)
		if err != nil {
			return fmt.Errorf("listing analyses from %q: %w", op.Name, err)
		}

		// Process each analysis found in the cluster. Per-analysis errors
		// are collected rather than short-circuited: we want to attempt
		// every analysis in this claim cycle, but also surface failure
		// so the enclosing transaction rolls back — which leaves
		// last_reconciled_at unchanged and re-queues this operator for
		// the next reconcile tick instead of making the failing analysis
		// wait until the next full 30s cycle.
		var analysisErrs []error
		for _, pod := range info.Pods {
			log.Debugf("reconciling analysis %s", pod.AnalysisID)
			if err := r.reconcileAnalysis(ctx, tx, pod); err != nil {
				log.Errorf("failed to reconcile analysis %s: %v", pod.AnalysisID, err)
				analysisErrs = append(analysisErrs, fmt.Errorf("analysis %s: %w", pod.AnalysisID, err))
			} else {
				log.Infof("reconciled analysis %s", pod.AnalysisID)
			}
			// Back-fill the operator name for this analysis if it's missing
			// or stale. The launch path also records this via
			// setOperatorNameWithRetry, but that retry can exhaust, and
			// without a recorded name every subsequent request for this
			// analysis falls through to the operator fan-out search.
			//
			// Back-fill errors are logged inside backfillOperatorName and
			// deliberately NOT added to analysisErrs — a failed back-fill
			// is self-healing on the next cycle and doesn't warrant
			// rolling back status updates we already collected.
			r.backfillOperatorName(ctx, pod.AnalysisID, op.Name)
		}

		if len(analysisErrs) > 0 {
			return fmt.Errorf("reconciling %d analysis(es) from operator %q: %w", len(analysisErrs), op.Name, errors.Join(analysisErrs...))
		}
		return nil
	})
}

// backfillOperatorName writes the operator name for an analysis when the
// DB either has no row yet or has a different name recorded. Errors are
// logged but never propagate — a failed back-fill just means the next
// reconciliation cycle will try again, and the fan-out fallback in
// operatorClientForAnalysis continues to work in the meantime.
func (r *Reconciler) backfillOperatorName(ctx context.Context, analysisID constants.AnalysisID, operatorName string) {
	if r.apps == nil || analysisID == "" {
		return
	}
	current, err := r.apps.GetOperatorName(ctx, analysisID)
	if err != nil {
		log.Warnf("backfill: GetOperatorName failed for analysis %s: %v", analysisID, err)
		return
	}
	if current == operatorName {
		return
	}
	if err := r.apps.SetOperatorName(ctx, analysisID, operatorName); err != nil {
		log.Warnf("backfill: SetOperatorName failed for analysis %s → %q: %v", analysisID, operatorName, err)
		return
	}
	if current == "" {
		log.Infof("backfill: recorded operator %q for analysis %s", operatorName, analysisID)
	} else {
		log.Infof("backfill: updated operator for analysis %s from %q to %q", analysisID, current, operatorName)
	}
}

func (r *Reconciler) reconcileAnalysis(ctx context.Context, tx *sqlx.Tx, pod reporting.PodInfo) error {
	// Skip pods that are missing required labels — these may be from a
	// different system or were created before labels were populated.
	if pod.AnalysisID == "" || pod.ExternalID == "" {
		log.Debugf("pod %s missing analysis-id or external-id label, skipping", pod.Name)
		return nil
	}

	// Compare against the most recent job_status_updates row rather than the
	// jobs table. There can be lag between recording a status update and
	// propagating it to jobs, which would cause duplicate reconciliation
	// updates if we compared against the jobs table.
	dbStatus, err := r.db.GetLatestStatusByExternalID(ctx, tx, pod.ExternalID)
	if err != nil {
		// No status updates yet — the pod may have been created before any
		// status was recorded, or it may belong to a different system.
		if errors.Is(err, sql.ErrNoRows) {
			log.Debugf("no status updates for analysis %s (external %s), skipping", pod.AnalysisID, pod.ExternalID)
			return nil
		}
		return err
	}

	clusterStatus := mapPodPhaseToStatus(pod.Phase)

	if clusterStatus != dbStatus {
		log.Infof("analysis %s status change: %s -> %s (cluster truth)", pod.AnalysisID, dbStatus, clusterStatus)
		return r.recordStatusUpdate(ctx, tx, pod.ExternalID, clusterStatus, pod.Message)
	}

	return nil
}

func mapPodPhaseToStatus(phase string) messaging.JobState {
	switch phase {
	case "Pending":
		return messaging.SubmittedState
	case "Running":
		return messaging.RunningState
	case "Succeeded":
		return messaging.SucceededState
	case "Failed":
		return messaging.FailedState
	default:
		return messaging.SubmittedState
	}
}

func (r *Reconciler) recordStatusUpdate(ctx context.Context, tx *sqlx.Tx, externalID constants.ExternalID, status messaging.JobState, message string) error {
	if message == "" {
		message = fmt.Sprintf("Status changed to %s", status)
	}

	update := &db.JobStatusUpdate{
		ExternalID:       externalID,
		Message:          message,
		Status:           status,
		SentFrom:         r.ip,
		SentFromHostname: r.hostname,
		SentOn:           time.Now().UnixMilli(),
	}

	return r.db.InsertJobStatusUpdate(ctx, tx, update)
}
