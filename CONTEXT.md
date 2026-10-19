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
The Gateway product area for authoritative name-query ingress and routing.

**Automation**:
Configured logic that runs in response to an incoming event or a scheduled occurrence.

**Schedule**:
A recurring time-based trigger for an Automation.
