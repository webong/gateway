# Arranger consumer fixture

This nested Go module models a third-party consumer of Gateway Arranger. It is
copied into an isolated repository by `scripts/test-arranger-release`, where it
resolves a synthetic published Arranger tag without a workspace or local
`replace` directive.

Keep this module out of the root `go.work`. Its packages exercise Arranger's
public Automation and Integration interfaces, while `cmd/integration` proves a
consumer can build an executable integration without importing Spinner.
