# Managed service execution through Workload

Gateway pins `github.com/webong/workload v0.2.1`. Planner continues to own desired state,
replica placement, credentials, target authorization, worker sessions and observations.
Workload owns reconciliation, process supervision and the local, Docker and Kubernetes drivers.
Arranger defines services without importing Workload.

```text
Planner reserves a stable replica slot and grants a bounded permission
  Gateway validates the complete snapshot, persistent owner and worker session
    Workload reconciles that slot and confirms termination before replacement
      Gateway publishes and withdraws the instance route
```

Assignment IDs identify a server's replica slot across revisions. A reservation is
written before an assignment is returned. An expired heartbeat cannot move a reserved
slot to another node. A different persistent worker owner cannot claim an old node,
even after its session expires. This deliberately prefers an unavailable service to
simultaneous execution on two nodes.

Every response carries `workload/v1`, the confirmed owner/session, an issuance timestamp
and a permission expiry timestamp. Each poll carries a fresh random nonce that Planner
echoes; a response from an earlier poll cannot renew permission. Gateway measures the grant from request start,
caps it at its configured coordination lease and subtracts 100 ms. The full request
round trip consumes this budget; remote wall-clock skew cannot widen it. Failed,
missing, null, malformed and expired responses never become an empty desired snapshot
or renew cached permission. Workload withdraws routes and stops execution when permission
expires.

## Worker state and supervisor

Set `GATEWAY_PROVISIONING_STATE_DIR` to an absolute path on private durable storage.
The directory contains a separate private journal for each target/node pair, including
a persistent owner identity and an exclusive controller lock. Mount the same journal
when replacing a worker with the same node ID. Do not clone an active journal, run the
same owner on multiple machines, or delete a journal to bypass an ownership conflict.
The local lock protects controllers sharing that filesystem; it is not a distributed
fencing mechanism. Cross-node failover requires confirmed termination and release.

For local execution, `GATEWAY_PROVISIONING_SUPERVISOR_BINARY` is an absolute path to
`gateway-workload-supervisor`. Its default is beside the host executable. Build it with:

```sh
GOWORK=off go -C pkg/spinner build -o /absolute/path/gateway-workload-supervisor ./cmd/workload-supervisor
```

On macOS, start local workers with `TMPDIR=/tmp` (or another short temporary path).
The supervisor socket path must fit within 100 bytes; the default macOS per-user
temporary path may exceed that limit. Gateway rejects this configuration before
claiming a worker session. The supervisor creates its own private socket directory.

The supervisor owns the service process group independently of the controller. A worker
restart recovers the supervisor's journal and verifies execution identity before adoption.
All Docker host images include the supervisor; the standalone worker image stores its
journal under `/data/workload`. Persist `/data` across replacement workers.

A failed stop or cleanup retains ownership and resources for retry. `StopContext` returns
that failure. The standalone worker exits unsuccessfully rather than releasing its session
as though cleanup completed. A failed Planner release also retains the local fence.

## Extension authoring

Existing `Provisioner.Launch` definitions keep their single-port interface. Optional
`PlacementProvisioner.LaunchPlacement` receives reusable named ports during recovery.
Executable integrations receive this additive `ports` map through the Arranger protocol.
A launch may declare named endpoints or the legacy `Port`, and may declare memory,
CPU and PID limits. Workload validates and enforces supported limits. Local execution
rejects limits it cannot enforce. Multiple endpoints require one named `default` for
Gateway routing; all observed endpoints are reported to Planner.

## Upgrade and rollback

1. Back up Planner and each worker journal. Inventory the running services and their
   legacy `gateway.provisioning.*` Docker/Kubernetes labels. New resources use
   `workload.provisioning.*` Docker labels and `workload.webong.dev/*` Kubernetes labels;
   they are not interchangeable.
2. Stop new desired-state changes. Stop/drain the legacy worker and each legacy service.
   Verify actual process/container/pod termination, including any disconnected worker.
   A database reset, expired heartbeat or a `stopped` row alone is insufficient proof.
3. Upgrade Planner and run its migrations. The new columns are additive. Schema adoption
   marks only the original core baseline; later core migrations still execute.
4. Start the replacement with the same intended node ID and durable journal. For an
   existing legacy node, set `GATEWAY_PROVISIONING_LEGACY_DRAINED=true` only after step 2.
   Planner also requires every existing instance on that node to be stopped. Remove the
   flag after adoption. This flag does not drain services or fence another machine.
5. Recreate desired services through Workload. Verify routes, observations, expiry stops,
   restart adoption and cleanup before accepting operator traffic.
6. To roll back, stop desired services through Workload, confirm all resources terminated,
   and finish worker shutdown/session release. Then roll back Planner and workers together
   and recreate services with the legacy executor. Keep backups/journals; never run the old
   and new executors against the same live workload. A rollback after unconfirmed cleanup
   is blocked until the resources are inspected and fenced.

The implementation branch must remain a draft until local/Linux, Docker, Kubernetes,
packaged worker, database and host-matrix acceptance finishes. This source migration does
not authorize publishing archives, packages, tags or images, or deploying an operator host.
