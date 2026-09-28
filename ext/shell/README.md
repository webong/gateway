# Gateway Shell extension

This optional extension adds the `shell` automation language for a controlled
local hook flow. A source is a relative hook key, not a command line: for
example, `github/deploy` resolves to an executable inside the configured hook
directory. Spinner executes the file directly through its shebang; it never
passes the source to `sh -c`.

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
# Optional private run history; create the directory with mode 0700 first.
export GATEWAY_SHELL_RUN_LOG_DIR=/var/lib/gateway/shell-runs
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

Each hook must be an executable regular file under `GATEWAY_SHELL_HOOKS_DIR`.
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
directory must have owner-only access and needs an operator retention policy.
It is not published through the public Gateway listener.

This extension supports route hooks only. It does not expose arbitrary request
fields as environment variables, run shell command strings, create schedules,
or map public URL paths directly to files. Planner remains responsible for
authentication and selecting the hook key.
