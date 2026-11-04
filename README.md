# Gateway Builder

`webong/gateway-builder` assembles deterministic `ext.package/v1` Gateway
extension directories from declarative build manifests. It is development
tooling and does not belong in extension runtime dependencies.

Install it with Composer:

```bash
composer require --dev webong/gateway-builder
```

Build through the installed binary:

```bash
vendor/bin/gy build \
  --manifest=extension.build.json \
  --version=1.0.0 \
  --output=dist/webong%2fexample
```

Or through its Composer plugin command:

```bash
composer gy:build \
  --manifest=extension.build.json \
  --extension-version=1.0.0 \
  --output=dist/webong%2fexample
```

The manifest declares the extension identity, descriptor, metadata, artifacts,
and entrypoints. Artifact sources are resolved relative to the manifest. A
manifest may also select a named source root with `"sourceRoot": "generated"`;
provide it by repeating `--source=NAME=DIR`. Repeat `--value=NAME=VALUE` to
expand custom `{{name}}` placeholders alongside the built-in identity, version,
and revision placeholders.

The builder rejects traversal and symbolic links, copies artifacts with
restrictive permissions, calculates deterministic SHA-256 digests, injects
package identity into plugin descriptors, and writes `plugin.json`.
