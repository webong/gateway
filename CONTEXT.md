# Gateway

Gateway routes application-layer traffic through a transport and policy boundary.
Its product areas are Web, Email, and DNS. Its two modules can be deployed together or separately.

## Language

**Spinner**:
The Go module that owns public ingress and outbound delivery.
_Avoid_: Go gateway, router

**Planner**:
The PHP module that owns routing policy and registry decisions.
_Avoid_: Laravel gateway

**Planner Resource**:
An operator-managed desired-state record with a stable key and a declared kind. It is distinct from a process's deployment configuration.
_Avoid_: environment setting, arbitrary config key

**Resource Kind**:
The contract that gives a Planner Resource its meaning and validation rules.
_Avoid_: database table type

**Operational Record**:
A record created by Gateway while handling traffic or running work, such as a delivery, execution, lease, receipt, or usage counter. It is observed activity, not operator-managed desired state.
_Avoid_: Planner Resource

**Router**:
The bundled service containing both Spinner and Planner behind one public listener.
_Avoid_: Standalone Gateway, FrankenPHP image

**Spinner Node**:
A running Spinner instance that serves Gateway transports and executes deliveries.
_Avoid_: Provisioning node

**Provisioning Node**:
A worker that reconciles managed server workloads under Planner's assignment policy.
_Avoid_: Spinner node

**Gateway Extension**:
An installable package that contributes Gateway behavior to one or both planes. An Extension may provide Spinner and Planner entrypoints and may also contribute Integration, Automation, or Companion capabilities.
_Avoid_: Plugin, Integration when referring to the whole package

**Planner Extension**:
The Planner-side entrypoint of a Gateway Extension. It may contribute resource kinds, management routes, migrations, commands, and protocol policy while remaining part of the Extension's shared identity and version.
_Avoid_: Product Module, Planner Plugin

**Arranger**:
The development interface through which an Extension declares and implements its Gateway capabilities without depending on a Gateway host.
_Avoid_: Driver module, Extension module

**Integration**:
An Extension capability that teaches Gateway how to configure and run a managed application service such as Reverb, Mercure, or Centrifugo.
_Avoid_: Workload Plugin, Workload Extension

**Delivery Admin Endpoint**:
The private authenticated listener of a Spinner Node used to inspect and redrive deliveries.

**Web**:
The Gateway product area for HTTP-based application traffic: Webhooks, WebSockets, and Webstreams.

**Webhook**:
An HTTP request event routed through Gateway to a response or subscribed destinations.

**WebSocket**:
A bidirectional Web session whose connection and messages are routed through Gateway.

**Webstream**:
A one-way Web event stream delivered to a client as Server-Sent Events (SSE).
_Avoid_: SSE as the product-area name

**Email**:
The Gateway product area for mail ingress, routing, and delivery; mailbox storage belongs to an external system.
_Avoid_: SMTP as the product-area name

**Account**:
An authenticated principal that owns Planner resources across product areas. It is not an external mailbox, message store, or a resource definition. A service must authorize access to each resource kind explicitly; account ownership alone does not grant cross-service access.

**Mailbox Connection**:
An account-owned Planner Resource describing the approved IMAP and SMTP transport path for an external mailbox. Its live credentials and observed health are separate from its desired state.

**DNS**:
The Gateway product area for application-layer name-query ingress and routing through managed, provided, or forwarded answer modes.

**Managed DNS**:
DNS answers owned and produced by Gateway from Planner-managed zones and records.

**Provided DNS**:
DNS answers supplied synchronously by an application through Gateway's normal Webhook, Automation, or Integration Interfaces. Gateway remains authoritative for the configured zone and validates the returned answer document.

**Forwarded DNS**:
DNS queries proxied to an upstream DNS resolver under Planner-managed domain and client policy. The upstream resolver owns recursion, caching, and DNSSEC validation.

**Automation**:
Configured logic that runs in response to an incoming event or a scheduled occurrence.

**Companion**:
Trusted Planner-side behavior contributed by a Gateway Extension and invoked through a declared contract. It is deployed application code, not operator-authored Automation source.
_Avoid_: Planner Plugin, PHP Automation

**PHP Companion Action**:
The current PHP implementation of a Companion, invoked by a stable action name inside the Planner host.
_Avoid_: uploaded PHP script, PHP Automation

**Schedule**:
A recurring time-based trigger for an Automation.
