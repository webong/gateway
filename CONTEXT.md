# Gateway

Gateway routes incoming protocol events through a transport and policy boundary.
Its two modules can be deployed together or separately.

## Language

**Spinner**:
The Go module that owns public ingress and outbound delivery.
_Avoid_: Go gateway, router

**Planner**:
The PHP module that owns routing policy and registry decisions.
_Avoid_: Laravel gateway

**Router**:
The bundled service containing both Spinner and Planner behind one public listener.
_Avoid_: Standalone Gateway, FrankenPHP image

**Automation**:
Configured logic that runs in response to an incoming event or a scheduled occurrence.

**Schedule**:
A recurring time-based trigger for an Automation.
