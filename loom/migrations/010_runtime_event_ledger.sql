-- AIMUX-26 CR-001: task-local runtime-event sequence and restartable v10 ledger.
--
-- This file is the release/audit representation of the executable migration
-- in loom/store.go. Do not execute it as one monolithic script: the Go runner
-- classifies an already-added column, holds each phase on one pinned connection,
-- and repeats the bounded backfill transaction with durable parameters.

-- expansion (one pinned BEGIN IMMEDIATE)
BEGIN IMMEDIATE;

ALTER TABLE task_artifacts ADD COLUMN event_seq INTEGER;

CREATE TABLE IF NOT EXISTS loom_migrations (
    version INTEGER PRIMARY KEY,
    state TEXT NOT NULL CHECK (state IN ('running','complete')),
    checkpoint_seq INTEGER NOT NULL,
    source_rows INTEGER NOT NULL,
    processed_rows INTEGER NOT NULL,
    batch_count INTEGER NOT NULL,
    started_at DATETIME NOT NULL,
    completed_at DATETIME
);

INSERT INTO loom_migrations(
    version,state,checkpoint_seq,source_rows,processed_rows,batch_count,started_at,completed_at
)
SELECT 10,'running',0,count(*),0,0,CURRENT_TIMESTAMP,NULL
FROM task_artifacts
WHERE event_seq IS NULL
ON CONFLICT(version) DO NOTHING;

DROP TRIGGER IF EXISTS trg_task_artifacts_event_seq_steady;
DROP TRIGGER IF EXISTS trg_task_artifacts_event_seq_migrating;
CREATE TRIGGER trg_task_artifacts_event_seq_migrating
AFTER INSERT ON task_artifacts
FOR EACH ROW WHEN NEW.event_seq IS NULL
BEGIN
    UPDATE task_artifacts
    SET event_seq = (
        SELECT COUNT(*)
        FROM task_artifacts AS prior
        WHERE prior.task_id = NEW.task_id
          AND prior.seq <= NEW.seq
    )
    WHERE seq = NEW.seq;
END;

COMMIT;

-- bounded backfill (the Go runner repeats this pinned transaction)
--
-- 1. Read checkpoint_seq for version 10.
-- 2. Select at most 256 NULL rows with seq > checkpoint_seq in global seq order.
-- 3. Assign each selected row its ordinal among rows of that task ordered by
--    immutable global seq.
-- 4. In the same transaction, advance checkpoint_seq to the batch maximum,
--    add the exact affected count to processed_rows, and increment batch_count
--    once. A zero-row batch does not mutate the ledger.

-- final cutover (one pinned BEGIN IMMEDIATE, after live validation)
--
-- The Go runner first requires source_rows=processed_rows, zero NULL event_seq,
-- and zero duplicate (task_id,event_seq) pairs. Only then does it execute the
-- exact index/trigger/ledger transition below.
BEGIN IMMEDIATE;

DROP INDEX IF EXISTS idx_task_artifacts_task_event_seq;
CREATE UNIQUE INDEX idx_task_artifacts_task_event_seq
    ON task_artifacts(task_id,event_seq);

DROP TRIGGER IF EXISTS trg_task_artifacts_event_seq_migrating;
DROP TRIGGER IF EXISTS trg_task_artifacts_event_seq_steady;
CREATE TRIGGER trg_task_artifacts_event_seq_steady
AFTER INSERT ON task_artifacts
FOR EACH ROW WHEN NEW.event_seq IS NULL
BEGIN
    UPDATE task_artifacts
    SET event_seq = COALESCE((
        SELECT MAX(prior.event_seq)
        FROM task_artifacts AS prior
        WHERE prior.task_id = NEW.task_id
          AND prior.seq <> NEW.seq
    ), 0) + 1
    WHERE seq = NEW.seq;
END;

UPDATE loom_migrations
SET state='complete', completed_at=COALESCE(completed_at, CURRENT_TIMESTAMP)
WHERE version=10 AND state='running' AND source_rows=processed_rows;

COMMIT;

-- down (additive rollback; shipped writers remain valid before the column drops)
BEGIN IMMEDIATE;

DROP TRIGGER IF EXISTS trg_task_artifacts_event_seq_migrating;
DROP TRIGGER IF EXISTS trg_task_artifacts_event_seq_steady;
DROP INDEX IF EXISTS idx_task_artifacts_task_event_seq;
DELETE FROM loom_migrations WHERE version=10;
ALTER TABLE task_artifacts DROP COLUMN event_seq;

COMMIT;
