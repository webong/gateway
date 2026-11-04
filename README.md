# Gateway Adapter

Framework-free PHP interfaces and protocol values shared by Gateway Planner
and optional Planner extension packages live here. Implementations remain in
Planner or the extension that owns them.

`ExtensionProviderRegistry` is the Composer discovery seam. An extension-owned
autoload file records its companion provider class there; Planner registers the
discovered classes without importing or enumerating individual extensions.

`CompanionActionRegistry` is the execution seam for trusted PHP behavior that
an installed extension contributes to the Planner host. Extensions register a
stable action name and handler class; Spinner invokes only that name and typed
input through the private companion-action protocol. The contract never accepts
PHP source.

Install `webong/gateway-adapter` when building a custom Planner or an extension
that only needs the stable Gateway seam. The package has no Laravel or Symfony
dependency. The official Planner implementation is published separately as
`webong/gateway` from `pkg/planner`.
