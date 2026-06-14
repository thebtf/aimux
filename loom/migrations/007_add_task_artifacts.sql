-- AIMUX-23 CR-001: add Loom task artifact projection rows.
--
-- The executable migration lives in loom/store.go because Loom's current
-- migration framework is Go-constant driven. This file is the release/audit
-- artifact named by the AIMUX-23 taskbook.

-- up
CREATE TABLE IF NOT EXISTS task_artifacts (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    kind TEXT NOT NULL,
    event_type TEXT NOT NULL DEFAULT '',
    summary TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    content_length INTEGER NOT NULL DEFAULT 0,
    redacted INTEGER NOT NULL DEFAULT 0,
    truncated INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_task_artifacts_task_seq ON task_artifacts(task_id, seq);

-- down
DROP INDEX IF EXISTS idx_task_artifacts_task_seq;
DROP TABLE IF EXISTS task_artifacts;
