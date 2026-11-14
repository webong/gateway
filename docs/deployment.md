# Deploying Gateway

For a single-service deployment, use the
[combined Router image](router.md#combined-router-docker-image): one image with
Spinner and Planner, one private `/data` volume, HTTPS on 8443, and TLS IMAP on
993. RoadRunner is the default runtime; select FrankenPHP at build time when
you want the Router to host PHP in-process. Planner APIs own the mailbox
connections and policies. No separate PHP Planner or PostgreSQL service is
needed for a single local replica. Do not point this deployment at a
standalone Spinner image.

## Published image tags

`webong/gateway` is published as the complete Gateway application. Public tags
identify only the product release:

- `latest` tracks the newest published release.
- `sha-<commit>` is the immutable build for a Git commit.
- `v<version>` is an immutable named release.

Do not publish capability, runtime, architecture, or transport names such as
`mail`, `router`, `javascript`, `arm64`, RoadRunner, or FrankenPHP in the tag.
The public tag points to one multi-platform manifest for `linux/amd64` and
`linux/arm64`. Runtime composition is image metadata and implementation detail,
not a different Gateway product.

The repository provides JavaScript, Lua, WebhookScript, Web, Email, DNS, and
the other Gateway Extensions. The host image remains extension-neutral: an
installable Spinner runtime is selected through Planner's `/extensions` CMS
instead of being compiled into every Router. The publisher defaults to the
bundled RoadRunner runtime used by Zorvia. Selecting FrankenPHP changes only
the runtime built into the image, not the repository or public tag. Publish from a clean commit with:

```bash
scripts/publish-gateway-image
# Select FrankenPHP instead of the default RoadRunner runtime:
GATEWAY_ROUTER_RUNTIME=frankenphp scripts/publish-gateway-image
# Or publish a named release as well as latest:
scripts/publish-gateway-image v1.0.0
```

The bundled Router starts through a short root-owned initializer, repairs the
ownership of an existing `/data` volume when the image's runtime UID changes,
removes image-specific Laravel caches, and then replaces itself with the
unprivileged Router process. The recursive ownership repair runs only when the
data directory's UID/GID differs from the runtime identity. In Kubernetes, the
supplied manifest continues to pin UID/GID 33 and the initializer performs only
the writable cache cleanup.

The split deployment below remains available when Spinner and Planner need to
scale independently:

```text
Internet -> TLS terminator -> Spinner (Go) -> private network -> Planner (PHP)
```

Only the TLS terminator/router is public. Keep the planner as a Compose-only
service, Kubernetes ClusterIP, loopback listener, or private-VPC service.
Spinner uses `GATEWAY_PLANNER_URL` and `GATEWAY_INTERNAL_TOKEN` to
call `POST /_internal/gateway/plan` privately.

The standalone Planner image supports conventional PHP-FPM and Laravel Octane.
Choose its application server with
`GATEWAY_PLANNER_SERVER=fpm|frankenphp|swoole|roadrunner`; the image contains
all four server prerequisites and defaults to FrankenPHP. This is a
deployment choice only: Spinner continues to use the same private HTTP Planner
contract and does not import or detect the selected PHP server. RoadRunner RPC
FrankenPHP's admin listener, and PHP-FPM's FastCGI listener remain private to
the Planner container. The image itself uses the official PHP CLI image as its
neutral base. PHP-FPM, Caddy, FrankenPHP, and RoadRunner are packaged as
optional server binaries rather than defining the image's base runtime; FPM
uses standard Caddy and does not invoke FrankenPHP.

## Spinner image

```bash
docker build -f infra/docker/Gateway.spinner.Dockerfile \
  -t gateway-spinner:local .
```

The standalone Spinner image does not contain the PHP Planner application.
Deploy both services on a private network with the same
`GATEWAY_INTERNAL_TOKEN`.

The image runs as the unprivileged `gateway` user and exposes:

| Port | Purpose |
| --- | --- |
| `5001/tcp` | HTTP and WebSocket |
| `2525/tcp` | Optional SMTP |
| `5353/tcp`, `5353/udp` | Optional authoritative DNS |

The Spinner image contains no PHP application. Deploy the Planner alongside it.

## Minimal Compose shape

```yaml
services:
  planner:
    image: your-laravel-planner
    environment:
      GATEWAY_INTERNAL_TOKEN: ${GATEWAY_INTERNAL_TOKEN}
      REGISTRY_TOKEN: ${REGISTRY_TOKEN}
    # Do not publish this service's ports.

  gateway:
    image: gateway-spinner:local
    environment:
      GATEWAY_RUNTIME: http
      GATEWAY_PLANNER_URL: http://planner:8080
      GATEWAY_INTERNAL_TOKEN: ${GATEWAY_INTERNAL_TOKEN}

  tls:
    image: caddy:2-alpine
    ports: ["443:443"]
    # Reverse proxy to gateway:5001.
```

Use a real certificate and hostname in production. `tls internal` is only for
local testing.

## Verify a deployment shape

```bash
# Router health and non-root image check.
make test-container

# Caddy TLS, private planner isolation, auth, subscription planning,
# private-target SSRF denial, and HTTP Email relay/receipt/DSN checks.
make test-container-integration

# Installable DNS process plus focused Planner DNS CMS coverage.
make test-dns-integration
```

The integration test deliberately confirms that a private Docker callback is
blocked. For a true end-to-end delivery test, use a controlled public HTTPS
receiver.

The Email slice of this test crosses public TLS, Spinner, Planner, the private
delivery-admin listener, and a disposable SMTP relay. It checks the envelope
and message at that relay, then confirms the delivered receipt survives a
Spinner restart, a same-key replay does not send a second copy, and a second
account cannot bypass the sender-domain quota. It also refuses one recipient
and checks the resulting null-path failure notice. This proves controlled
local relay and failure paths, not public-domain deliverability or a later
bounce after a relay accepted mail.
The Planner must trust only its private reverse proxy so Laravel can recognize
the original HTTPS scheme; do not trust arbitrary public `X-Forwarded-Proto`
headers or expose the Planner listener directly.

## Database support

| | SQLite | PostgreSQL | MySQL 8.4 |
| --- | --- | --- | --- |
| Standalone Planner (`scripts/planner-start`) | no | yes | yes |
| Bundled Router, registry on the spool path (`scripts/router-start`) | yes (default) | yes | yes |
| Router shared deliveries (`GATEWAY_DELIVERY_DATABASE_URL`) | no | **yes** | **no** |
| Gateway migrations (`gateway:migrate`) | yes | yes | yes (MariaDB: migrations only) |

The Router's shared delivery queue and its delivery-admin discovery are Go code
written in PostgreSQL SQL (`SKIP LOCKED` claims with `RETURNING`, `ON CONFLICT`,
`payload->>'adapter'`), so `scripts/router-start` refuses `DB_CONNECTION=mysql`
together with `GATEWAY_DELIVERY_DATABASE_URL`. A MySQL deployment runs the Router
on the spool path (`GATEWAY_DELIVERY_SPOOL_PATH`, the default). MariaDB is not
supported at runtime.

**Running the Planner on MySQL.** Create the database with the exact collation,
then point the launcher at it:

```sql
CREATE DATABASE gateway CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin;
```

```sh
DB_CONNECTION=mysql
DB_URL=mysql://gateway:secret@mysql.internal:3306/gateway
```

`scripts/planner-start migrate` then applies Gateway's schema, Proxy's migrations
and the application's, as it does on PostgreSQL. What differs from PostgreSQL, and
what Gateway does about it (`app/planner/config.php`, the `mysql` connection):

- **Exact comparisons.** MySQL's default collations ignore case, accents and
  trailing spaces. The tables `gateway:migrate` creates are `utf8mb4_0900_bin`,
  whatever the database default is, so keys, tokens and names compare exactly, as
  on PostgreSQL and SQLite. Tables made by Laravel-style migrations (Proxy's, the
  Email extension's) inherit the database default, which is why the database
  should be created with the same collation. `DB_COLLATION` changes the
  connection collation.
- **Dates past 2038.** The timestamp columns `gateway:migrate` creates are
  `DATETIME` on MySQL, not `TIMESTAMP`, which ends in 2038. Tables made by
  Laravel-style migrations keep `TIMESTAMP`: Proxy's (audit stamps such as
  `created_at` and `last_delivered_at`) and the Email extension's (`expires_at`,
  `lease_until`, `next_attempt_at`, `occurred_at`, which hold short retention and
  retry times). A date after 2038 in one of those columns is rejected.
- **UTC and READ COMMITTED.** The connection sets its time zone to `+00:00`
  (`DB_TIMEZONE`) and its isolation level to `READ COMMITTED` (`DB_ISOLATION_LEVEL`),
  PostgreSQL's default, instead of MySQL's `REPEATABLE READ`.
- **JSON text.** MySQL's `JSON` type rewrites stored text (key order, whitespace,
  escaped slashes). Nothing Gateway stores is hashed or signed from the stored
  text; request bodies are kept as base64 inside it.
- **TLS.** Set `MYSQL_ATTR_SSL_CA` to a CA bundle to verify the server.

`tests/Feature/PlannerMysqlRuntimeTest.php`, the MySQL cases in
`tests/Feature/WorkermanPlannerTest.php` and `scripts/test-launcher-databases`
cover this; they run when `GATEWAY_TEST_MYSQL_*` is set, as in CI. See the
[MySQL runtime note](verification/planner-mysql-runtime-2026-11-14.md) for the
measured runs and what was not run.

## Upgrading to Proxy 0.3.1 (subscription identity migration)

Gateway now requires `webong/proxy` `^0.3.1`. Proxy 0.3.1 adds one additive
migration, `2026_11_12_000001_use_portable_subscription_identity`, to the
subscription table that Planner's registry stores in. It replaces the original
seven-column unique key (which MySQL cannot index at utf8mb4, error 1071) with a
fixed 64-character `registration_hash`, and removes the old key. Every existing
subscription keeps its id, values, metadata and delivery state; the hash is
computed from them during the migration. Run it on every existing PostgreSQL or
SQLite registry, whichever way you start Gateway: the Router launcher, the
standalone Planner image and the Kubernetes Job already run
`artisan migrate --realpath --path=.../webong/proxy/database/migrations`, so the
migration is applied by the same `migrate` step as before. Nothing else changes
in how you start Gateway, and there is no new setting.

Code and schema must change together. Code that predates Proxy 0.3.1 does not
write the hash, and once the migration has run the column is `NOT NULL`, so the
old Planner can no longer create a subscription. Do not keep an old Planner
running against the migrated database, and do not run a new Planner before the
migration. A short outage of subscription writes is the expected cost:

1. **Back up** the registry database (for the Router, the whole data directory).
2. **Stop subscription writers**: clients of the registry API, any queue worker
   or script that creates, replaces or deactivates subscriptions, and the old
   Planner. Spinner can stay up. While Planner is down it cannot plan new
   routes, so stop Spinner too if you cannot accept failed ingress meanwhile.
3. **Migrate** with the new image or checkout, once:
   - Router (native): start the new Router; it migrates before it serves.
   - Standalone Planner image: `gateway-planner-start migrate` as a one-off
     command, or remove the completed Kubernetes `planner-migrate` Job and apply
     it with the new image tag. Run it once before scaling Planner replicas; the
     `serve` command also migrates at start, so do not start several new
     replicas against an unmigrated database.
4. **Start** the new Planner (and Spinner) and check `/ready`.

The migration is safe to repeat: applied migrations are recorded, and the
subscription change resumes after a partial failure, but it must run with the
writers stopped. It refuses an index that carries a known name but the wrong
columns rather than guessing; fix or drop that index and run it again. Custom
table names under `proxy.tables` are honoured. If your host published Proxy's
migrations with `vendor:publish` (Gateway's launchers do not), republish them
without overwriting local changes and review your copy of the subscriptions
creation migration before upgrading; a fresh MySQL install fails without
Proxy's small correction to it.

**Rolling back.** PostgreSQL and SQLite can roll the migration back inside a
transaction, but the old key cannot always represent data accepted since the
upgrade; then the rollback fails and keeps the rows and the hash. MySQL and
MariaDB refuse to roll back at all. Treat the pre-upgrade backup as the way
back, and never run Planner code older than 0.3.1 against the new schema.

**What is covered.** `tests/Feature/ProxyRegistryUpgradeTest.php` builds a Proxy
0.2.0 registry from its original migrations, adds subscriptions (including
case- and whitespace-distinct, Unicode and 255-character values), upgrades it
with the Proxy 0.3.1 migrations, and checks that every row is unchanged, hashed
and unique, the old key is gone and a second run does nothing. It runs on SQLite
always and on PostgreSQL when `GATEWAY_TEST_POSTGRES_*` is set. The same file
applies the migrations to a fresh MySQL 8.4 database with the default utf8mb4
settings when `GATEWAY_TEST_MYSQL_*` is set. See [Database
support](#database-support) for what runs on MySQL, and the
[Proxy 0.3.1 adoption note](verification/proxy-v0.3.1-adoption-2026-11-13.md)
for the measured runs.

## Protocol notes

- **WebSocket:** make sure the public TLS proxy supports upgrades.
- **SMTP:** install and activate `webong/dns` and `webong/email`, then configure
  `smtp.address` and the optional TLS files under the Email Extension resource.
  SMTP AUTH is disabled by default and requires TLS. When a deployed outbound
  adapter is enabled, retain persistent durable delivery storage.
- **POP3/IMAP retrieval:** configure the listeners, TLS certificate/key files,
  limits, and registered upstream maps under the `webong/email` Extension.
  Configure a Planner
  `GATEWAY_EMAIL_MAILBOX_AUTHENTICATOR`; without one, login is denied.
  Publish only the implicit-TLS listeners (normally ports 995 and 993) and keep the
  Spinner-to-Planner connection private. See [mail forwarding](mail-forwarding.md).
- **DNS:** install and activate `webong/dns` through Planner's `/extensions`
  API, setting `configuration.address` to the Extension process's published
  UDP/TCP address. Planner projects API-managed Zones and Forwarders into the
  versioned desired state; Spinner replaces only the verified Extension process
  when that snapshot changes. Publish both UDP and TCP. Each active zone requires
  parent-zone delegation. Gateway is
  authoritative-only on its public listener. Its separately bounded private
  resolver capability may serve dependent Extensions such as Email. See [DNS high availability](dns-ha.md)
  for the two-node deployment and validation shape.
- **Realtime:** planner state can reconcile Reverb, Mercure, and Centrifugo
  workloads via Local, Docker, or Kubernetes drivers. A separate HTTP Spinner
  can opt into private Planner-backed routing with
  `GATEWAY_PROVISIONING_ROUTE_PROXY_ENABLED=true`; see
  [deployment targets](provisioning-targets.md#route-managed-workloads-from-a-separate-spinner).
