CREATE TABLE IF NOT EXISTS worker_sessions (
    id TEXT PRIMARY KEY NOT NULL,
    tenant_id TEXT NOT NULL,
    engine_name TEXT NOT NULL,
    project_id TEXT NOT NULL,
    canonical_worktree_root TEXT NOT NULL,
    profile_fingerprint TEXT NOT NULL,
    capability_fingerprint TEXT NOT NULL,
    requested_mode TEXT NOT NULL,
    provider_name TEXT,
    provider_session_id TEXT,
    provider_session_generation INTEGER,
    state TEXT NOT NULL DEFAULT 'available',
    active_task_id TEXT,
    lease_owner TEXT,
    lease_generation INTEGER NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
    lease_expires_at TEXT,
    parent_worker_session_id TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    closed_at TEXT,
    FOREIGN KEY (active_task_id) REFERENCES tasks(id),
    FOREIGN KEY (parent_worker_session_id) REFERENCES worker_sessions(id),
    CHECK ((provider_name IS NULL AND provider_session_id IS NULL AND provider_session_generation IS NULL) OR (provider_name IS NOT NULL AND provider_session_id IS NOT NULL AND provider_session_generation > 0)),
    CHECK ((requested_mode = 'fork' AND parent_worker_session_id IS NOT NULL) OR (requested_mode <> 'fork' AND parent_worker_session_id IS NULL)),
    CHECK ((state = 'leased' AND active_task_id IS NOT NULL) OR (state <> 'leased' AND active_task_id IS NULL)),
    CHECK ((state = 'leased' AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL) OR (state <> 'leased' AND lease_owner IS NULL AND lease_expires_at IS NULL)),
    CHECK ((state = 'closed' AND closed_at IS NOT NULL) OR (state <> 'closed' AND closed_at IS NULL)),
    CHECK (requested_mode IN ('new','exact_resume','fork')),
    CHECK (state IN ('available','leased','closed','unavailable'))
);

CREATE TABLE IF NOT EXISTS worker_run_bindings (
    id TEXT PRIMARY KEY NOT NULL,
    task_id TEXT NOT NULL,
    worker_session_id TEXT,
    tenant_id TEXT NOT NULL,
    engine_name TEXT NOT NULL,
    project_id TEXT NOT NULL,
    requested_mode TEXT NOT NULL,
    executor_name TEXT NOT NULL,
    provider_name TEXT,
    provider_session_id TEXT,
    provider_session_generation INTEGER,
    provider_connection_generation INTEGER,
    swarm_scope TEXT NOT NULL,
    swarm_handle_id TEXT,
    swarm_handle_generation INTEGER,
    swarm_registry_generation INTEGER,
    execution_id TEXT,
    process_pid INTEGER,
    process_start_fingerprint TEXT,
    process_tree_id TEXT,
    state TEXT NOT NULL DEFAULT 'reserved',
    lease_owner TEXT,
    lease_generation INTEGER NOT NULL DEFAULT 0 CHECK (lease_generation >= 0),
    lease_expires_at TEXT,
    reconciliation_classification TEXT,
    terminal_reason TEXT,
    created_at TEXT NOT NULL,
    started_at TEXT,
    returned_at TEXT,
    terminal_at TEXT,
    updated_at TEXT NOT NULL,
    FOREIGN KEY (task_id) REFERENCES tasks(id),
    FOREIGN KEY (worker_session_id) REFERENCES worker_sessions(id),
    CHECK ((provider_name IS NULL AND provider_session_id IS NULL AND provider_session_generation IS NULL) OR (provider_name IS NOT NULL AND provider_session_id IS NOT NULL AND provider_session_generation > 0)),
    CHECK (provider_connection_generation IS NULL OR (provider_connection_generation > 0 AND provider_name IS NOT NULL)),
    CHECK ((swarm_handle_id IS NULL AND swarm_handle_generation IS NULL AND swarm_registry_generation IS NULL) OR (swarm_handle_id IS NOT NULL AND swarm_handle_generation > 0 AND swarm_registry_generation > 0)),
    CHECK ((process_pid IS NULL AND process_start_fingerprint IS NULL AND process_tree_id IS NULL) OR (process_pid > 0 AND process_start_fingerprint IS NOT NULL AND process_tree_id IS NOT NULL)),
    CHECK ((requested_mode = 'stateless' AND worker_session_id IS NULL) OR (requested_mode <> 'stateless' AND worker_session_id IS NOT NULL)),
    CHECK ((state IN ('reserved','running','returned','cancelling') AND lease_owner IS NOT NULL AND lease_expires_at IS NOT NULL) OR (state = 'terminal' AND lease_owner IS NULL AND lease_expires_at IS NULL)),
    CHECK ((state = 'terminal' AND terminal_at IS NOT NULL) OR (state <> 'terminal' AND terminal_at IS NULL)),
    CHECK (requested_mode IN ('stateless','new','exact_resume','fork')),
    CHECK (state IN ('reserved','running','returned','cancelling','terminal'))
);

CREATE INDEX IF NOT EXISTS idx_worker_sessions_lookup
    ON worker_sessions(tenant_id,engine_name,project_id,canonical_worktree_root,profile_fingerprint,capability_fingerprint,state);

CREATE INDEX IF NOT EXISTS idx_worker_run_bindings_reconcile
    ON worker_run_bindings(tenant_id,engine_name,project_id,state,updated_at);

CREATE UNIQUE INDEX IF NOT EXISTS idx_worker_run_bindings_active_session
    ON worker_run_bindings(worker_session_id)
    WHERE worker_session_id IS NOT NULL AND state IN ('reserved','running','returned','cancelling');
