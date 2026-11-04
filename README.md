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
vendor/bin/gateway-extension build \
  --manifest=extension.build.json \
  --version=1.0.0 \
  --output=dist/webong%2fexample
```

Or through its Composer plugin command:

```bash
composer gateway-extension:build \
  --manifest=extension.build.json \
  --extension-version=1.0.0 \
  --output=dist/webong%2fexample
```

The manifest declares the extension identity, descriptor, metadata, artifacts,
and entrypoints. Artifact sources are resolved relative to the manifest. The
builder rejects traversal and symbolic links, copies artifacts with restrictive
permissions, calculates deterministic SHA-256 digests, injects package identity
into plugin descriptors, and writes `plugin.json`.
