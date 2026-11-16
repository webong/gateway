# Composer Source Plugin publisher adoption

Verified on 2026-11-16 against public **v0.5.1**, source commit
`52605a4e707c07fcc4baf64cf0614a05f27ab25d`.

## Result

Gateway now requires `webong/composer-source-plugin:^0.5.1` in its author-only
`require-dev`. The targeted lock update changes only that package, from v0.4.0
to v0.5.1. Production app/extension manifests and locks are unchanged.

The duplicate publisher implementation has been removed. `scripts/split-packages`
is a thin invocation adapter translating legacy `--version` to Composer's
`--release-version`, preserving the caller's repository and selecting
`--ref-names=short --metadata-alias=packages.json`. No temporary local source
override remains. Publication workflows invoke the Composer command directly.

The workflow installs locked author dependencies and explicitly fetches selected
remote publication refs. `scripts/prepare-composer-publication` seeds only absent
local `dist/*` branches and requires the published metadata baseline. Existing
local branches are never reset. This preserves the history the plugin must read
before replacing metadata and enables non-fast-forward checks on package refs.

## Public-package verification

A new temporary project with an empty Composer home and cache installed v0.5.1
from public Packagist, without a path/VCS repository override. Its lock points to
the release commit above. All 47 shipped PHP source files match the plugin
installed in Gateway byte-for-byte.

Earlier v0.5.0 checks reproduced nested-manifest discovery failure, rejection of
Composer-plugin packages and loss of old short-ref versions/index filenames.
Those gaps were sent to the Composer Source Plugin chat. Its tested local fixes
were independently accepted before release; final Gateway acceptance uses the
public release, not that local candidate.

## Acceptance coverage

`bash scripts/test-split-packages` passes through the real Composer command:

- nested companion projects are excluded; Builder's Composer-plugin package is
  included, installed and actually activated;
- split roots/history, determinism, stable/dev sibling requirements and version
  collision/non-fast-forward refusals remain correct;
- migration from a legacy-only `packages.json` retains complete historical stable
  records, with byte-identical old/new index files;
- a missing local metadata baseline refuses with no ref/output changes;
  bootstrapping a fresh checkout succeeds and preserves existing local branches;
- fresh historical source installation and plugin-disabled locked replay pass;
- fresh ZIP installs use package roots, preserve plugin metadata and explicitly
  assert `installation-source: dist`, preventing a source fallback from passing;
- dry-run immutability, verification and atomic pushes to disposable local bare
  remotes pass. No live push is part of these tests.

A new disposable clone of Gateway's actual published history was seeded from
its remote-tracking refs and run through the public publisher. All 14 historical
0.2.0 stable records compare exactly as structured JSON, including source/dist
URLs, references and other fields. Both index filenames are byte-identical;
every package branch and tag remains unchanged. Only the disposable metadata
branch moves. The primary Gateway checkout's publication refs are untouched.

Evidence is under `/private/tmp/gateway-source-publisher-adoption`:
`public-v051-install.log`, `public-v051-gateway-tests.log`,
`public-history-bootstrap.log`, `public-history.log`, `public-history-output/`,
`public-gateway-list.log` and `public-locked-install.log`. Temporary evidence is
local and may be cleaned; committed fixtures reproduce the compatibility suite.

## Publication boundary and remaining steps

Source commits and CI delivery are separate from publication. This migration
does not change live package metadata, package refs, release tags, runtime
archives or release assets.
The signed local freeze record at `30359f98` remains local-only. The frozen
v0.2.0-rc.1 manifest digest remains
`aac50ac458847bb6df9dfc57c93f4d1171e01ff4735ac15a5679e9fc81950b3d`.
The trusted freeze verifier confirms its 4 images and 2 extension archives.

The pre-commit review found and fixed caller-relative path handling in the thin
adapter. Regression coverage includes both option syntaxes from an external
checkout. Standards review found no blockers. Source review/CI can proceed
without publishing the package index. A future explicitly authorized
publication must fetch current refs, inspect and seed the metadata baseline,
review the generated index against current stable records, then publish refs.
Do not create a global version tag as part of the frozen standalone preview
without reviewing its separate all-package publication trigger. No root
Packagist catalog was introduced; Gateway remains a development project.
