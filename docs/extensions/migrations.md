# Extension schema migrations

Installed PHP extensions own their schema and declare migration directories.
Planner coordinates those contributions; Router startup invokes only:

```sh
php app/planner/artisan gateway:migrate --force
```

`--database=name` selects the Laravel connection, including its resolved URL,
write PDO, TLS/session settings and table mapping. The command stops on failure;
the launchers do not start the host after a failed migration.

## Neutral extensions

Register a Phinx directory through the shared `MigrationRegistry`:

```php
use Webong\Gateway\Migrations\MigrationRegistry;

$this->app->afterResolving(MigrationRegistry::class, static function (MigrationRegistry $migrations): void {
    $migrations->register('acme/extension', __DIR__.'/../migrations');
});
```

In Symfony, add a method call to the public `MigrationRegistry` service from the
extension's container configuration, or register on that service before running
`gateway:migrate`. The runner reads the live registry, not a snapshot captured
when its service was constructed.

Core and registered neutral migrations use Phinx's existing `gateway_migrations`
history. Their numeric migration versions/class names must be globally unique.
The registry owner identifies a contribution; it does not rewrite versions,
namespace schema tables, or create another ledger. Repeated identical registration
is a no-op; conflicting owners/paths fail. Existing single-path runner APIs remain
supported. Use the registry when coordinating core plus extension directories.

## Laravel extensions and adapters

Existing `loadMigrationsFrom()` declarations are discovered automatically and
remain in Laravel's normal `migrations` history. An extension can also declare:

```php
$migrations->registerLaravel('acme/extension', __DIR__.'/../database/migrations');
```

An adapter required before core schema creation may opt into `beforeCore: true`.
The Laravel command runs prerequisites, core/neutral migrations, then application
and remaining registered Laravel migrations. It excludes prerequisite directories
from the last phase. Native filename/history semantics prevent replay and preserve
published application overrides. Do not submit Laravel migration directories to a
neutral Symfony host: it fails clearly before migrating rather than ignoring them.

Proxy's integration bridge reads the enabled adapter provider's existing
`proxy-migrations` publish declaration and registers it as a prerequisite. It has
no dependency on Web, no vendor-directory assumptions and does not opt into the
separate `proxy-legacy-migrations` group. Legacy adoption remains an explicit
operator action. Proxy continues to own its schema, models and data. New MySQL core schemas match
the adapter-owned endpoint ID collation only on the referencing event column;
Gateway keeps its exact-comparison collation for its own keys.

## Existing databases and adoption

`--adopt-existing` validates the known core schema and records only the core's
Phinx versions without replaying that schema. Newly registered extension schemas
still execute normally; they are not silently marked applied. Laravel migrations
retain their existing history and execute only when pending.

Back up data and application keys and serialize deployment migration work before
starting writers. Migration engines keep their native transaction/DDL behavior;
this coordinator does not promise an atomic rollback across engines. A failure
leaves native history available for diagnosis and a later retry. Do not reset
migration ledgers, rename historical migrations, or drop registry data during a
package upgrade or rollback.

Schema migrations are separate from runtime database effects. Registration does
not add a general SQL execution capability or run schema changes on HTTP requests.
