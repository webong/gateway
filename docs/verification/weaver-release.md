# Weaver candidate publication and acceptance

PR #24 was merged into main at `543e8d50` after explicit source-integration
approval. The owner subsequently reiterated: do not publish anything yet. The
prerequisite WorkFlow release exception does not authorize Weaver publication.
Do not publish tags, runtime archives or installation metadata, or deploy from
this checklist without an explicit exception naming the tested source commit.

## Release scope

Weaver is distributed for installation through Gateway. The release consists of
Linux amd64/arm64 runtime archives, their SHA-256 checksums and Gateway installation
metadata identifying the version, source revision, platform and archive URL/digest.
Each runtime archive already contains its verified `plugin.json` manifest.

There is no separate Composer publication requirement: do not publish Weaver
Composer source TARs, package indexes, split branches or Composer package tags.
Direct PHP consumers may use Composer Source Plugin with a pinned Gateway checkout
and its `pkg/weaver` workspace. They must retain their own root-owned plugin trust,
WorkFlow core-surface selection and locks. This optional source-consumer path is
not a Gateway runtime installation prerequisite.

## Prepare the candidate

The runtime CI checks out the PR head itself, uses native Linux amd64/arm64 runners,
and builds `0.1.0-rc.1`. The immutable WorkFlow runtime and PHP/Go builder image
digests are recorded in each manifest and candidate report. `package.py --release`
requires a clean source tree and exact HEAD revision, verifies every manifest
artifact, checks runtime/Composer source parity and regenerates archives twice.
The gzip compression level is explicit so Python default changes cannot alter
archive bytes. Original local arm64 cold-cache rebuilds matched; explicit level 9
also reproduces the original native CI runtime and Composer archive checksums.

The candidate tooling currently produces both a runtime TAR and internal Composer
source TAR/index fixtures, SHA256 sums and a report for each architecture. CI
tests the exact Composer TAR locally, keeping its checksum and replacing only the URL
with a read-only fixture mount. All three real hosts exercise that runtime package.
The standalone cutover CLI must also bootstrap from the installed Composer dist.
The private HTTPS Composer test fetches an index and source TAR with an empty
download cache; it rejects untrusted TLS and corrupt archives before accepting
the explicitly trusted fixture CA. No ports are published and no assets leave
the disposable Docker fixture.

The Composer outputs remain internal packaging/consumer test fixtures. Their
existing tests remain useful but do not create a Composer release gate.

No candidate bytes are uploaded by default. Only after explicit publication
approval may `WEAVER_APPROVED_SOURCE_SHA` be set to the exact tested commit and
that commit's runtime jobs rerun. The final upload step then retains both candidate
sets for seven days. It does not publish a release. Verify the job SHA, candidate
manifest revision, version and checksums before downloading or publishing.

## Publish only after approval

Use a signed `weaver-runtime/v0.1.0-rc.1` tag at the approved source commit and a
GitHub prerelease with that tag. Stage only the two verified runtime TARs unchanged:

- `gateway-weaver-linux-amd64.tar.gz`
- `gateway-weaver-linux-arm64.tar.gz`

Prepare runtime-only checksum and Gateway installation metadata files from the
verified candidate reports. Preserve each runtime digest, identity, source revision,
platform and reviewed build inputs; use the approved HTTPS archive URLs. The
candidate `SHA256SUMS-*` and `weaver-*.json` reports also reference internal Composer
TARs, so filter those entries for the runtime release. Do not upload the entire
candidate directory or advertise unpublished Composer assets. Review the resulting
runtime-only file list before uploading anything. Their HTTPS base is
`https://github.com/webong/gateway/releases/download/weaver-runtime%2Fv0.1.0-rc.1`.
Do not use the global `v*` guest-package release workflow: this candidate has its
own package version. The global workflow skips this namespaced release event.
Keep publication, installation, deployment and merging as distinct actions.

## Verify replacement acceptance

A compatible Gateway host needs the reserved-receipt, own-adapter readiness and
`supports-pinned-http` / `deliver-pinned-http` capabilities merged through PR #24. Test with the exact reviewed Gateway source; older hosts must refuse
unsupported activation before accepting work.

From a fresh Gateway installation and empty artifact cache, use the Gateway
installation metadata to fetch the exact published native TAR over HTTPS, apply
the operator's artifact trust policy, verify checksums and run real activation on
all three hosts. Do not preload the cache for this final download test.

Composer remains an internal build/source-consumer dependency. A PHP source
consumer explicitly trusts the Composer Source Plugin and selects only WorkFlow's
`core` surface; the dependency cannot grant itself that trust. Public Composer
index/dist installation is not required for runtime release acceptance.

Configure account grants, private credentials, operational SQL schema and operator
HTTP destination secrets as documented in Weaver's README,
HTTP and UPGRADE guides. No destination URL or provider credential comes from a
client workflow input.

Rehearse stopped-writer copied-data cutover and current-progress rollback on an
operator-owned copy, covering the real provider's deduplication guarantee and
unknown-outcome reconciliation. Cross-engine migration and Weaver-native runs
back into the legacy app are unsupported. No production data was used in CI.

Only after compatible published replacement acceptance should WorkFlow's `app/`
move into examples or receive versioned deprecation guidance. Preserve engine and
authoring CLI independence. Source integration is already complete; publication
alone does not close replacement acceptance.

## Rehearse downloads without publication

Build the Weaver package and actual hosts using the existing build scripts, then
run these private Docker-only acceptance commands:

```sh
python3 pkg/weaver/tests/https-composer-smoke.py gateway-weaver:arm64
python3 tools/workflow-runtime-gate/host-matrix.py spinner gateway-weaver:arm64 --fresh-https
python3 tools/workflow-runtime-gate/host-matrix.py roadrunner gateway-weaver:arm64 --fresh-https
python3 tools/workflow-runtime-gate/host-matrix.py frankenphp gateway-weaver:arm64 --fresh-https
```

Use `amd64` on its native runner. Each run creates and removes its own network,
certificate, temporary caches, containers and SQL volumes. The host matrix never
preloads package directories or desired receipts in this mode. Both production
downloaders must fetch and verify the archive before activation. The Planner
installer streams the download and expands gzip to bounded temporary files,
retaining its normal memory budget; a separate regression installs an 80 MiB
runtime under a 64 MiB limit. Ext deliberately installs non-entrypoint files as private data. Weaver prepares
only its fixed, verified ELF loader for owner execution after installation;
arbitrary TAR permission bits do not grant execution permissions.
TLS verification remains enabled; provider-effect HTTP redirects remain forbidden.

The native runtime workflow runs these checks on relevant main pushes and PRs.
Candidate upload remains exact-source approval gated; do not enable it while
publication is prohibited. This rehearsal is deliberately distinct from eventual
published HTTPS/CDN installation and operator-specific trust/backup acceptance.
