---
status: accepted
---

# Structure concurrency by ownership, lifecycle, and durability

Gateway does not use one concurrency mechanism everywhere. It chooses the
smallest mechanism that expresses the lifetime and guarantees of the work:
coordinated service groups for host lifecycle, bounded executors for stateless
work, actors for long-lived addressable state, and durable stores with leases
for cross-process truth. Ergo is therefore an implementation tool for local
ownership and supervision, not Gateway's general scheduler or durability
layer.

## Decision

Gateway classifies concurrent work into four categories.

| Category | Gateway examples | Required structure |
| --- | --- | --- |
| Host lifecycle | listeners, pollers, reconcilers, and background services started by one Spinner process | One parent context and a service group that cancels siblings on terminal failure and waits for bounded shutdown |
| Bounded stateless work | ingress Automations, Schedule execution, and outbound delivery workers | A capability-level Executor or WorkerPool contract with explicit capacity, bounded admission, a total deadline, cancellation, and observable outcomes |
| Stateful addressable ownership | a logical WebSocket session or a managed Integration process with non-trivial restart state | One owner for mutable state; use an Ergo actor and supervisor when mailbox ordering, addressability, or restart policy materially simplifies the lifecycle |
| Durable coordination | delivery retries, Schedule claims, execution identities, desired state, and cross-node ownership | Planner or another durable store remains authoritative through records, idempotency keys, claims, and fenced leases |

The capability contract hides the concurrency implementation. For example,
Automation callers depend on `automation.Executor`; they do not send actor
messages, select worker identities, or know whether a local implementation uses
goroutines or Ergo. This preserves one admission, deadline, cancellation,
error, and metrics contract while allowing implementations to be compared or
changed without altering route handling.

Every queue, mailbox, worker set, connection set, and shutdown wait is bounded.
Waiting for capacity consumes the caller's total deadline; Gateway never holds
a request indefinitely. Synchronous ingress reports overload or timeout through
its protocol contract. Asynchronous work is accepted only after its relevant
durable record or queue owns recovery; actor mailbox delivery alone is not an
acceptance or completion guarantee.

## Actor boundary

Use an actor when the concurrent thing has a stable logical identity, owns
mutable state across messages, requires ordered handling, or has a lifecycle
whose supervision policy is part of the domain. The first intended Gateway
candidate is a logical WebSocket session once Gateway needs server-initiated
delivery, reconnectable session state, targeted routing, or cross-component
session lookup. A managed Integration process is another candidate when its
health, backoff, dependency, and restart transitions outgrow the existing
serialized registry lifecycle.

Do not create an actor merely because code is concurrent. Request-scoped
protocol I/O, response streaming, SMTP and IMAP connections, independent
outbound jobs, and single-owner reconciliation loops remain ordinary
goroutines with contexts and explicit bounds. A fixed goroutine worker pool is
the default for low-complexity stateless work; a local Ergo pool is appropriate
when its supervision and observability justify the additional runtime.

Actor callbacks must not block for an unbounded time. All calls, I/O, mailbox
operations, and waits have hard deadlines. Actor state is not shared by mutable
pointer with other actors. Restart does not imply replay: work with possible
external side effects is retried only through its durable, idempotent execution
contract.

## Distributed boundary

Actors and pools are local process structure by default. They do not replace
Planner's desired state, Schedule claims, delivery records, idempotency
receipts, or provisioning leases. Cross-node actor execution remains governed
by [ADR-0003](0003-defer-cross-node-ergo-automation-execution.md) and stays
disabled until its durable execution preconditions are met.

## Considered options

- Use Ergo for all Gateway concurrency. Rejected because request-scoped I/O,
  stateless bounded work, and desired-state loops do not benefit from actor
  identity, while actor mailboxes do not provide durable acceptance or
  exactly-once execution.
- Use goroutines and mutexes everywhere. Rejected for long-lived addressable
  state because ownership, ordering, supervision, and failure isolation would
  be repeatedly reimplemented in registries and shared maps.
- Treat actors as the cross-node source of truth. Rejected because process and
  network failure can leave completion ambiguous; durable identities, leases,
  idempotency, and terminal results remain necessary.
- Choose Ergo or goroutines in route and protocol handlers. Rejected because it
  leaks infrastructure into policy and lets callers bypass consistent
  admission and safety semantics.

## Consequences

- Spinner host assembly should converge on one coordinated service-lifecycle
  primitive for related listeners and background loops.
- Existing Automation executors remain interchangeable behind one contract;
  performance claims require measurement under identical capacity and timeout
  settings.
- Existing durable queues, Schedule claims, and reconcilers remain authoritative
  and are not migrated into actor memory.
- WebSocket session ownership is the next place to evaluate an actor-shaped
  implementation, but the current request-owned connection loop should not be
  rewritten until session identity or server-directed behavior requires it.
- Integration process supervision should be introduced only when concrete
  lifecycle states or restart policy exceed the clarity of the current
  serialized registry implementation.
