-- AIMUX-21 T002: add parent_task_id for Loom sub-task tree observability.
--
-- The executable migration lives in loom/store.go because Loom's current
-- migration framework is Go-constant driven. This file is the release artifact
-- named by the AIMUX-21 taskbook.

-- up
ALTER TABLE tasks ADD COLUMN parent_task_id TEXT REFERENCES tasks(id);
CREATE INDEX IF NOT EXISTS idx_tasks_parent_task_id ON tasks(parent_task_id);

-- down
DROP INDEX IF EXISTS idx_tasks_parent_task_id;
ALTER TABLE tasks DROP COLUMN parent_task_id;
