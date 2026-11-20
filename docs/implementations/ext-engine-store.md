# Ext engine and archive-store adoption

Gateway consumes released `github.com/webong/ext/pkg/ext-go/engine v0.3.0`
(tagged source `3198d351099c1c22c86d4fbf985f96872fa12785`). The former
`pkg/plugin-go/contract` imports and module requirements are removed throughout
Arranger, guests, hosts and runtime acceptance tooling. Public Ext protocol/package
identities remain `ext/v1` and `ext.package/v1`; this changes the Go engine module,
not Gateway's extension desired-state API or WorkFlow's pinned core/rollback source.

Spinner uses `Store.InstallArchive` for verified downloads, extraction, installs,
upgrades and persisted archive receipts. `NewArtifactHTTPClient` delegates the
redirect policy to Ext's `NewDownloadClient`. Gateway retains:

- 512 MiB maximum download, single file and expanded package size;
- 250,000 maximum archive/tree entries;
- a 60-second context deadline even with an injected download client, plus the
  default artifact client's 60-second timeout;
- HTTPS/TLS verification, at most five redirects, forbidden URL credentials and
  fragments, and credential/referrer stripping on redirects;
- desired identity, version and optional directory digest verification before
  loading any guest; Planner credentials use their separate client;
- dependency resolution, capability selection and fail-closed guest unloading.

The store owns `.receipts` (archive SHA-256 and package identity/revision). Gateway
keeps desired versions, configuration revisions and activation state; those are
not inferred from an archive receipt. Matching verified receipts avoid downloads
across process restarts and desired-state revision changes.

Existing installed package directories are retained. On first use without an Ext
receipt, Spinner fetches the pinned archive once; the store verifies it against
the existing revision/content and records its receipt. Old Gateway `.desired`
receipt files are unused and need not be deleted. Corrupt installations fail
before a download rather than being repaired. A different archive for an already
installed revision must have identical package content or the store rejects it.
Do not clear store contents or receipts to bypass a verification failure.

The store accepts zip, tar.gz and plain tar archives, identified by contents.
Links, special files, unsafe/colliding paths and oversized trees are rejected.
Gateway's existing tar.gz distributions remain supported. Unsupported formats
(zip64, zstd/xz/bzip2 archives) are not enabled by this migration. Executable
entrypoints retain the store's normal copy policy; arbitrary helper archive modes
do not grant execution permission.

Planner's PHP installer and catalogs are unchanged: the PHP runtime has no matching
store API in this migration. No C engine binding, publication, deployment or
production store mutation is part of the source change. Frozen release assets and
the privately staged Weaver archives remain tied to their original source; new
release candidates must be built and accepted at their own exact source revision.

Regression coverage exercises receipt creation for an existing directory install,
cache reuse after reopening the store, wrong desired version/digest, corrupt
installed files, the injected-client deadline and the expanded-tree size budget.
Existing HTTPS redirect/TLS, guest activation, removal and dependency tests remain.
