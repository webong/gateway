# Executable Go workload integrations

Spinner and the standalone provisioning worker load server workloads from
configured executable files. They do not import the workload's Go package.
Planner still discovers the separate PHP package that validates management
requests and stores the server configuration.

## Install and enable

Build the manager and bundled workload binaries from this checkout. Pass an
exact version for a release; the no-argument build uses `v0.0.0-local`:

```sh
make build-integration-manager
scripts/build-gateway-integrations dist/gateway-integrations v1.2.3
bin/gateway-integration list --catalog "$PWD/dist/gateway-integrations/catalog.json"
```

The build writes `gateway-{centrifugo,mercure,reverb}` plus `catalog.json`.
The catalog records each executable's exact version, OS/architecture, relative
filename, and SHA-256 digest. To install from that trusted local bundle:

```sh
bin/gateway-integration install \
  --catalog "$PWD/dist/gateway-integrations/catalog.json" \
  --name centrifugo --version v1.2.3 --dir /opt/gateway/integrations
bin/gateway-integration verify \
  --path /opt/gateway/integrations/centrifugo/v1.2.3/gateway-centrifugo
bin/gateway-integration probe \
  --path /opt/gateway/integrations/centrifugo/v1.2.3/gateway-centrifugo \
  --spec /secure/gateway/centrifugo-server.json --driver kubernetes
```

`probe` starts the selected integration on the current host, sends `describe`,
validates one complete `integration.ServerSpec` JSON file, and checks the launch
specification for the selected driver. For `local`, pass the intended `--host`
and `--port`; Docker and Kubernetes probes derive the container port from the
integration. The command prints no launch environment because it may contain
credentials. Keep the specification file private. This checks the integration
protocol and configuration on this host; it does not create a Pod/container,
verify target credentials, or prove that the workload image starts. Run the
target connection check and a disposable real-workload smoke before production
activation.

The installer refuses an existing version, a mismatched digest, an oversized
artifact, or a wrong OS/architecture. It writes a local receipt and returns
the versioned executable path only after a complete install. The same catalog
and binaries can be hosted unchanged on a trusted HTTPS site; pass the HTTPS
`catalog.json` URL to `list` and `install`. HTTP and HTTPS-to-HTTP redirects
are refused. Keep the catalog and release assets immutable: HTTPS protects
transport, but the catalog publisher is still the authority for its hashes.
There is no Gateway-hosted public catalog or automatic update channel yet.

For a multi-platform release bundle, provide an exact version:

```sh
scripts/build-gateway-integration-release dist/gateway-integration-release v1.2.3
bin/gateway-integration list --catalog "$PWD/dist/gateway-integration-release/catalog.json"
```

This cross-builds the three bundled integrations for Linux and macOS on amd64
and arm64, with binaries under `OS/architecture/gateway-name`. One catalog
contains all twelve entries; `install` selects only the current host's entry.
The builder requires CGO-free integration code and refuses to replace an existing
release directory. Test each target on its actual OS before publishing: a
successful cross-compile and matching checksum do not prove runtime behavior.
Publish the entire directory unchanged under HTTPS if using a private release
site. A Gateway-hosted registry and unattended updates are still separate work.

On a macOS host with Rosetta, Docker/binfmt, Python 3, and an already-local
Linux test image, run the release protocol check before publishing:

```sh
scripts/test-gateway-integration-release \
  dist/gateway-integration-release v1.2.3 python:3.12-slim-bookworm
```

It verifies all twelve catalog paths and hashes, then launches each binary to
check its `describe` response. Linux probes run in network-isolated, read-only
containers without pulling an image. Set
`GATEWAY_INTEGRATION_TEST_PUBLIC_KEY=/secure/gateway-integration-publisher.pub`
to require signature verification too. This is a protocol smoke test, not a
test of launching actual Reverb, Mercure, or Centrifugo workloads on each OS.

To authenticate a release independently of its HTTPS host, create an Ed25519
publisher key pair once and keep the private key outside the bundle and source
repository. Distribute the public key to operators through a trusted channel:

```sh
bin/gateway-integration keygen \
  --private-key /secure/gateway-integration-publisher.key \
  --public-key /secure/gateway-integration-publisher.pub
bin/gateway-integration sign \
  --catalog "$PWD/dist/gateway-integration-release/catalog.json" \
  --private-key /secure/gateway-integration-publisher.key
bin/gateway-integration list \
  --catalog "$PWD/dist/gateway-integration-release/catalog.json" \
  --public-key /secure/gateway-integration-publisher.pub
```

Publish `catalog.json.sig` beside `catalog.json`. The signature covers the
exact catalog bytes; artifact SHA-256 values in that catalog then protect the
downloaded binaries. Pass the same `--public-key` to `install` to require
signature verification before installation. Missing, malformed, wrong-key, or
modified signatures fail closed. Back up and protect the private key; do not
serve it. Leaving off `--public-key` retains the original trusted-local or
HTTPS-publisher mode and does **not** authenticate the catalog against a pinned
signer. Key rotation and an official publisher trust policy remain operational
work.

Configure only installed absolute paths on the worker:

```sh
GATEWAY_PROVISIONING_ENABLED=true
GATEWAY_INTEGRATION_PATHS=/opt/gateway/integrations/centrifugo/v1.2.3/gateway-centrifugo
GATEWAY_CENTRIFUGO_DOCKER_IMAGE=centrifugo/centrifugo:v6
```

Separate multiple paths using the OS path-list separator (`:` on Linux/macOS).
Restart the worker after changing paths or replacing a binary. Enabling a
server workload with `GATEWAY_*_ENABLED` alone no longer loads Go code. A
bundled Router's in-process PHP adapter is a different integration category.

The selected integration binaries run as the worker's OS user. Install trusted
binaries and keep their executable directory writable only by trusted
operators. Their stdout is reserved for protocol messages; send logs to stderr.
Spinner closes them when provisioning stops. An integration that hangs on a call
is terminated after 15 seconds; the reconciler reports failed work until the
worker is restarted. Integrations receive server configuration, including
workload secrets needed to construct launch settings.

## Third-party Go module

Authors depend on `github.com/webong/gateway/arranger/integration`, not the
Spinner module. Implement `integration.Provisioner`, then serve it from a tiny
`main` package:

```go
package main

import (
    "log"
    "os"

    "github.com/webong/gateway/arranger/integration"
    "example.com/your-integration/workload"
)

func main() {
    if err := integration.ServeProvisioner(workload.New(), os.Stdin, os.Stdout); err != nil {
        log.Fatal(err)
    }
}
```

Build a platform-specific executable with `go build -o your-integration
./cmd/integration`. The [isolated example](../mod/arranger/testdata/integration) is
compiled against a synthetic published `arranger/v0.1.0` tag by
`scripts/test-arranger-release`, with no Spinner dependency or local `replace`.

The host starts an explicitly configured executable and exchanges newline
delimited JSON over stdin/stdout. Protocol version 1 supports `describe`,
`validate`, and `launch`; `describe` advertises the workload type and supported
local/Docker/Kubernetes drivers. Requests and responses carry matching IDs and
have an 8 MiB limit. The integration returns an `integration.LaunchSpec`; Gateway
core still owns process/container/Pod creation, reconciliation, and routing.

The protocol version is checked at startup. The manager handles catalog-based,
version-pinned, hash-verified installation and a local specification probe,
while activation remains an explicit `GATEWAY_INTEGRATION_PATHS` operator step.
A hosted official registry, signing-key distribution, real workload validation
on each target and publishing, and unattended updates remain future work.
