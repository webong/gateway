# Gateway PHP Contract

Framework-free PHP interfaces and protocol values shared by Gateway Planner
and optional Planner extension packages live here. Implementations remain in
Planner or the extension that owns them.

`ExtensionProviderRegistry` is the Composer discovery seam. An extension-owned
autoload file records its companion provider class there; Planner registers the
discovered classes without importing or enumerating individual extensions.

The root `webong/gateway` Composer package currently includes this source and
autoloads its namespaces. The nested manifest keeps the seam independently
testable and ready for a split-package release without coupling extensions to
`src/planner` implementation paths.
