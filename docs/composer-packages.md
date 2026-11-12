# Installing and releasing the Composer packages

The Composer packages under `mod/` and `pkg/` (`webong/gateway`, `webong/gateway-adapter`, `webong/gateway-builder` and the `webong/gateway-*` extensions) are published from this repository. No separate repository per package is needed.

Composer binds a VCS repository to one package name, so adding this repository directly would expose only the root project. Instead, `scripts/split-packages`:

1. splits each package directory into its own history on a branch, `dist/<package>` (for example `dist/gateway-adapter`), with the package's `composer.json` at the root;
2. tags a release on every package as `<package>/vX.Y.Z` (for example `gateway/v1.2.0`);
3. generates a static Composer repository, `packages.json`, from those branches and tags, kept on the `dist/composer` branch.

## Installing

Add the repository once to the consuming project's `composer.json`:

```json
{
  "repositories": [
    { "type": "composer", "url": "https://raw.githubusercontent.com/webong/gateway/dist/composer" }
  ]
}
```

Then require packages by name, at the released version:

```bash
composer require webong/gateway:^1.2 webong/gateway-web:^1.2
```

All packages are released together and share one version, so use the same constraint for every Gateway package. Packagist is not involved. GitHub's raw host caches responses for a few minutes, so a new release can take a moment to appear.

**Development versions.** `dev-main` of each package is the tip of its `dist/<package>` branch. Packages at `dev-main` depend on each other at `dev-main`, so the project must accept dev stability and should require every Gateway package at `dev-main`:

```json
{ "minimum-stability": "dev", "prefer-stable": true, "require": { "webong/gateway-web": "dev-main" } }
```

Mixing `dev-main` of one Gateway package with a released version of another does not resolve.

## Releasing

Push a monorepo release tag, `vX.Y.Z`. The `Composer packages` workflow tests the tool, splits every package, tags each `<package>/vX.Y.Z`, regenerates the metadata and pushes the `dist/*` branches, the tags and `dist/composer` in one atomic push.

To run it by hand, or to preview a release:

```bash
scripts/split-packages release --version=1.2.0 --dry-run    # what would happen, nothing changes
scripts/split-packages release --version=1.2.0              # local branches, tags and metadata
scripts/split-packages release --version=1.2.0 --push       # and publish them
```

The `Composer packages` workflow can also be started by hand (Actions, Run workflow). With no version it refreshes the `dist/` branches and the metadata so `dev-main` is current; with *push* unticked it only reports.

A release is refused, and nothing is changed, when:

- a package already has that version tagged at different content (versions are not reused);
- a tagged package would require another Gateway package at a version that is not published;
- a `dist/<package>` branch would not fast-forward (its history changed); pass `--force-branches` only when that is intended.

Every package is tagged on each release, including packages that did not change; an unchanged package simply gets the new tag on the same commit.

### What the metadata contains

For each package and version, the entry is that commit's own `composer.json` (without the root-only keys `repositories`, `config`, `scripts`, `minimum-stability`, `prefer-stable`), plus the commit's date and where to get it: the git clone URL and a GitHub zip URL at the exact commit. In tagged versions, a requirement on another Gateway package of `dev-main` is rewritten to `^X.Y.Z`, so a release installs as a stable set; `dev-main` entries keep `dev-main`. Packages are listed in a fixed order and the same refs always generate the same file.

### Using another host

`--source-url` sets the clone URL written into the metadata (default: the remote's URL, with `git@github.com:` turned into `https://github.com/`), `--dist-url` sets a zip URL template (`{sha}` is replaced by the commit) and `--metadata-branch` changes the metadata branch. For a private repository, consumers need credentials for both the metadata URL and the zip URL; the generated `README.md` on the metadata branch shows the repository URL to use.

## Notes

- Packages also require packages that are not part of this split, for example `webong/proxy`; Composer resolves those from their own repositories as usual.
- `dist/composer` is generated. Do not edit it by hand.
- `git subtree` must be available (it is part of the standard git distribution).
- `bash scripts/test-split-packages` (or `make test-split-packages`) tests the tool on a throwaway repository, including a real `composer install` from the generated repository. It needs no network.
- Run `scripts/split-packages --help` for every option.
