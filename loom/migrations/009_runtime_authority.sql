-- AIMUX-26 CR-001: durable single-writer runtime authority.
--
-- The executable, repeatable migration lives in loom/store.go. It classifies
-- duplicate-column errors deliberately and inspects the named provider index
-- before repairing a legacy two-column definition without rewriting rows.
-- The executable path pins one connection and holds BEGIN IMMEDIATE across
-- index inspection, replacement, and commit so no missing-index window exists.

-- up
BEGIN IMMEDIATE;

ALTER TABLE tasks ADD COLUMN cancel_requested_at DATETIME;

CREATE TABLE IF NOT EXISTS pending_actions (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id),
    kind TEXT NOT NULL,
    status TEXT NOT NULL,
    provider_request_id TEXT NOT NULL,
    connection_generation INTEGER NOT NULL,
    request_json TEXT NOT NULL,
    response_json TEXT,
    delivery_json TEXT,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    responded_at DATETIME,
    resolved_at DATETIME
);

-- Repair the pre-v9 same-name two-column index, if present.
DROP INDEX IF EXISTS idx_pending_actions_provider_generation;
CREATE UNIQUE INDEX idx_pending_actions_provider_generation
    ON pending_actions(task_id, provider_request_id, connection_generation);

COMMIT;

-- down
BEGIN IMMEDIATE;

DROP INDEX IF EXISTS idx_pending_actions_provider_generation;
DROP TABLE IF EXISTS pending_actions;
ALTER TABLE tasks DROP COLUMN cancel_requested_at;

COMMIT;
