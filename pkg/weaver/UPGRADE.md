# Standalone cutover and rollback

This is an explicit offline **same-engine** copy, tested against the standalone
application at `00e0bbcdd61c2c6ed96af55865d0ef2e77557adb` and Weaver operational
schema 1. It does not rewrite an existing database, change run authority, or adopt
old tables during activation. Rehearse with disposable copies before production.

## Cutover

1. Stop old RPC ingress, queue workers and outbox publishers. Drain or reconcile
   every claimed/executing attempt and delivering outbox reservation. Do not
   discard uncertain provider effects; their idempotency policy determines replay.
   Deferred steps may remain outstanding; preserve their callback bodies.
2. Prepare a fresh database on the **same SQL engine**. Existing operational tables,
   including an interrupted installation, are rejected. Use read-only source
   credentials. SQLite source opens read-only without creating a file or changing
   journal mode. Preserve the complete database/WAL snapshot when making backups.
3. Create a private mode-0600 context outside the package. Its shape is:

   ```json
   {
     "source": {"dsn": "sqlite:/private/standalone.sqlite"},
     "destination": {"dsn": "sqlite:/private/weaver.sqlite"},
     "configuration": {"schema_version": 1, "database_ref": "database", "authentication_ref": "rpc", "operations": ["gateway.approval", "gateway.complete"], "accounts": []},
     "assignments": [{"account": "account-a", "subject": {"type": "order", "id": "42"}}]
   }
   ```

   Replace `configuration` with the complete approved runtime projection, including
   published definitions and explicit grants. Each original subject needs exactly
   one configured account assignment and an explicit flow/subject grant. Source and
   destination may use PDO username/password fields; keep TLS options in their DSNs.
4. Run the packaged CLI with the bundled PHP/loader/ini when using the minimal
   artifact (or PHP 8.3 with matching PDO in the Composer consumer):

   ```sh
   GATEWAY_WEAVER_CUTOVER_CONTEXT=/private/cutover.json php bin/cutover --copy --writers-stopped
   ```

5. Verify imported identities, pinned definitions/context, attempt and callback
   receipts, outbox IDs and provider-effect keys. Configure the destination through
   Weaver's scoped secret. Activate Weaver and switch the private endpoint only
   after the old writer is stopped. Callback correlation is unchanged; transport
   authentication now selects the assigned account and requires its Weaver token.
   Check old credentials are rejected, then retry outstanding callbacks.

The copier preserves queue records verbatim, including physical execution IDs and
claim payloads. It adds only a private routing import marker binding the original
subject to the assigned account. Runtime scope validation checks that binding;
public RPC hides it. Pinned runs may precede the current published version, but
all their operations must still be deployed and approved. Missing queue rows,
inline/Temporal authority, uninitialized/unrouted records, ambiguous assignments
and unsupported definitions stop the copy. MySQL requires the audited binary
`utf8mb4_bin` identity schema; previously merged case-insensitive identities cannot
be reconstructed by this tool.

The source snapshot is consistent and read-only; stopping writers is an operator
precondition, not a distributed lock acquired by the CLI. MySQL DDL is a separate
phase: a failed destination may contain empty tables and must be reconciled or
replaced with a new database before retry. No automatic cleanup deletes it.

## Rollback

Stop Weaver ingress, workers and publishers first and drain/reconcile claims and
reservations. Create another private mode-0600 context containing `source`
(the current Weaver database) and `destination` (a fresh legacy database), then:

```sh
GATEWAY_WEAVER_CUTOVER_CONTEXT=/private/rollback.json php bin/cutover --rollback --writers-stopped
```

Rollback copies **current progress**, restores original routing subjects, removes
only the import marker and omits Weaver's schema marker. The old queue authority
can therefore see callbacks and effects completed after cutover. Verify it with
the compatible original application before switching the endpoint back. Do not
restore a stale pre-cutover backup over completed external effects.

Automatic legacy rollback stops once Weaver-native runs exist, or if any queue
record lacks an imported route. Those records require explicit reconciliation
and an agreed migration path; the CLI never guesses or converts their authority.
Keep old and new writers mutually exclusive throughout. Do not delete the old
application until a released replacement and its installation acceptance exist.
