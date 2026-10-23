# webhookd patterns relevant to Gateway

This is a design comparison, not an integration proposal. It is based only on
webhookd's public repository and source tree, pinned to
[`5ac6667`](https://github.com/ncarlier/webhookd/tree/5ac66677bf7224191f1dfc51a875b09187d10beb)
(reviewed 2026-09-26).

## What webhookd optimizes for

webhookd is a small Go HTTP server whose URL path selects an executable from a
scripts directory; it runs that executable and turns its output into an HTTP
response. The mapping deliberately makes a checked-out scripts tree the hook
registry, and accepts any executable with an appropriate shebang. [The resolver
keeps the selected executable inside that root](https://github.com/ncarlier/webhookd/blob/5ac66677bf7224191f1dfc51a875b09187d10beb/pkg/hook/helper.go#L10-L32),
while [the README documents the filesystem-to-URL model](https://github.com/ncarlier/webhookd/blob/5ac66677bf7224191f1dfc51a875b09187d10beb/README.md#L52-L68).

That is a useful deployment-automation primitive, but a different route
boundary from Gateway. Gateway's public path is application-owned, while PHP
owns authentication, registry lookup, matching, and route planning; the Go
edge receives a `RoutePlan` and executes it. See [Gateway's responsibility
split](../README.md#responsibilities). Gateway's optional `shell` extension
can execute a Planner-selected hook from a configured directory without
turning every public URL into a filesystem lookup.

## Patterns worth carrying forward

| webhookd pattern | Evidence | Gateway interpretation |
| --- | --- | --- |
| **An explicit invocation/response mode.** It supports SSE, chunked output, and buffered completion; buffered mode maps process exit status to HTTP status. | [README: response modes and exit mapping](https://github.com/ncarlier/webhookd/blob/5ac66677bf7224191f1dfc51a875b09187d10beb/README.md#L69-L100) | `shell` supports structured JSON outcomes, opt-in buffered stdout, and request-selected SSE or chunked output after Planner selects a hook. Streaming errors are reported in the stream because HTTP headers have already been sent. |
| **A bounded execution system, decoupled from HTTP.** Jobs enter a queue of 100 and a configured fixed worker pool. | [Dispatcher source](https://github.com/ncarlier/webhookd/blob/5ac66677bf7224191f1dfc51a875b09187d10beb/pkg/worker/dispatcher.go#L7-L35) | `shell` caps active processes and optionally queues a bounded number of requests per Spinner. Full queues return 503. It does not have per-route limits. |
| **Correlatable execution output.** Each execution has an ID in `X-Hook-Id`, and historical output is addressable by hook name and ID. | [Job identity](https://github.com/ncarlier/webhookd/blob/5ac66677bf7224191f1dfc51a875b09187d10beb/pkg/hook/job.go#L22-L48) and [HTTP lookup](https://github.com/ncarlier/webhookd/blob/5ac66677bf7224191f1dfc51a875b09187d10beb/pkg/api/index.go#L252-L280) | `shell` emits `X-Gateway-Shell-Run-Id` for completed responses and streams, stores private run files, supports opt-in retention, and exposes an authenticated Planner lookup. Output remains redacted by default. |
| **Deliberate upstream-identity handling.** It supports basic auth, a configured allow-list of upstream headers, and signature verification using a truststore. | [Route middleware](https://github.com/ncarlier/webhookd/blob/5ac66677bf7224191f1dfc51a875b09187d10beb/pkg/api/routes.go#L11-L67) and [signature selection](https://github.com/ncarlier/webhookd/blob/5ac66677bf7224191f1dfc51a875b09187d10beb/pkg/middleware/signature.go#L10-L25) | This reinforces Gateway's PHP-owned authentication and validation. Add provider verification as explicit Planner policy where needed, and pass only normalized, trusted identity to automations. Do not copy webhookd's optional wildcard header forwarding into a script context. |

## Patterns to avoid importing

- webhookd projects query parameters, headers, and form fields into script
  variables and passes JSON/text bodies as a positional argument. [Input
  parsing and header allow-list](https://github.com/ncarlier/webhookd/blob/5ac66677bf7224191f1dfc51a875b09187d10beb/pkg/api/index.go#L101-L149)
  Gateway should retain its immutable `gateway.event` document and restricted
  capability API; implicit variable injection makes name collisions and
  unreviewable data flow too easy.
- webhookd can send selected script-output lines to an email or HTTP
  notification URI. [The HTTP notifier filters `notify:` output into a
  completion payload](https://github.com/ncarlier/webhookd/blob/5ac66677bf7224191f1dfc51a875b09187d10beb/pkg/notification/http/http_notifier.go#L16-L74)
  Gateway does not treat automation output as a notification payload: output
  can contain request data, secrets, or unbounded text. A Planner-selected
  shell completion destination instead receives bounded run metadata.

## Practical conclusion

Gateway now has the core hook flow in an optional `shell` extension with
bounded execution and run IDs. It intentionally retains PHP-selected routes
for this release. It does not have webhookd's direct URL-to-file routing,
public log lookup or output-driven notifications. Its run-history lookup is
authenticated and redacts output by default; its completion notifications
are structured and Planner-selected.
See [the shell extension guide](../pkg/shell/README.md) for the current
contract and deployment settings.
