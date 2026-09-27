# Gateway WebhookScript extension

This optional package adds Webhook.site-style WebhookScript syntax to Gateway
through [Primi](https://github.com/smuuf/primi) and a bounded Gateway host
interface. Gateway core does not install Primi or execute WebhookScript.

## Install

Once the package is published or configured as a Composer path repository:

```bash
composer require webong/gateway-webhookscript
```

Laravel package discovery registers
`Webong\Gateway\WebhookScript\WebhookScriptExtensionServiceProvider`. The
provider registers its route capability through Gateway's
`AutomationLanguageRegistry` contract and decorates the configured
`RoutePlanner`: Go-runtime language plans continue to Go, while
`webhookscript` plans run in PHP and are converted to ordinary response and
relay plans before they cross the Go bridge. It deliberately does not register
scheduled execution support.

The PHP runtime requires `pcntl` and `mbstring`. Its defaults can be
published with:

```bash
php artisan vendor:publish --tag=gateway-webhookscript-config
```

## Host capabilities

The extension exposes a deliberately bounded host interface:

- `var(path, default)` and `set(path, value)` read and update execution variables.
- `respond(content, status, headers)` responds and stops the script.
- `set_response`, `set_content`, `set_status`, and `set_header` build a response.
- `forward(url, content, method, headers)` adds an HTTP delivery for Go to send.
- `json_encode`, `json_decode`, Base64, `hash`, and `hmac` transform values.
- `echo`, `dump`, and `stop` provide bounded debugging and flow control.

Scripts receive flat Webhook.site-style variables such as `request.method`,
`request.host`, `request.path`, `request.url`, `request.query`,
`request.query.<name>`, `request.header.<name>`, and `request.content`. They can
be read with `var('request.content')` or through
`variables['request.content']`.

The interpreter has no filesystem, process, package-import, database, or raw
network capability. `forward()` only produces a delivery plan; Go still owns
egress and applies Gateway's outbound policy.

## Limits

The extension defaults to a 64 KiB source limit, a one-second PCNTL deadline,
32 deliveries, and 100 log entries per execution. Configure them with
`GATEWAY_WEBHOOKSCRIPT_MAX_SOURCE_BYTES`,
`GATEWAY_WEBHOOKSCRIPT_TIMEOUT_SECONDS`,
`GATEWAY_WEBHOOKSCRIPT_MAX_DELIVERIES`, and
`GATEWAY_WEBHOOKSCRIPT_MAX_LOGS`.
