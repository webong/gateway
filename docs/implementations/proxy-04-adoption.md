# Proxy 0.4 installation and ownership

Gateway provisions one immutable Proxy monorepo snapshot at `app/ext/proxy`.
`tools/proxy/source.json` records its commit, archive URL and SHA-256. Run:

```sh
scripts/composer-install --prefer-dist                  # locked author install
scripts/composer-install --working-dir=app --no-dev --prefer-dist
```

The helper provisions and verifies the source before invoking Composer. A changed
checkout is refused rather than overwritten. Preserve edits before explicitly
removing/reprovisioning it. Docker builds exclude the host copy and provision the
same verified archive inside each dependency builder. Keep the relative checkout
path present for later cold installs and deployments, including installs with
plugins disabled. Proxy's manifests declare 0.4.0; no new release is created.

For an intentional dependency update from an empty vendor directory, first run
`scripts/bootstrap-composer`. This installs the locked, trusted Composer Source
Plugin seed before the main solve. Then run a targeted Composer update. Bootstrap
does not update the application's dependency lock. The plugin remains author
tooling; the bundled application's production lock does not require it.

The repository root retains its Gateway workspaces and includes `app/ext/*` and
`app/ext/*/adapters/laravel`. The bundled app owns its separate configuration with
`ext/*` and `ext/*/adapters/laravel`. Patterns are relative to each consumer root;
parent traversal is not supported. Globs let the Git-only Gateway publisher ignore
untracked provisioned dependencies. No Proxy source is committed or split by the
Gateway publisher. Copy mode is explicit; reinstall affected packages after edits.

## Dependency boundary

- `webong/proxy:^0.4`: framework-neutral transports, portable profiles and registry
  records/contracts. Neutral Planner installation does not install Laravel or Spatie.
- `webong/proxy-laravel:^0.4`: Laravel persistence, encryption, models, providers,
  migrations and its existing Spatie integration. Web, DNS, Email and Shell use
  these adapter-owned APIs. The bundled host and author test project require it.
- Go consumers use the released `github.com/webong/proxy/go v0.4.0` module. Wire
  profiles remain version 1. No additional transport capability is enabled.

Gateway-specific webhook routing is registered by Web's `WebServiceProvider` and
`WebhookRouter`. Planner's host configuration retains only general Proxy settings.
The shared empty Planner Proxy router remains available to non-Web extensions and
old cached configurations. Host router/channel overrides win. Proxy's provider,
models and queued job identities are reused; no copied Spatie provider or job is
introduced, and the paused extraction draft is not merged.

Registry migrations now load from `vendor/webong/proxy-laravel/database/migrations`.
Existing migration names, tables, identifiers, ciphertext, credentials, subscriptions,
profiles and queued payloads remain owned by Proxy. Coordinate writers when applying
its subscription-lock migration. Before rollback, stop ingress, drain workers and
restore matching dependency locks/source pins and configuration. Clear/rebuild
Laravel configuration/routes and regenerate Proxy discovery after the source
upgrade; never ship a developer-generated `app/planner/bootstrap/cache/proxy.php`
in a container image. Existing legacy router classes remain loadable during the
transition. Do not discard
registry tables, migration history or application keys.

Gateway continues to supply authorized dialers, TLS/trust, proxy grants, callback
CIDR ceilings, redirect policy, credential/header isolation, deadlines, cancellation
and body/stream limits. Shared Proxy clients describe connection settings and do
not replace these controls.

## Acceptance

- `scripts/test-proxy-provisioning`: immutable checksum, repeat install, corruption
  rejection and preservation of modified source.
- `scripts/test-proxy-neutral-install`: independent neutral Planner/core install.
- `scripts/test-composer-install-stability`: fresh author install and empty-vendor
  production lock replay with plugins disabled and byte-identical locks.
- `tests/Unit/WebProxyOwnershipTest.php`: Web ownership, single provider, host overrides.
- Existing multi-database registry/runtime and Go forwarding policy suites cover
  populated upgrades, TLS, allowed/denied requests and compatibility.

Plain Packagist 0.4.0 records still reference deleted distribution repositories.
This setup deliberately resolves provisioned source through Composer Source Plugin;
it neither repairs those public URLs nor adds a catalog or publication mechanism.
The frozen standalone v0.2.0-rc.1 artifacts are unchanged. Publication remains forbidden.
