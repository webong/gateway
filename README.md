# Gateway WebhookScript extension

This paired extension adds Webhook.site-style WebhookScript syntax to Gateway
through [Primi](https://github.com/smuuf/primi) and a bounded Gateway host
interface. Its Go guest runs in Spinner and invokes the trusted
`gateway.hookscript.execute` PHP companion enabled in Planner. The Go module
does not embed FrankenPHP, load PHP, receive a Planner credential, or accept
arbitrary PHP source.

## Static installation

Once the package is published or configured as a Composer path repository:

```bash
composer require webong/gateway-hookscript
```

Laravel package discovery registers
`Webong\Gateway\HookScript\HookScriptExtensionServiceProvider`. The provider
registers the language as Spinner-executed and offers the matching static PHP
companion action. Enable the compiled Go guest with
`GATEWAY_AUTOMATION_ENGINES=hookscript`. The engine advertises the public
language ID `webhookscript`. Route and scheduled executions cross
the declared plugin reverse-call interface; credential-source execution stays
inside Planner to avoid a Planner-to-Spinner-to-Planner callback.

The PHP runtime requires `pcntl` and `mbstring`. Its defaults can be
published with:

```bash
php artisan vendor:publish --tag=gateway-webhookscript-config
```

## Dynamic installation

The same PHP code can run as a self-contained ext process without changing
Planner's root Composer installation. Build the release artifact after a
package-local production Composer install:

```bash
composer install --no-dev --classmap-authoritative
php scripts/build-ext-package /tmp/webong-hookscript 1.0.0
```

Install that directory into the ext store mounted at
`GATEWAY_COMPANION_PLUGIN_DIR`. The package declares a `planner` entrypoint
using `php-jsonline` and serves `ext.plugin/v1` from `bin/companion`. When
Planner runs under PHP-FPM, set `GATEWAY_COMPANION_PHP_BINARY` to the PHP CLI
executable used to launch that entrypoint.

Enable it through Planner CMS:

```http
POST /extensions
Authorization: Bearer <registry token>
Content-Type: application/json

{
  "extension": "webong/hookscript",
  "enabled": true,
  "mode": "dynamic",
  "version": "1.0.0"
}
```

Static and dynamic candidates may both be discovered. The activation resource
selects exactly one, and `GET /extensions` reports its mode, generation,
readiness and discovered candidates. Selecting dynamic mode fails closed if
the package is absent or its version/digest does not match.

## Host capabilities

The extension exposes a deliberately bounded host interface:

- `var(path, default)` and `set(path, value)` read and update execution variables.
- `respond(content, status, headers)` responds and stops the script.
- `set_response`, `set_content`, `set_status`, and `set_header` build a response.
- `forward(url, content, method, headers)` adds an HTTP delivery for Go to send.
- `schedule(definition)` creates or updates a Planner-owned schedule for an
  installed Go-side language. A stable `key` makes retries idempotent.
- `json_encode`, `json_decode`, Base64, `hash`, and `hmac` transform values.
- `echo`, `dump`, and `stop` provide bounded debugging and flow control.

Scripts receive flat Webhook.site-style variables such as `request.method`,
`request.host`, `request.path`, `request.url`, `request.query`,
`request.query.<name>`, `request.header.<name>`, and `request.content`. They can
be read with `var('request.content')` or through
`variables['request.content']`.

The interpreter has no filesystem, process, package-import, database, or raw
network capability. `forward()` only produces a delivery plan; Go still owns
egress and applies Gateway's outbound policy. Removing either half fails
closed: Planner advertises only an enabled and compatible provider, and the Go
guest cannot execute without the exact companion action/version.

## Limits

The extension defaults to a 64 KiB source limit, a one-second PCNTL deadline,
32 deliveries, 8 schedule commands, and 100 log entries per execution. Configure them with
`GATEWAY_WEBHOOKSCRIPT_MAX_SOURCE_BYTES`,
`GATEWAY_WEBHOOKSCRIPT_TIMEOUT_SECONDS`,
`GATEWAY_WEBHOOKSCRIPT_MAX_DELIVERIES`, and
`GATEWAY_WEBHOOKSCRIPT_MAX_SCHEDULES`, and
`GATEWAY_WEBHOOKSCRIPT_MAX_LOGS`.

Published Laravel configuration applies to static mode. The dynamic release
artifact currently uses those same documented defaults; future per-instance
dynamic configuration belongs in the ext configuration contract rather than
ambient process environment.
