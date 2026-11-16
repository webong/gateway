# Installing and releasing the Composer packages

The packages under `mod/` and `pkg/` are served from this repository. Composer
Source Plugin owns their splitting, versioning and index generation. Gateway
requires public `webong/composer-source-plugin:^0.5.1` as an author development
dependency; consumers do not need this publisher installed.

**Current release boundary:** do not publish Gateway packages, tags, metadata or
release artifacts until separately authorized. The frozen standalone
v0.2.0-rc.1 artifacts are independent of this tooling change. Commands below
explain the publication procedure; they do not authorize running it live.

## Installing

Add the existing index to the consuming project's `composer.json`:

```json
{
  "repositories": [
    { "type": "composer", "url": "https://raw.githubusercontent.com/webong/gateway/dist/composer/packages.json" }
  ]
}
```

Then require packages at a version that is already published. For example, the
historical index contains `webong/gateway:0.2.0` and matching extension packages.
Use matching constraints for sibling Gateway packages. Packagist is not involved
in resolving this index; external dependencies resolve through their own
repositories. GitHub's raw host may cache a new index briefly.

`dev-main` entries describe the package split branches. Their sibling requirements
also use `dev-main`; consumers need `minimum-stability: dev` and should not mix
these with stable Gateway siblings.

The new publisher writes `composer-packages.json` and an identical
`packages.json` alias. Existing directory URLs and locked source/dist URLs keep
working. Historical stable records, including their original URLs and commit
references, are retained exactly. Old package tags must never be rewritten.

## Preparing an author checkout

```bash
scripts/composer-install --prefer-dist
# Fetch and inspect the selected remote's published history first.
git fetch --no-tags origin '+refs/heads/dist/*:refs/remotes/origin/dist/*'
git log -1 origin/dist/composer
git show origin/dist/composer:packages.json
scripts/prepare-composer-publication "$PWD" origin
```

The helper seeds absent local `dist/*` branches from the fetched `origin` refs.
It never fetches, pushes or resets an existing local branch. It requires the
published metadata tracking ref. Substitute the selected remote consistently
when working elsewhere. Review divergence instead of resetting local history.
Metadata, release and verify commands refuse to ignore a remote-tracking
metadata baseline when the corresponding local branch is missing.

## Previewing and publishing

Use both compatibility flags consistently:

```bash
composer source:publish list --ref-names=short --metadata-alias=packages.json
composer source:publish release --release-version=1.2.0 --ref-names=short --metadata-alias=packages.json --dry-run
```

`--ref-names=short` retains `dist/gateway-adapter` and
`gateway-adapter/vX.Y.Z`, rather than introducing vendor-qualified refs.
`--metadata-alias=packages.json` preserves the existing index endpoint. Root
`extra.source-plugin.workspaces` selects committed package manifests; nested
projects are excluded, and Composer-plugin packages such as Builder are included.

Without `--dry-run`, the release command changes local package branches, tags and
metadata. `--push` additionally publishes refs in an atomic remote push. The
`Composer packages` workflow installs the locked author dependencies, runs the
acceptance suite, fetches/seeds the published baselines, then calls the plugin
with these compatibility flags. Its existing global release-tag trigger can
publish all discovered packages, so creating such a tag requires publication
authorization. A manual dispatch defaults to dry-run.

The compatibility entrypoint `scripts/split-packages` delegates to the same
Composer command and translates legacy `--version` to `--release-version`. It
contains no independent split or version-index implementation. Existing callers
can keep using it; new automation uses `composer source:publish` directly.

The plugin refuses reused versions with different content, conflicting stable
references, ambiguous short names, disagreeing index aliases, unrelated metadata
branch files and unintended non-fast-forward package updates. It retains exact
stable records while regenerating development records. It does not retrieve or
validate historical network URLs automatically.

## Verification

```bash
bash scripts/test-split-packages
```

After author dependencies are installed, this suite needs no network. It uses
disposable local repositories and consumers to check split history/determinism,
legacy index migration, old stable records, stable/dev siblings, Builder plugin
activation, genuine ZIP installs, locked source replay, missing-baseline refusal,
safe checkout bootstrap, dry-run/refusal behavior and local atomic pushes.

See the [adoption evidence](verification/composer-source-publisher-adoption.md)
and [upstream command documentation](https://github.com/webong/composer-source-plugin/blob/v0.5.1/docs/publication.md)
for the supported flags and migration contract. `git subtree` must be available.
