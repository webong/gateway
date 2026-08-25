# RFC: Optional TCP/Goridge PHP bridge

Status: Deferred

## Decision

Gateway will use the existing HTTP runtime when Laravel is hosted by a standard
PHP server, PHP-FPM, Octane, or FrankenPHP. Go calls the Laravel planner over a
private HTTP connection and receives the JSON route plan over the same
request/response exchange.

No TCP or Goridge runtime is part of the current Gateway contract.

## Why this is deferred

FrankenPHP is an HTTP application server, not a RoadRunner PHP-worker host. It
does not automatically provide RoadRunner's PHP worker relay or Goridge RPC
endpoint. A custom bridge would therefore introduce another long-lived process
and protocol boundary to operate.

Goridge itself can run over TCP, but the protocol is not just a raw socket. A
Go-to-PHP design would need a PHP worker or RPC server that speaks the expected
framing, codec, lifecycle, and error semantics. The standard RoadRunner RPC
pattern is commonly PHP calling Go services; it does not, by itself, provide an
arbitrary Go client for invoking Laravel methods inside FrankenPHP.

## Possible future designs

### Custom Goridge worker bridge

Run a dedicated PHP worker that accepts framed Goridge requests from Gateway and
returns planner responses. Gateway would own the TCP connection pool and the
worker protocol adapter.

This would preserve Goridge framing but require PHP worker bootstrapping,
connection recovery, request limits, authentication, and Laravel state reset.

### Custom private TCP RPC

Define a Gateway-owned TCP protocol using protobuf or length-delimited JSON.
The PHP application would run a dedicated bridge process that handles planner
requests and returns route plans.

This may be simpler to control than implementing the full Goridge worker
protocol, but it creates a new protocol and another PHP service to deploy.

### Embedded PHP runtime

Embed FrankenPHP as a Go library and invoke Laravel through an in-process
`net/http` handler. This could remove the network hop, but introduces CGO,
embedded PHP runtime, thread-safety, memory-isolation, and deployment concerns.

## Requirements before reconsideration

- HTTP mode must demonstrate a measured planner or connection-latency
  bottleneck that justifies the additional operational complexity.
- The bridge must preserve the existing `RoutePlanner` and protocol contracts.
- PHP application state must be isolated between requests, including under
  long-lived workers.
- The design must define authentication, backpressure, request cancellation,
  timeouts, reconnects, graceful shutdown, and observability.
- Standard Laravel, FrankenPHP, and RoadRunner deployments must remain
  independently supported.

## Revisit trigger

Revisit this RFC only when HTTP transport overhead is measured as a material
production constraint or when a deployment requirement makes a private binary
RPC bridge necessary. Until then, HTTP is the supported and preferred
Go-to-Laravel transport outside embedded RoadRunner mode.
