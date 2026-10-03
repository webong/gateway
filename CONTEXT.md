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

**DNS**:
The Gateway product area for authoritative name-query ingress and routing.

**Automation**:
Configured logic that runs in response to an incoming event or a scheduled occurrence.

**Schedule**:
A recurring time-based trigger for an Automation.
