---
status: accepted
---

# Model Gateway Extensions as capability packages

A Gateway Extension is the installable package and lifecycle boundary. An extension may contribute one or more **Integration**, **Automation**, or **Companion** capabilities: Integrations configure and run managed application services, Automations execute configured event or schedule logic, and Companions contribute trusted Planner-side behavior through declared contracts. This keeps packaging and discovery under the stable Extension concept without calling every contributed behavior a plugin or conflating managed workloads with automation runtimes.

## Considered options

- Call every contributed capability an extension. Rejected because it obscures whether code manages a service, executes operator-configured logic, or extends Planner.
- Replace Extension with Integration everywhere. Rejected because Integration names only the managed-service capability and cannot accurately describe Automation or Companion packages.
- Give each capability an unrelated package lifecycle. Rejected because one distributable extension may need coordinated Spinner and Planner behavior with one identity and version.

## Consequences

- Extension remains the umbrella term for packages, installation, discovery, identity, versioning, and activation.
- Integration replaces Workload Plugin and Workload Extension in managed-service APIs, binaries, configuration, scripts, and documentation.
- Automation and Companion retain their distinct names and contracts.
- One extension may provide any subset of the three capabilities; a capability is not required to have its own independently versioned package.
