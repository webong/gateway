# Executable Go workload extensions

Spinner and the standalone provisioning worker load server workloads from
configured executable files. They do not import the workload's Go package.
Planner still discovers the separate PHP package that validates management
requests and stores the server configuration.

## Install and enable

Build the manager and bundled workload binaries from this checkout. Pass an
exact version for a release; the no-argument build uses `v0.0.0-local`:

```sh
make build-extension-manager
scripts/build-gateway-extensions dist/gateway-extensions v1.2.3
bin/gateway-extension list --catalog "$PWD/dist/gateway-extensions/catalog.json"
```

The build writes `gateway-{centrifugo,mercure,reverb}` plus `catalog.json`.
The catalog records each executable's exact version, OS/architecture, relative
filename, and SHA-256 digest. To install from that trusted local bundle:

```sh
bin/gateway-extension install \
  --catalog "$PWD/dist/gateway-extensions/catalog.json" \
  --name centrifugo --version v1.2.3 --dir /opt/gateway/extensions
bin/gateway-extension verify \
  --path /opt/gateway/extensions/centrifugo/v1.2.3/gateway-centrifugo
```

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
scripts/build-gateway-extension-release dist/gateway-extension-release v1.2.3
bin/gateway-extension list --catalog "$PWD/dist/gateway-extension-release/catalog.json"
```

This cross-builds the three bundled extensions for Linux and macOS on amd64
and arm64, with binaries under `OS/architecture/gateway-name`. One catalog
contains all twelve entries; `install` selects only the current host's entry.
The builder requires CGO-free extension code and refuses to replace an existing
release directory. Test each target on its actual OS before publishing: a
successful cross-compile and matching checksum do not prove runtime behavior.
Publish the entire directory unchanged under HTTPS if using a private release
site. A Gateway-hosted registry and unattended updates are still separate work.

On a macOS host with Rosetta, Docker/binfmt, Python 3, and an already-local
Linux test image, run the release protocol check before publishing:

```sh
scripts/test-gateway-extension-release \
  dist/gateway-extension-release v1.2.3 python:3.12-slim-bookworm
```

It verifies all twelve catalog paths and hashes, then launches each binary to
check its `describe` response. Linux probes run in network-isolated, read-only
containers without pulling an image. Set
`GATEWAY_EXTENSION_TEST_PUBLIC_KEY=/secure/gateway-extension-publisher.pub`
to require signature verification too. This is a protocol smoke test, not a
test of launching actual Reverb, Mercure, or Centrifugo workloads on each OS.

To authenticate a release independently of its HTTPS host, create an Ed25519
publisher key pair once and keep the private key outside the bundle and source
repository. Distribute the public key to operators through a trusted channel:

```sh
bin/gateway-extension keygen \
  --private-key /secure/gateway-extension-publisher.key \
  --public-key /secure/gateway-extension-publisher.pub
bin/gateway-extension sign \
  --catalog "$PWD/dist/gateway-extension-release/catalog.json" \
  --private-key /secure/gateway-extension-publisher.key
bin/gateway-extension list \
  --catalog "$PWD/dist/gateway-extension-release/catalog.json" \
  --public-key /secure/gateway-extension-publisher.pub
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
GATEWAY_EXTENSION_PATHS=/opt/gateway/extensions/centrifugo/v1.2.3/gateway-centrifugo
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

The protocol version is checked at startup. The manager handles catalog-based,
version-pinned, hash-verified installation, while activation remains an
explicit `GATEWAY_EXTENSION_PATHS` operator step. A hosted official registry,
signing-key distribution, full workload validation on each target and publishing,
and unattended updates remain future work.
