---
status: accepted
---

# Model Gateway Extensions as capability packages

A Gateway Extension is the installable package and lifecycle boundary. An extension may contribute one or more **Integration**, **Automation**, or **Companion** capabilities: Integrations configure and run managed application services, Automations execute configured event or schedule logic, and Companions contribute trusted Planner-side behavior through declared contracts. Extension authors use one **Arranger** development module for all of those contracts. This keeps packaging and discovery under the stable Extension concept without calling every contributed behavior a plugin, splitting one authoring model across driver and extension modules, or conflating managed workloads with automation runtimes.

## Considered options

- Call every contributed capability an extension. Rejected because it obscures whether code manages a service, executes operator-configured logic, or extends Planner.
- Replace Extension with Integration everywhere. Rejected because Integration names only the managed-service capability and cannot accurately describe Automation or Companion packages.
- Give each capability an unrelated package lifecycle. Rejected because one distributable extension may need coordinated Spinner and Planner behavior with one identity and version.
- Publish separate Driver and Extension development modules. Rejected because an extension author needs both contracts and packaging adapters, while the split duplicated versioning, dependency wiring, and terminology without creating a useful seam.

## Consequences

- Extension remains the umbrella term for packages, installation, discovery, identity, versioning, and activation.
- Arranger is the single independently versioned Go authoring module. Its root `Extension` interface and its Automation, Companion, and Integration packages form the extension development interface. Its Router package holds runtime-neutral HTTP routing policy so routing does not require a second shared Go module.
- Spinner owns bundled host assembly and capability-specific route policies. Arranger Router accepts those policies as injected contracts and remains independent of Spinner and runtime adapters.
- Local, Docker, and Kubernetes drivers remain Spinner implementation details; they execute an Extension's launch plan but are not a second public authoring module.
- Integration replaces Workload Plugin and Workload Extension in managed-service APIs, binaries, configuration, scripts, and documentation.
- Automation and Companion retain their distinct names and contracts.
- One extension may provide any subset of the three capabilities; a capability is not required to have its own independently versioned package.
- Gateway host binaries do not compile first-party capability implementations. Automation and Integration entrypoints are installable processes; Companion entrypoints are independently reconciled by Planner.
- A package that contributes Automation declares its languages and route, schedule, or credential capabilities in `gateway.automation/v1` metadata. Planner trusts that declaration only after the pinned package has been installed and verified.
- Standalone Spinner, FrankenPHP Router, and RoadRunner Router reconcile the same Planner-owned artifact desired state and select only their named `spinner` entrypoint.
