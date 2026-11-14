# WorkFlow core release candidate for Weaver

Proposed tag: `v0.1.0-rc.2` in `webong/workflow`.
Exact target: `00e0bbcdd61c2c6ed96af55865d0ef2e77557adb`.
This is the clean merged queue/RPC prerequisite, excluding unrelated Composer
projection work. No source change or old-app deletion is needed for this tag.

## Why it is required

The published `v0.1.0-rc.1` predates queue contracts used by Weaver, including
`FlowQueueScheduler`, `FlowStepClaim` and `FlowDeferredCompletion`. Weaver is
currently pinned to the exact merged commit for development. A compatible core
tag is needed before replacing that temporary constraint and establishing clean
released-package consumption.

## Verified evidence

- All source checks passed at the exact target: validation, PHP tests, native
  amd64/arm64, RPC amd64/arm64. Publication was skipped.
- [Post-merge source CI](https://github.com/webong/workflow/actions/runs/43508334306).
- Weaver's extracted PHP 8.3 SQL suite passed 126 tests / 691 assertions, no skips,
  using the exact core commit through Composer, against SQLite, PostgreSQL and MySQL.
- The core worktree is detached at this commit and clean. Existing WorkFlow app,
  optional adapters and native sources remain in the core repository.

## Proposed release notes

Add the queue-backed RPC authority contracts and hardened recovery semantics used
by application hosts. Preserve logical/scoped run identities, pinned definitions
and execution authority across callbacks, cancellation, worker restart and lease
recovery. Include the compatible standalone RPC/native distributions while the
Gateway Weaver replacement is being developed. No automatic data migration or
run-authority conversion is introduced.

## Publication boundary

The existing WorkFlow tag workflow builds and publishes versioned native/RPC
artifacts as well as the GitHub release. Publishing this prerequisite requires an
explicit exception to the migration handoff's instruction not to publish artifacts.
It does not authorize publishing Weaver/Gateway artifacts, deploying either
service, deleting the old app, or merging Gateway PR #24.
