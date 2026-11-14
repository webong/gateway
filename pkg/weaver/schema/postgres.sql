CREATE TABLE IF NOT EXISTS workflow_rpc_states (
    subject_type TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    flow_key TEXT NOT NULL,
    state JSONB NOT NULL,
    initialized BOOLEAN NOT NULL DEFAULT FALSE,
    lock_version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (subject_type, subject_id, flow_key)
);
