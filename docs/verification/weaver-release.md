# Weaver candidate publication and acceptance

PR #24 stays draft and unmerged until replacement acceptance is complete. The
prerequisite WorkFlow release exception does not authorize Weaver publication.
Do not publish tags, archives, Composer metadata or deploy from this checklist
without an explicit exception naming the tested source commit.

## Prepare the candidate

The runtime CI checks out the PR head itself, uses native Linux amd64/arm64 runners,
and builds `0.1.0-rc.1`. The immutable WorkFlow runtime and PHP/Go builder image
digests are recorded in each manifest and candidate report. `package.py --release`
requires a clean source tree and exact HEAD revision, verifies every manifest
artifact, checks runtime/Composer source parity and regenerates archives twice.
This proves deterministic archive creation, not independently rebuilt binaries.

Each architecture produces a runtime TAR, Composer source TAR, Composer package
entry and repository index, SHA256 sums and a report. CI tests the exact Composer
TAR locally before publication, keeping its checksum and replacing only the URL
with a read-only fixture mount. All three real hosts exercise that runtime package.
The standalone cutover CLI must also bootstrap from the installed Composer dist.

No candidate bytes are uploaded by default. Only after explicit publication
approval may `WEAVER_APPROVED_SOURCE_SHA` be set to the exact tested commit and
that commit's runtime jobs rerun. The final upload step then retains both candidate
sets for seven days. It does not publish a release. Verify the job SHA, candidate
manifest revision, version and checksums before downloading or publishing.

## Publish only after approval

Use a signed `weaver-runtime/v0.1.0-rc.1` tag at the approved source commit and a
GitHub prerelease with that tag. Upload both native candidate sets unchanged,
including metadata and checksums. Their HTTPS base is
`https://github.com/webong/gateway/releases/download/weaver-runtime%2Fv0.1.0-rc.1`.
Do not use the global `v*` guest-package release workflow: this candidate has its
own package version. The global workflow skips this namespaced release event.
Keep publication, installation, deployment and merging as distinct actions.

## Verify replacement acceptance

A compatible Gateway host needs the reserved-receipt, own-adapter readiness and
`supports-pinned-http` / `deliver-pinned-http` capabilities introduced on this
branch. Test with the exact reviewed Gateway source; older hosts must refuse
unsupported activation before accepting work.

From a fresh consumer and empty artifact cache, fetch the published HTTPS Composer
index and exact native TAR, apply the operator's artifact trust policy, verify
checksums and run real activation on all three hosts. Do not preload the cache for
this final download test. The Planner root explicitly trusts the Composer source
plugin and selects only WorkFlow's `core` surface; the dependency cannot grant
itself that trust. Configure account grants, private credentials, operational SQL
schema and operator HTTP destination secrets as documented in Weaver's README,
HTTP and UPGRADE guides. No destination URL or provider credential comes from a
client workflow input.

Rehearse stopped-writer copied-data cutover and current-progress rollback on an
operator-owned copy, covering the real provider's deduplication guarantee and
unknown-outcome reconciliation. Cross-engine migration and Weaver-native runs
back into the legacy app are unsupported. No production data was used in CI.

Only after compatible published replacement acceptance should WorkFlow's `app/`
move into examples or receive versioned deprecation guidance. Preserve engine and
authoring CLI independence. Keep PR #24 unmerged until the full setup is complete;
publication alone does not close these gates.
