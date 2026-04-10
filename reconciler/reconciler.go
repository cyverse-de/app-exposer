package reconciler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/cyverse-de/app-exposer/common"
	"github.com/cyverse-de/app-exposer/db"
	"github.com/cyverse-de/app-exposer/operatorclient"
	"github.com/cyverse-de/app-exposer/reporting"
	"github.com/cyverse-de/messaging/v12"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

var log = common.Log.WithFields(logrus.Fields{"package": "reconciler"})

// Reconciler manages the background process for syncing VICE analysis status
// from remote operators into the DE database.
type Reconciler struct {
	db        *db.Database
	scheduler *operatorclient.Scheduler
	aesKey    string
	hostname  string
	ip        string
}

// New creates a new Reconciler.
func New(db *db.Database, scheduler *operatorclient.Scheduler, aesKey string) *Reconciler {
	hostname, _ := os.Hostname()
	return &Reconciler{
		db:        db,
		scheduler: scheduler,
		aesKey:    aesKey,
		hostname:  hostname,
		ip:        getLocalIP(),
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

// Run starts the reconciliation loop. It periodically refreshes the operator
// list and reconciles remote clusters.
func (r *Reconciler) Run(ctx context.Context) {
	log.Info("starting reconciliation worker")

	// Initial sync of operators from DB.
	if err := r.SyncOperators(ctx); err != nil {
		log.Errorf("initial operator sync failed: %v", err)
	}

	syncTicker := time.NewTicker(5 * time.Minute)
	reconcileTicker := time.NewTicker(30 * time.Second)

	defer syncTicker.Stop()
	defer reconcileTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("stopping reconciliation worker")
			return

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

// SyncOperators fetches operators from the DB, decrypts their passwords,
// and updates the scheduler. If any operator's password cannot be decrypted,
// the entire sync is aborted to avoid silently dropping operators from the
// scheduler's routing list.
func (r *Reconciler) SyncOperators(ctx context.Context) error {
	ops, err := r.db.ListOperators(ctx)
	if err != nil {
		return err
	}

	configs := make([]operatorclient.OperatorConfig, 0, len(ops))
	for _, op := range ops {
		password, err := common.Decrypt(op.AuthPasswordEncrypted, r.aesKey)
		if err != nil {
			return fmt.Errorf("decrypting password for operator %q: %w", op.Name, err)
		}
		configs = append(configs, op.ToOperatorConfig(password))
	}

	return r.scheduler.Sync(configs)
}

// ReconcileNext claims one operator that is due for reconciliation and
// processes all analyses in its cluster. The claim and reconciliation
// timestamp update happen within a single transaction so the FOR UPDATE
// SKIP LOCKED row lock is held for the entire operation.
func (r *Reconciler) ReconcileNext(ctx context.Context) error {
	return r.db.ClaimAndReconcile(ctx, r.hostname, 60*time.Second, func(tx *sqlx.Tx, op *db.Operator) error {
		log.Infof("reconciling operator %q", op.Name)

		client := r.scheduler.ClientByName(op.Name)
		if client == nil {
			return fmt.Errorf("operator %q not found in scheduler", op.Name)
		}

		// Fetch bulk status from operator.
		info, err := client.Listing(ctx, nil)
		if err != nil {
			return fmt.Errorf("listing analyses from %q: %w", op.Name, err)
		}

		// Process each analysis found in the cluster.
		for _, pod := range info.Pods {
			if err := r.reconcileAnalysis(ctx, tx, pod); err != nil {
				log.Errorf("failed to reconcile analysis %s: %v", pod.AnalysisID, err)
			}
		}

		return nil
	})
}

func (r *Reconciler) reconcileAnalysis(ctx context.Context, tx *sqlx.Tx, pod reporting.PodInfo) error {
	dbStatus, err := r.db.GetAnalysisStatus(ctx, tx, pod.AnalysisID)
	if err != nil {
		return err
	}

	clusterStatus := mapPodPhaseToStatus(pod.Phase)

	if clusterStatus != dbStatus {
		log.Infof("analysis %s status change: %s -> %s (cluster truth)", pod.AnalysisID, dbStatus, clusterStatus)
		return r.recordStatusUpdate(ctx, tx, pod.ExternalID, clusterStatus, pod.Message)
	}

	return nil
}

func mapPodPhaseToStatus(phase string) string {
	switch phase {
	case "Pending":
		return string(messaging.SubmittedState)
	case "Running":
		return string(messaging.RunningState)
	case "Succeeded":
		return string(messaging.SucceededState)
	case "Failed":
		return string(messaging.FailedState)
	default:
		return string(messaging.SubmittedState)
	}
}

func (r *Reconciler) recordStatusUpdate(ctx context.Context, tx *sqlx.Tx, externalID, status, message string) error {
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
