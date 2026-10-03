# Gateway Shell extension

This optional extension adds the `shell` automation language for a controlled
local hook flow. A source is a relative hook key, not a command line: for
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
It is not published through the public Gateway listener.

This extension supports route hooks only. It does not expose arbitrary request
fields as environment variables, run shell command strings, create schedules,
or map public URL paths directly to files. Planner remains responsible for
authentication and selecting the hook key.
