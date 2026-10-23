# Gateway Shell extension

This optional extension adds the `shell` automation language for controlled
local and SSH-backed reply hooks. A local source is a relative hook key, not a command line: for
example, `github/deploy` resolves to an executable inside the configured hook
directory. Spinner executes the file directly through its shebang; it never
passes the source to `sh -c`.

The optional `powershell` interpreter mode instead invokes a configured
PowerShell executable with fixed `-NoLogo -NoProfile -NonInteractive -File`
arguments. This supports `.ps1` hooks on Unix and Windows without treating a
route source as a command line. Interpreter mode is selected for the whole
Spinner instance; the default remains `direct`.

Build Spinner with the extension and enable it at runtime:

```bash
make build GATEWAY_BUILD_TAGS=gateway_shell

export GATEWAY_SHELL_ENABLED=true
export GATEWAY_SHELL_HOOKS_DIR=/opt/gateway/hooks
export GATEWAY_SHELL_TIMEOUT=15s
export GATEWAY_SHELL_MAX_OUTPUT_BYTES=65536
export GATEWAY_SHELL_MAX_CONCURRENT=2
export GATEWAY_SHELL_MAX_QUEUED=0
export GATEWAY_SHELL_OUTPUT_MODE=outcome
# Optional: use .ps1 hooks instead of directly executable hooks.
# export GATEWAY_SHELL_INTERPRETER=powershell
# export GATEWAY_SHELL_POWERSHELL_PATH=/usr/bin/pwsh
# Optional private run history; create the directory with mode 0700 first.
export GATEWAY_SHELL_RUN_LOG_DIR=/var/lib/gateway/shell-runs
# Optional: retain run records for 30 days. Unset to never prune automatically.
# export GATEWAY_SHELL_RUN_RETENTION=720h
# Optional: allow the authenticated run-history API to return bounded stdout,
# stderr, and error text. Off by default because hooks may print secrets.
# export GATEWAY_SHELL_RUN_LOG_EXPOSE_OUTPUT=true
```

Install the Planner companion so a `shell` route plan is authorized only when
the Spinner advertises this engine:

```bash
composer require webong/gateway-shell
```

After authenticating and matching the request, the PHP Planner selects a hook:

```php
use Webong\Gateway\Protocol\Automation;
use Webong\Gateway\Protocol\RoutePlan;

return RoutePlan::automate(new Automation(language: 'shell', source: 'github/deploy'));
```

Alternatively, a registry-managed HTTP route can select a Shell hook with a
`reply` subscription. This uses the existing authenticated subscription
endpoint and its match rules, ETag updates, pause/resume, and removal lifecycle:

```http
POST /registry/endpoints/{endpointKey}/subscriptions
Authorization: Bearer {REGISTRY_TOKEN}
X-Gateway-Shell-Subscription: {SHELL_OPERATOR_TOKEN}
Content-Type: application/json

{"subscriber_id":"deploy-owner","type":"reply","target_kind":"shell","hook":"github/deploy","webhook_group":"github","routing_scope":"repository","routing_key":"my-repo"}
```

Only one active reply subscription, whether HTTP or Shell, may own a route.
The hook file must already be deployed to each eligible Spinner under
`GATEWAY_SHELL_HOOKS_DIR`; subscribing does not upload it. PHP still decides
whether the request matches, and Spinner returns the hook outcome to the
original HTTP caller. A Shell reply has no `url` or outbound proxy `profile`.
Because the registry token does not bind a caller to a subscriber identity,
Shell subscription creation and mutation also require the separate
`GATEWAY_SHELL_SUBSCRIPTION_TOKEN` on Planner, sent in the
`X-Gateway-Shell-Subscription` header. Keep this operator token out of
subscriber clients. Local hook subscriptions are denied by default. The
operator creates an exact Planner grant with `POST /shell/hook-grants` using
the resolved endpoint key, subscriber ID, and hook. Grants are typed,
versioned Planner resources rather than host configuration entries.
Removing a key from this policy also stops existing subscriptions from
executing it; pause or remove those subscriptions separately to free the
route's reply slot. The hook directory itself should remain operator-owned
and read-only to Spinner.
Shell subscription `PATCH`, status, match-patch, and `DELETE` requests require
both tokens; read-only `GET` uses the registry token alone.
PHP callers can use `SubscriptionDefinition::shellReply(...)` with
`SubscribeEndpoint::handle(...)` instead of the JSON endpoint.
See [the subscription contract](../../docs/subscription-dsl-v1.md) for updates
and match rules.

## SSH-backed replies

SSH is opt-in. Build the `gateway_shell` extension, set
`GATEWAY_SHELL_ENABLED=true`, and deploy a hard outbound ceiling such as
`GATEWAY_SHELL_SSH_ALLOWED_CIDRS=10.42.0.0/16`. This is a process safety
limit, not a route destination; Planner cannot widen it. A Spinner with only
SSH targets does not need `GATEWAY_SHELL_HOOKS_DIR`. Without a CIDR ceiling or
a private Planner resolver, SSH sources fail closed. In HTTP Planner mode,
the private credential connection must use HTTPS, loopback HTTP, or a Unix
socket. Plaintext HTTP to another host is refused when SSH is enabled.

On each remote host, install the separately built helper and an
operator-owned hook tree:

```bash
go -C pkg/shell build -o ../../gateway-shell-helper ./cmd/gateway-shell-helper
```

Configure the remote SSH account's `authorized_keys` entry with a forced
command pointing to that absolute helper path, and disable forwarding and
PTY:

```text
restrict,command="/usr/local/bin/gateway-shell-helper" ssh-ed25519 AAAA... gateway
```

Set the helper's `GATEWAY_SHELL_HOOKS_DIR`, timeout, output, and concurrency
ceilings in the remote account's environment. The helper accepts only the
fixed SSH original command `gateway-shell-helper`, reads bounded JSON stdin,
and invokes the selected relative hook using the local Shell engine. It does
not accept an executable path or shell command from the subscription. Set
`GATEWAY_SHELL_OUTPUT_MODE=outcome` on remote hosts. SSH replies do not stream
SSE or chunked output. Gateway does not retry an SSH attempt whose outcome
is unknown.

Before subscription creation, the operator creates a distinct SSH grant via
`POST /shell/hook-grants` with `mode: ssh`, `endpoint_key`, `subscriber_id`,
`hook`, `host`, `port` (default 22), `user`, and `host_public_key` (the pinned
OpenSSH host public key). A local-mode grant does not authorize SSH. Then the
authenticated subscription create request includes:

```json
{
  "subscriber_id": "deploy-owner", "subscription_id": "deploy-ssh",
  "type": "reply", "target_kind": "shell", "hook": "github/deploy",
  "webhook_group": "github", "routing_scope": "repository", "routing_key": "my-repo",
  "ssh": {
    "host": "worker.internal.example", "port": 22, "user": "gateway",
    "host_public_key": "ssh-ed25519 AAAA...",
    "private_key": "-----BEGIN OPENSSH PRIVATE KEY-----..."
  }
}
```

`private_key` is write-only. Planner stores it encrypted in the live generic
`configuration_secrets` collection, which is not copied into configuration
revision history; route plans contain only an opaque execution reference.
Public subscription reads return the host, port, user, pinned key, and
credential version, not the private key. Rotate only the client key with
`POST /registry/endpoints/{endpointKey}/subscriptions/{id}/ssh/rotate-key`,
body `{"private_key":"..."}`, both mutation tokens, and a current
subscription `If-Match` ETag. A stale ETag fails with 412. Host, user, pin,
and hook changes require a new subscription and matching grant. Removal
revokes the live credential. Do not send keys in metadata or URLs.

Planner can also select a completion-notification destination:

```php
use Webong\Gateway\Protocol\Delivery;

return RoutePlan::automate(
    new Automation(language: 'shell', source: 'github/deploy'),
    shellNotification: new Delivery(url: 'https://operator.example.test/shell-runs'),
);
```

Spinner queues a POST to that Planner-chosen destination after an admitted
hook finishes, with a small JSON body containing `event`, `run_id`,
`delivery_id`, `hook`, and `status` (`succeeded` or `failed`). It does not
include request data, stdout, stderr, or script-selected URLs. A generated
`Idempotency-Key` identifies the run; use a durable delivery queue when
notifications must survive a restart. A failed queue admission returns 503
for buffered requests; after streaming headers are sent it is logged on
Spinner instead. Capacity rejections before a run starts do not generate a
completion notification.

For PowerShell mode, select a `.ps1` hook instead, such as
`source: 'github/deploy.ps1'`.

In the default `direct` mode, each hook must be an executable regular file
under `GATEWAY_SHELL_HOOKS_DIR`. In `powershell` mode, the hook must be a
regular `.ps1` file (it need not have an executable bit), and
`GATEWAY_SHELL_POWERSHELL_PATH` must name an absolute, installed PowerShell 7
executable path. On Windows, use an absolute path to `pwsh.exe`. Gateway does
not install PowerShell or inherit the Spinner process's full environment.
Path traversal, hidden paths, and symlinks that resolve outside the directory
are rejected. Keep the hook tree owned by the operator and mounted read-only;
hooks are trusted host programs with the operating-system privileges of the
Spinner process. Gateway sends this JSON document to stdin:

```json
{"event":{"delivery_id":"..."},"variables":{"request":{}}}
```

By default, the hook writes one JSON Gateway automation outcome to stdout.
This example returns an immediate response:

```sh
#!/bin/sh
read input
printf '%s' '{"response":{"status":202,"body":"accepted"}}'
```

Set `GATEWAY_SHELL_OUTPUT_MODE=buffered` to return raw stdout as `text/plain`.
Exit code `0` maps to HTTP 200, `1` through `99` to 500, and `100` through
`255` to exit code plus 300. This mode cannot declare Gateway deliveries.
With buffered mode, clients may request live output with
`Accept: text/event-stream` or `X-Hook-Mode: chunked`. Streaming uses HTTP 200
and sends a generic error event or marker if the hook fails after headers have
been sent. Streaming hooks cannot declare Gateway deliveries, and the Planner
must not include relay destinations in their route plans.

`GATEWAY_SHELL_MAX_CONCURRENT` limits hook processes in one Spinner instance
(maximum 128). `GATEWAY_SHELL_MAX_QUEUED` sets a bounded waiting queue
(maximum 1024); zero rejects excess requests immediately with HTTP 503. A full
queue or an expired queue wait also returns 503. The timeout is capped at five
minutes, and each output stream at 16 MiB even if configuration requests more.
The timeout applies to
each run and terminates its process group on Unix. Both stdout and stderr have
the configured output cap. A configured run directory stores one mode-0600 JSON
file per invocation, named by `X-Gateway-Shell-Run-Id`; records include the
Gateway delivery ID, exit code, bounded output, and failure reason. The run
directory must have owner-only access. With `GATEWAY_SHELL_RUN_RETENTION` set
to a positive Go duration, Spinner removes expired regular run-record files
at startup and, while hooks continue to run, at most once per hour. It never
prunes unrelated files or symlinks, and leaving the setting unset preserves
existing records indefinitely. Idle Spinners do not run a background pruning
task; operators who need a strict expiry deadline should also schedule
private directory maintenance.
Run records store at most 64 KiB each of stdout and stderr; larger streams
are marked as truncated in the record.

To inspect a run, configure Spinner's private admin listener with
`GATEWAY_DELIVERY_ADMIN_ADDR` and `GATEWAY_INTERNAL_TOKEN`, and configure
Planner's matching `GATEWAY_DELIVERY_ADMIN_URL` (or its node list) and
`GATEWAY_INTERNAL_TOKEN`. The Shell companion adds
`GET /shell/runs/{run_id}` to Planner's registry-token-protected API. The
response contains run metadata and the owning node. Output and error text
are omitted unless `GATEWAY_SHELL_RUN_LOG_EXPOSE_OUTPUT=true` is explicitly
set on Spinner. The private Spinner endpoint is never mounted on the public
Gateway listener. Missing runs return 404 only after every configured node
has answered; unreachable nodes return 503 instead.

This extension supports route hooks only. It does not expose arbitrary request
fields as environment variables, run shell command strings, create schedules,
or map public URL paths directly to files. Planner remains responsible for
authentication and selecting the hook key.
