# Move app-exposer / timelord writes off the `jobs` table

## Context

`apps` and `app-exposer` both write to the `de-database` `jobs` table. Under
load (workshops, batches of cancellations), users report that operations on
their analyses — notably terminations — block or fail. The investigation
showed:

- `app-exposer` writes `operator_id` (with a ~21-second retry loop),
  `millicores_reserved`, and `planned_end_date` to `jobs` as
  single-statement auto-commit UPDATEs.
- `timelord` writes `planned_end_date` and `subdomain` to `jobs` from its
  AMQP status-message handler — every status update that arrives for an
  analysis without a planned end re-touches the `jobs` row.
- `apps` holds explicit `(transaction …)` blocks that take row-exclusive
  locks (and `SELECT … FOR UPDATE`) on the `jobs` row for the duration of
  status updates and stop-job paths.
- `apps` reads and writes none of `operator_id`, `millicores_reserved`,
  `planned_end_date`, or `subdomain`.
- `analyses` does not touch the `jobs` table at all.

Three services brief-locking the same row while a fourth holds it under
`FOR UPDATE` is enough to produce sustained contention and at least
opportunistic FK-mediated deadlocks. Moving the four "single-writer-style"
columns into a separate join table `job_runtime` removes the cross-service
write traffic from `jobs`, leaving `apps` as the sole writer of the
status/end_date/deleted columns it actually owns.

Beyond the schema move, the launch handler becomes the canonical
initial-writer for `planned_end_date` and `subdomain` as well. Today
timelord's `EnsurePlannedEndDate` and `EnsureSubdomain` set these on the
first `Running` AMQP status message; we move both into app-exposer's
launch path so each `job_runtime` column has exactly one writer service:

- `operator_id`           — app-exposer launch handler (already true).
- `planned_end_date`      — app-exposer launch handler (initial) and
                             app-exposer time-limit endpoint (extension).
- `subdomain`             — app-exposer launch handler.
- `millicores_reserved`   — app-exposer queue worker (post-launch async).

timelord becomes effectively read-only against `jobs` / `job_runtime`:
its only remaining responsibility on those tables is querying for
expiring/expired analyses to drive notifications and kills.

Decisions: cut-over (old columns left orphaned, dropped in a follow-up
migration), table name `job_runtime`, add an index on `subdomain`,
multi-stage rollout (timelord's `Ensure*` calls stay as a safety net
through stages 1–2 and are removed only after we've verified in
production that they never fire).

## Preliminary work already on the branch

Two commits already on `jobs-table-refactor` removed the
`operator_id`-specific contention sources independently of the schema
move:

- `c12fe99` — Stop writing `jobs.operator_id` from non-launch paths.
  The reconciler's `backfillOperatorID` and the cache-miss writeback in
  `operatorClientForAnalysis` are gone. The launch handler is now the
  only writer. Missing `operator_id` now falls back to the operator
  fan-out search (`handlers.go:84-99` documents this).
- `3c3ca6e` — Drop the retry loop. `SetOperatorID` is now a single-shot
  function (`apps/apps.go:263-277`); the launch handler calls it once at
  `httphandlers/launch.go:180-182` and logs on failure. `ErrJobsRowMissing`
  and `SetOperatorIDNoRetry` are gone.

The remaining `operator_id` contention story is therefore already
minimized; the join-table move gets it the rest of the way (off the
`jobs` row entirely) for consistency with the other three columns. The
new write work in this plan concerns `planned_end_date` and `subdomain`
(consolidating their initial-writes into app-exposer's launch handler)
and `millicores_reserved` (route through `job_runtime`).

## Schema migration

### Existing views that depend on the columns being moved

Two views currently reference `jobs.planned_end_date` and/or
`jobs.subdomain`, which means a naïve `ALTER TABLE … DROP COLUMN` would
either fail or (with `CASCADE`) silently kill the views:

- `job_listings` (defined in `000033_app_versions_table.up.sql:393`).
  References `j.planned_end_date`. Consumed by `apps`
  (`persistence/jobs.clj:228, 292, 340, 383, 499, 509`), though apps'
  current field lists in `job-base-query` and `hsql-job-base-query` do
  not actually project `planned_end_date` — the view simply defines it.
- `vice_analyses` (defined in `000017_views.up.sql:201`). References
  `j.subdomain` and `j.planned_end_date`. **No current consumers** found
  in any sibling repo's source (only in `de-database/old-databases/`).
  Keep it (don't drop) for SQL-surface stability, but rebase onto
  `job_runtime`.

Migration 000050 must `CREATE OR REPLACE VIEW` both of these so the
column projections come from `job_runtime` via `LEFT JOIN`. (`CREATE OR
REPLACE VIEW` in PostgreSQL requires the projection's column names,
order, and types to be preserved — adding new columns at the end is
allowed but we don't need to. Source-column re-routing is allowed.)

### New migration: `de-database/migrations/000050_job_runtime_table.up.sql`

Conventions match the existing migrations (e.g. `000048_operators_table.up.sql`).

```sql
BEGIN;

CREATE TABLE IF NOT EXISTS job_runtime (
    job_id              uuid PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
    operator_id         uuid REFERENCES operators(id) ON DELETE RESTRICT,
    millicores_reserved int NOT NULL DEFAULT 0,
    planned_end_date    timestamp,
    subdomain           varchar(32)
);

CREATE INDEX IF NOT EXISTS job_runtime_subdomain_idx ON job_runtime (subdomain);

-- One-time backfill from the orphaned columns. ON CONFLICT keeps the
-- migration idempotent if it gets re-run after a partial failure.
INSERT INTO job_runtime (job_id, operator_id, millicores_reserved, planned_end_date, subdomain)
SELECT id, operator_id, millicores_reserved, planned_end_date, subdomain
FROM jobs
ON CONFLICT (job_id) DO NOTHING;

-- Rebase dependent views onto job_runtime so the follow-up migration
-- can drop the orphaned jobs columns safely. Column lists are
-- preserved verbatim from 000033 / 000017; only the FROM/JOIN source
-- of the four moved columns changes.
CREATE OR REPLACE VIEW job_listings AS
    SELECT j.id, j.job_name, j.app_name, j.start_date, j.end_date,
           j.status, j.deleted, j.notify, u.username, j.job_description,
           j.app_id, j.app_version_id, j.app_wiki_url, j.app_description,
           j.result_folder_path, j.submission, t.name AS job_type,
           j.parent_id,
           EXISTS (SELECT * FROM jobs child WHERE child.parent_id = j.id) AS is_batch,
           t.system_id,
           jr.planned_end_date,
           j.user_id
    FROM jobs j
    JOIN users u ON j.user_id = u.id
    JOIN job_types t ON j.job_type_id = t.id
    LEFT JOIN job_runtime jr ON jr.job_id = j.id;

-- vice_analyses: similar treatment. Re-state the full projection from
-- 000017_views.up.sql:201 with planned_end_date and subdomain pulled
-- from job_runtime (TODO during implementation: copy the existing
-- column list verbatim, only changing the source of the two columns).

COMMIT;
```

Column types match the current `jobs` columns exactly: `operator_id uuid`
(nullable), `millicores_reserved int NOT NULL DEFAULT 0`,
`planned_end_date timestamp` (nullable), `subdomain varchar(32)`
(nullable).

### Down migration: `de-database/migrations/000050_job_runtime_table.down.sql`

```sql
BEGIN;

-- Restore the views to their prior source-from-jobs definitions before
-- dropping job_runtime, otherwise the DROP TABLE will fail on the view
-- dependency.
CREATE OR REPLACE VIEW job_listings AS
    SELECT j.id, j.job_name, j.app_name, j.start_date, j.end_date,
           j.status, j.deleted, j.notify, u.username, j.job_description,
           j.app_id, j.app_version_id, j.app_wiki_url, j.app_description,
           j.result_folder_path, j.submission, t.name AS job_type,
           j.parent_id,
           EXISTS (SELECT * FROM jobs child WHERE child.parent_id = j.id) AS is_batch,
           t.system_id,
           j.planned_end_date,
           j.user_id
    FROM jobs j
    JOIN users u ON j.user_id = u.id
    JOIN job_types t ON j.job_type_id = t.id;

-- vice_analyses: restore to source-from-jobs (paste the original
-- 000017_views.up.sql:201 definition verbatim).

DROP TABLE IF EXISTS job_runtime;

COMMIT;
```

### Follow-up migration (separate release): `de-database/migrations/000051_drop_orphaned_jobs_columns.up.sql`

Apply after the new code has run in production for at least one full
release cycle and `job_runtime` is verified to be the source of truth.
By this point migration 000050 has already moved the two views off these
columns, so the drop is dependency-free.

```sql
BEGIN;
ALTER TABLE jobs
    DROP COLUMN IF EXISTS operator_id,
    DROP COLUMN IF EXISTS millicores_reserved,
    DROP COLUMN IF EXISTS planned_end_date,
    DROP COLUMN IF EXISTS subdomain;
COMMIT;
```

(Note: the `jobs_operator_id_fkey` constraint will be dropped with the
column. No separate `DROP CONSTRAINT` needed.)

### Down migration: `de-database/migrations/000051_drop_orphaned_jobs_columns.down.sql`

Restores the columns and the data, sourced from `job_runtime` (which is
still present at this point because 000050 hasn't been rolled back). The
views still source from `job_runtime` after this — that's the state we
left at the end of 000050, which is what the down should restore us to.

```sql
BEGIN;

ALTER TABLE jobs
    ADD COLUMN IF NOT EXISTS operator_id         uuid DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS millicores_reserved int NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS planned_end_date    timestamp,
    ADD COLUMN IF NOT EXISTS subdomain           varchar(32);

-- Restore data from job_runtime, the source of truth at this point.
UPDATE jobs j
SET operator_id         = jr.operator_id,
    millicores_reserved = jr.millicores_reserved,
    planned_end_date    = jr.planned_end_date,
    subdomain           = jr.subdomain
FROM job_runtime jr
WHERE j.id = jr.job_id;

-- Restore the FK constraint that was originally added in 000048.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'jobs_operator_id_fkey'
    ) THEN
        ALTER TABLE jobs
            ADD CONSTRAINT jobs_operator_id_fkey
            FOREIGN KEY (operator_id) REFERENCES operators(id) ON DELETE RESTRICT;
    END IF;
END$$;

COMMENT ON COLUMN jobs.operator_id IS
    'FK to operators. NULL means the job was launched before the operator system.';

COMMIT;
```

## app-exposer changes

### Writes — replace direct `UPDATE jobs` with UPSERT into `job_runtime`

The shared UPSERT shape, suitable for callers that only touch one column
at a time:

```sql
INSERT INTO job_runtime (job_id, <col>) VALUES ($1, $2)
ON CONFLICT (job_id) DO UPDATE SET <col> = EXCLUDED.<col>;
```

| Current site | Change |
|---|---|
| `apps/apps.go:250-254` (`setOperatorIDStmt`) | UPSERT `operator_id` |
| `apps/apps.go:194-198` + 200-207 (`setMillicoresStmt`, `setMillicoresReserved`) | UPSERT `millicores_reserved` |
| `db/db.go:56-60` (`SetMillicoresReservedByAnalysisID`) | UPSERT `millicores_reserved` |
| `incluster/incluster.go:201-210` (time-limit extension) | Rewrite as an UPDATE on `job_runtime` joined to `jobs` for the user-ownership filter — see shape below. A bare UPSERT can't enforce user ownership, and a row that has no `job_runtime` entry yet should fail the extension rather than invent a planned_end_date out of thin air. |

### Launch handler: write initial `planned_end_date` and `subdomain`

The async launch path in `httphandlers/launch.go:launchAsync` already
calls `SetOperatorID` after dispatching the job to the operator
(`launch.go:180-182`). Extend that same post-dispatch block to also
compute and write `planned_end_date` and `subdomain` into `job_runtime`.

**Subdomain.** Generate the same way timelord does today:

```go
// timelord/analyses.go:425-427 — keep identical so deploys are interchangeable.
fmt.Sprintf("a%x", sha256.Sum256([]byte(userID+externalID)))[0:9]
```

The launch handler has `userID` (looked up from the job) and `externalID`
(the job's `InvocationID`), so this is a pure local computation.

**Planned end date.** timelord computes it as `start_date + SUM(tools.time_limit_seconds)`,
defaulting to 72 h when no tool sets a limit
(`timelord/analyses.go:517-524`). Port that query to app-exposer (likely
into a small helper in `apps/apps.go` or `incluster/incluster.go`), use
`time.Now()` as the start anchor at launch time (it matches what apps
would write to `jobs.start_date`), and write the resulting timestamp
into `job_runtime`.

Three writes (operator_id, planned_end_date, subdomain) — combine into
one UPSERT so it's a single row touch:

```sql
INSERT INTO job_runtime (job_id, operator_id, planned_end_date, subdomain)
VALUES ($1, $2, $3, $4)
ON CONFLICT (job_id) DO UPDATE
SET operator_id      = EXCLUDED.operator_id,
    planned_end_date = EXCLUDED.planned_end_date,
    subdomain        = EXCLUDED.subdomain;
```

Same best-effort semantics as `SetOperatorID` today: on error, log and
continue. timelord's `Ensure*` paths remain as the safety net through
stage 1–2 of the rollout (see Deploy order).

### Time-limit extension shape

Replacement for the current `UPDATE ONLY jobs FROM (subquery) WHERE
jobs.id = $2 AND jobs.user_id = $1`:

```sql
UPDATE job_runtime
   SET planned_end_date = job_runtime.planned_end_date + interval '1 second' * $3
  FROM jobs
 WHERE job_runtime.job_id = $2
   AND jobs.id = job_runtime.job_id
   AND jobs.user_id = $1
RETURNING EXTRACT(EPOCH FROM
    job_runtime.planned_end_date AT TIME ZONE current_setting('TimeZone')
)::bigint
```

A `RowsAffected() == 0` means either user-mismatch or no `job_runtime`
row yet. The handler should map this to the same "not found / not yours"
404/403 the current code returns when the WHERE clause fails to match.

### Reads — JOIN against `job_runtime` (LEFT JOIN where absence is meaningful)

| Current site | Change |
|---|---|
| `apps/apps.go:106-110` (`analysisIDBySubdomainQuery`) | `SELECT job_id FROM job_runtime WHERE subdomain = $1` — drops the `jobs` reference entirely. |
| `apps/apps.go:290-294` + 298-310 (`getJobDebugInfoQuery`, `GetJobDebugInfo`) | LEFT JOIN `job_runtime jr ON jr.job_id = j.id`, project `jr.operator_id`. |
| `apps/apps.go:312-316` + 323-334 (`getOperatorIDQuery`, `GetOperatorID`) | `SELECT operator_id FROM job_runtime WHERE job_id = $1`. Caller in `httphandlers/handlers.go:105` (fast-path lookup in `operatorClientForAnalysis`) doesn't change. |
| `incluster/incluster.go:212-219` (time-limit GET) | LEFT JOIN `job_runtime` for `planned_end_date`; keep the `jobs` filter for `user_id`. |

### Critical files

- `app-exposer/apps/apps.go` — `SetOperatorID`, `setMillicoresReserved`,
  `GetOperatorID`, `GetJobDebugInfo`, `GetAnalysisIDBySubdomain`. Plus a
  new helper for computing initial `planned_end_date` (port the tool
  time-limit SUM query from `timelord/analyses.go:517-524`).
- `app-exposer/db/db.go` — `SetMillicoresReservedByAnalysisID`.
- `app-exposer/incluster/incluster.go` — time-limit GET/SET (the two
  queries at lines 201–219).
- `app-exposer/httphandlers/launch.go` — extend the `launchAsync`
  post-dispatch block (currently just `SetOperatorID` at lines 180–182)
  to compute and write `planned_end_date` and `subdomain` as part of a
  single combined UPSERT into `job_runtime`.

(`reconciler/reconciler.go` still doesn't need changes — `c12fe99`
already removed the backfill.)

## timelord changes

timelord stays in its current single-file style (`analyses.go`); no
restructuring as part of this work.

### Writes — rebased onto `job_runtime` initially, then removed entirely

In stage 1 of the rollout (see Deploy order) timelord's two write paths
are rebased onto `job_runtime` so they continue to function as a safety
net while we verify the launch handler is reliably setting both fields.
In stage 3, after metrics/logs confirm the safety net never fires in
production, both writes (and the `Ensure*` helpers that call them) are
deleted; the AMQP message handler becomes a no-op for the
subdomain/planned-end-date branches.

| Current site | Stage 1 (rebase) | Stage 3 (delete) |
|---|---|---|
| `analyses.go:441` (`setPlannedEndDateMutation`) | UPSERT into `job_runtime` | Remove `setPlannedEndDateMutation`, `setPlannedEndDate`, `EnsurePlannedEndDate`, and the call site in `CreateMessageHandler` (`analyses.go:648`). |
| `analyses.go:429` (`setSubdomainMutation`) | UPSERT into `job_runtime` | Remove `setSubdomainMutation`, `setSubdomain`, `EnsureSubdomain`, `generateSubdomain` (`analyses.go:425`), and the call site in `CreateMessageHandler` (`analyses.go:642`). |

After stage 3 timelord has zero writes against `jobs` or `job_runtime`.

### Reads

| Current site | Change |
|---|---|
| `analyses.go:186, 197` (expiring-analyses query) | LEFT JOIN `job_runtime`; use `jr.planned_end_date` in both SELECT list and WHERE. |
| `analyses.go:243, 255` (analyses-by-user query) | Same JOIN treatment. |
| `analyses.go:302, 313-314` (analyses-in-window query) | Same JOIN treatment, including both bounds in the WHERE. |
| `analyses.go:368` (single-analysis lookup) | Same JOIN treatment. |

All reads should treat a missing `job_runtime` row as "no planned end
yet" — equivalent to today's `NULL` behavior — which falls out naturally
from `LEFT JOIN`.

### Critical files

- `timelord/analyses.go` — both write statements and the seven read sites
  listed above.

## apps / analyses / job-status-* services

No changes. Confirmed by grep across all sibling repos:

- `apps` (Clojure) source has zero references to `operator_id`,
  `millicores_reserved`, `planned_end_date`, or `subdomain`.
- `analyses` does not touch the `jobs` table.
- `job-status-to-apps-adapter` and `job-status-listener` (both recently
  added to the session) have zero references to any of the four columns.

## Deploy order

Four stages, separated to keep timelord's safety net in place while we
verify the launch handler is reliable.

**Stage 1 — Schema + code.**

1. Apply migration `000050_job_runtime_table.up.sql` (creates table,
   copies existing data, rebases dependent views).
2. Deploy `app-exposer` and `timelord` with the new code. Order between
   them is not load-bearing — they UPSERT independently into
   `job_runtime` keyed by `job_id`. After this stage:
   - app-exposer launch handler writes `operator_id`, `planned_end_date`,
     and `subdomain` to `job_runtime`.
   - timelord's `EnsurePlannedEndDate` / `EnsureSubdomain` still run on
     the first Running AMQP message, but UPSERT into `job_runtime`. They
     act as a safety net for any analysis that escaped the launch
     handler's write.

Rolling-deploy inconsistency notes:
- `operator_id`: no auto-backfill (`backfillOperatorID` was removed in
  `c12fe99`). Missing values trigger the existing fan-out search at
  `handlers.go:84-99` — one extra RPC per affected analysis until the
  next launch writes the value.
- `planned_end_date` / `subdomain`: timelord's `Ensure*` safety net fills
  in any value the launch handler missed (old app-exposer pod, write
  error, etc.).
- `millicores_reserved`: lost write means brief quota inaccuracy until a
  re-launch. No auto-recovery path; acceptable.

**Stage 2 — Observe.** Run for at least one full release cycle. Watch
timelord logs/metrics for any case where `Ensure*` actually had to set a
value (i.e. found `planned_end_date` or `subdomain` empty for a
freshly-launched analysis). The expectation is that the safety net never
fires in steady state once stage 1 has fully rolled out.

**Stage 3 — Remove the safety net.** Deploy a timelord build with
`EnsurePlannedEndDate`, `EnsureSubdomain`, `setPlannedEndDateMutation`,
`setSubdomainMutation`, and `generateSubdomain` deleted, along with the
two call sites in `CreateMessageHandler` (`analyses.go:642, 648`).
timelord now performs zero writes against `jobs` or `job_runtime`.

**Stage 4 — Drop the orphaned columns.** Apply migration
`000051_drop_orphaned_jobs_columns.up.sql`. The two dependent views were
already rebased in stage 1, so the drop is dependency-free.

## Verification

### End-to-end (QA)

Use `~/.kube/qa.conf` per repo conventions.

- **Launch path**: Launch a VICE analysis. Verify (immediately after
  launch, before any AMQP `Running` status message has been processed):
  - `SELECT * FROM job_runtime WHERE job_id = '<id>'` shows
    `operator_id`, `planned_end_date`, and `subdomain` all populated by
    the launch handler. `millicores_reserved` may still be 0 (it's
    written asynchronously by the queue worker once the pod reports its
    CPU request).
  - The analysis is reachable through normal subdomain routing.
  - timelord logs do NOT show an `Ensure*` fire-and-set event for this
    analysis on the first Running message — the safety net should
    observe both fields already populated.
- **Time limit**: Hit the time-limit-extension endpoint. Verify
  `job_runtime.planned_end_date` advances, and the GET endpoint returns
  the new value.
- **Termination (the original bug)**: Trigger a terminate. Under
  reasonable concurrent load (multiple analyses in flight, status
  callbacks happening), the terminate should complete promptly with no
  apparent blocking. Compare wall-clock latency against pre-migration
  baseline if available.
- **Batch cancel**: Launch a batch, cancel it. Verify all child analyses
  transition out of running cleanly.
- **Lock-wait sanity**: With `log_lock_waits = on`, watch the PostgreSQL
  log for `process N still waiting for ShareLock on transaction X` lines
  on the `jobs` table. Expectation: those messages stop appearing for
  `jobs`-row contention between services.

### Automated tests

- `app-exposer`: update existing handler/db tests for the four
  changed code paths. The `apps/apps.go` and `db/db.go` methods have
  table-driven test patterns already; extend them to exercise the new
  UPSERT queries.
- `timelord`: extend existing tests in `analyses.go` (per
  `notifications_test.go` / `users_test.go` patterns) to cover the
  JOIN-based reads.
- Run `golangci-lint run ./...` in both repos before deploying.

### Rollback

Stage-dependent. The earlier the failure, the simpler the rollback.

**Stage 1 failure (app-exposer + timelord deployed, problems observed).**

1. Roll back both service deploys. Old code writes to `jobs.X` again.
2. Don't roll back migration 000050. `job_runtime` is harmless once
   nothing reads or writes it; the rebased view definitions still work
   (they `LEFT JOIN` an empty/stale `job_runtime`, returning `NULL` for
   those columns, which matches the pre-migration view behavior for any
   analysis that lacked those values).
3. If data in `jobs.X` is stale for analyses written only by the new
   code, run a one-off backfill:
   ```sql
   UPDATE jobs j
   SET operator_id         = jr.operator_id,
       millicores_reserved = jr.millicores_reserved,
       planned_end_date    = jr.planned_end_date,
       subdomain           = jr.subdomain
   FROM job_runtime jr
   WHERE j.id = jr.job_id;
   ```

**Stage 3 failure (timelord safety net removed, new gaps appearing).**

Re-deploy the stage-1 timelord build to restore the `Ensure*` safety
net. No data backfill needed — the safety net runs on the next AMQP
Running message and fills in any missing values.

**Stage 4 failure (orphaned columns dropped, downstream consumer
breaks).** Apply `000051_drop_orphaned_jobs_columns.down.sql` to restore
the columns and data from `job_runtime`. Investigate which consumer was
relying on the orphaned columns directly rather than reading through the
views or `job_runtime`, and fix forward.
