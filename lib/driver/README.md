# Gateway driver

Module: `github.com/webong/gateway/driver`

This module defines the shared contracts between Gateway hosts and workload
extensions. It depends only on the Go standard library. Building an extension
against it does not install Spinner, PHP, Caddy, or a JavaScript runtime.

```go
import "github.com/webong/gateway/driver"

var _ driver.Workload = (*MyWorkload)(nil)
```

`Workload` describes a technology: its name, supported runtimes, configuration
validation, and launch specification. `Launch` describes what to run; the host
executes it. `Driver` starts that workload on a runtime and returns a `Process`
handle. `ServerSpec` and `InstanceReport` carry assignments and status.

Spinner owns reconciliation, configuration loading, routing, and the Local,
Docker, and Kubernetes driver implementations. Concrete extensions own their
technology-specific configuration. Neither belongs in this contracts module.
The existing `provision` type names are aliases to these contracts, preserving
type identity for host integrations during migration.

## Develop and test

The repository keeps development sources in `lib/driver`. Its consuming modules
use explicit local `replace` directives, including the optional FrankenPHP
module (Go does not inherit replacements from dependencies). This works with
the existing standalone builds without requiring a workspace or native PHP
toolchain for ordinary driver development.

```sh
go -C lib/driver test ./...
make test-driver-release
```

The release test creates an isolated Git repository and release tag. An example
third-party extension then downloads that tag using Go's normal GitHub module
resolution, redirected to the local fixture. It uses no `replace` directive,
no network, no CGO, and no Spinner source. Its complete module graph is just
the extension and this library. The example lives in `testdata/extension`.

## Publish

The public module path omits `lib/`. Go therefore expects the module in
`driver/` at a release tag named `driver/vX.Y.Z`. Tagging the ordinary source
tree directly would publish the wrong layout.

After committing and validating the library, from the repository root run:

```sh
bash scripts/release-driver v0.1.0
git push upstream refs/tags/driver/v0.1.0
```

The first command creates a separate release commit containing `driver/`, with
the current source commit as its parent, and an annotated local tag. It leaves
the current branch, checkout, and index unchanged and never pushes. It refuses
dirty library source, existing tags, and major versions requiring a `/v2` path.
Do not merge the release commit into the development branch. Publish subsequent
versions from new committed source; never move an existing release tag.

Once that tag is published to `github.com/webong/gateway`, consumers can use:

```sh
go get github.com/webong/gateway/driver@v0.1.0
```

`v0.1.0` is an example first release, not a claim that it has been published.
Use explicit release versions: development branch commits retain the `lib/`
layout and are not directly fetchable through this public module path.
Downloaded consumers resolve through published tags, independently of the
repository's local development replacements.

Go's [subdirectory module rules](https://go.dev/doc/modules/managing-source#sourcing-multiple-modules-in-a-single-repository)
describe the directory-prefixed tags used here. Third-party extensions publish
under their own module paths and depend on this module. Running one inside
Gateway still requires a host build that includes and registers the extension;
this library does not add dynamic plugin loading.
