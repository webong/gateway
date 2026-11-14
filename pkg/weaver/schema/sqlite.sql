CREATE TABLE IF NOT EXISTS workflow_rpc_states (
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    flow_key TEXT NOT NULL,
    state TEXT NOT NULL,
    initialized INTEGER NOT NULL DEFAULT 0,
    lock_version INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (subject_type, subject_id, flow_key)
);
