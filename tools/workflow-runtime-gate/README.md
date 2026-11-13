# WorkFlow runtime feasibility gate

This is an issue #18 prototype, not `pkg/workflow` and not a published extension.
It consumes only merged WorkFlow commit `00e0bbcdd61c2c6ed96af55865d0ef2e77557adb`.
The inventory and proposed runtime decision are in `docs/verification/workflow-extension-inventory.md` and ADR 0012.

The first probe builds the unmodified Go/FrankenPHP RPC bridge, then assembles its PHP 8.3 CLI, PDO modules and ELF dependencies into an isolated runtime closure. The closure must run without host PHP or a compiler. Runtime coverage must include both architectures, supervised worker/readiness, private authenticated RPC and recovery before extraction can proceed. Test-only bootstrap files are trusted fixtures, never client-selectable code.

## Reproduce

```sh
GOWORK=off go -C tools/workflow-runtime-gate test -race ./...
tools/workflow-runtime-gate/build.sh /path/to/clean/workflow arm64
python3 tools/workflow-runtime-gate/smoke.py gateway-workflow-runtime-gate:arm64
# Repeat build + smoke with amd64; emulation is not native hardware validation.
```

The build refuses a dirty source or a commit other than the merged prerequisite. It builds the current upstream bridge without changing its files, statically compiles the Arranger supervisor, copies PHP/PDO and the ELF dependency closure, hashes every packaged file in an Ext manifest and runs only that package in `scratch`. No artifacts are published. The smoke fixture creates a unique disposable state volume and removes its own containers/volume.

The executable's `--session-probe` mode is an independent Ext consumer harness: it verifies the manifest, opens the real JSON-line guest session and binds only an identity-scoped test secret resolver. Production mode is the named Spinner entrypoint. `--probe` is a direct-start diagnostic and is insufficient evidence for the Ext gate by itself. Test-only token/definition/SQLite bootstrap fixtures are not a production configuration or permission model.

## Still gated

The scratch proof alone does not verify integration with standalone Spinner, FrankenPHP Router or RoadRunner Router. Actual lifecycle/capability-loss acceptance across those hosts, production resource grants and scoped credentials, released core consumption, SQL matrix, copied-data cutover/rollback and reproducible release publication remain separate gates. Do not treat the probe as the production extension or delete WorkFlow's old `app/`.
