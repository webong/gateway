# Executable Go workload extensions

Spinner and the standalone provisioning worker load server workloads from
configured executable files. They do not import the workload's Go package.
Planner still discovers the separate PHP package that validates management
requests and stores the server configuration.

## Install and enable

Build the bundled workload binaries from this checkout:

```sh
scripts/build-gateway-extensions
```

The script writes `dist/gateway-extensions/gateway-{centrifugo,mercure,reverb}`.
Copy only the executables you want onto the worker host, grant the worker user
execute permission, and configure their absolute paths:

```sh
GATEWAY_PROVISIONING_ENABLED=true
GATEWAY_EXTENSION_PATHS=/opt/gateway/extensions/gateway-centrifugo
GATEWAY_CENTRIFUGO_DOCKER_IMAGE=centrifugo/centrifugo:v6
```

Separate multiple paths using the OS path-list separator (`:` on Linux/macOS).
Restart the worker after changing paths or replacing a binary. Enabling a
server workload with `GATEWAY_*_ENABLED` alone no longer loads Go code. A
bundled Router's in-process PHP adapter is a different extension category.

The selected extension binaries run as the worker's OS user. Install trusted
binaries and keep their executable directory writable only by trusted
operators. Their stdout is reserved for protocol messages; send logs to stderr.
Spinner closes them when provisioning stops. An extension that hangs on a call
is terminated after 15 seconds; the reconciler reports failed work until the
worker is restarted. Extensions receive server configuration, including
workload secrets needed to construct launch settings.

## Third-party Go module

Authors depend on `github.com/webong/gateway/driver`, not the Spinner module.
Implement `driver.Workload`, then serve it from a tiny `main` package:

```go
package main

import (
    "log"
    "os"

    "github.com/webong/gateway/driver/workloadplugin"
    "example.com/your-extension/workload"
)

func main() {
    if err := workloadplugin.Serve(workload.New(), os.Stdin, os.Stdout); err != nil {
        log.Fatal(err)
    }
}
```

Build a platform-specific executable with `go build -o your-extension
./cmd/plugin`. The [isolated example](../lib/driver/testdata/extension) is
compiled against a synthetic published `driver/v0.1.0` tag by
`scripts/test-driver-release`, with no Spinner dependency or local `replace`.

The host starts an explicitly configured executable and exchanges newline
delimited JSON over stdin/stdout. Protocol version 1 supports `describe`,
`validate`, and `launch`; `describe` advertises the workload type and supported
local/Docker/Kubernetes drivers. Requests and responses carry matching IDs and
have an 8 MiB limit. The extension returns a `driver.LaunchSpec`; Gateway core
still owns process/container/Pod creation, reconciliation, and routing.

The protocol version is checked at startup. Installation from a package
registry, version pinning and verified download tooling are future work; today
binary placement and `GATEWAY_EXTENSION_PATHS` are explicit operator steps.
