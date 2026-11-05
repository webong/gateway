# Gateway Planner

`webong/gateway` is one Composer package with a shared Planner implementation
and separate Laravel and Symfony integrations. Public planning contracts and
protocol values remain in `webong/gateway-adapter` for custom planners.

```text
pkg/planner/
  composer.json
  config/
    gateway.php                 Laravel configuration
    symfony-routes.php          Symfony planning routes
  migrations/                  Shared Phinx schema; gateway_migrations ledger
  src/
    Planning/                  Authentication, decoding, planner registries
    Automation/                Capability checks and language registry
    Companion/                 Portable package/process support
    Migrations/                Shared migration runner
    Laravel/                   Provider, Eloquent storage, management, scheduling
      Http/
      Console/
      database/migrations/     Legacy Laravel schema, for adoption/parity tests
    Symfony/
      GatewayBundle.php
      DependencyInjection/
      Http/
      Console/
```

Shared code accepts explicit dependencies and ordinary PHP values. It must not
import either integration, Laravel helpers, Eloquent, or framework HTTP/container
objects. Standalone Symfony Process and Console utilities remain usable libraries:
the process runner uses Process, and Phinx accepts Console input/output objects.
Architecture tests enforce the dependency direction.

## Laravel

Install `webong/gateway`, `laravel/framework:^12.0`, and `webong/proxy:^0.2.0`.
Composer discovers `Webong\Gateway\Laravel\GatewayServiceProvider`. Existing
Laravel management APIs, registry policy, extension lifecycle, and scheduling
remain available through this integration. Run `php artisan gateway:migrate`
to apply the shared schema. Existing deployments use `--adopt-existing` only
after their existing schema is current.

Laravel classes now use the `Webong\Gateway\Laravel\` namespace. Update imports
in host applications and extensions, regenerate Composer autoload metadata,
and clear/rebuild Laravel's package, service, configuration, and Proxy discovery caches when
upgrading. Shared contracts retain their existing namespaces.

Octane and Workerman are host runtime choices in `app/`; they do not change
which code belongs in the shared Planner.

## Symfony

Install `webong/gateway` and `symfony/framework-bundle`. Register
`Webong\Gateway\Symfony\GatewayBundle` in `config/bundles.php` and import
`@GatewayBundle/../../config/symfony-routes.php` from your routes configuration
(or use the installed package's `config/symfony-routes.php` absolute path).

Bind `Webong\Gateway\Contracts\RoutePlanner` and
`Webong\Gateway\Contracts\ProtocolPlanner` to your application's implementations.
If protocol events should use HTTP route planning, bind `ProtocolPlanner` to the
shared `Webong\Gateway\RouteProtocolPlanner` and inject your `RoutePlanner`.

```yaml
# config/packages/gateway.yaml
gateway:
  internal_token: '%env(GATEWAY_INTERNAL_TOKEN)%'
  max_body_bytes: 1048576
  connection:
    driver: sqlite
    database: '%kernel.project_dir%/var/gateway.sqlite'
```

The bundle exposes `POST /_internal/gateway/plan` and
`POST /_internal/gateway/event`, authenticated with `X-Gateway-Internal`.
An empty configured token disables access. Invalid input returns 422 and
oversized bodies return 413. Application planner exceptions remain server errors.

Providing `connection` registers `php bin/console gateway:migrate`. SQLite,
PostgreSQL (`pgsql`), and MySQL/MariaDB are supported by the shared runner.
Omit `connection` if this application does not own schema management.

Symfony currently provides planning and shared migrations with application-owned
policy. The Laravel registry, management APIs, scheduler, and extension providers
are not automatically available in Symfony.

## Development and verification

The repository root uses `webong/composer-source-plugin` workspaces for
`mod/adapter`, `mod/builder`, and `pkg/*`. Both integrations stay in this source
directory and this package; there are no framework-specific split packages.
Set `COMPOSER_ROOT_VERSION=dev-main` when resolving workspace dependencies on a
feature branch.

Run `vendor/bin/pest --compact` for regression, boundary, HTTP, and migration
tests. `bash scripts/test-planner-package` installs a fresh Symfony consumer and
exercises the real kernel and migration command without Laravel or Proxy.
CI also supplies disposable PostgreSQL and MySQL databases for migration tests.
