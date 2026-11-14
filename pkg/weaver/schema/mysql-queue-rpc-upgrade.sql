-- Maintenance operation for an existing RPC registry before enabling the queue authority.
-- Back up and audit existing subject identities first. This cannot recover data that
-- previously collided under a case-insensitive collation. Do not run on each request.
ALTER TABLE workflow_rpc_states CONVERT TO CHARACTER SET utf8mb4 COLLATE utf8mb4_bin;
