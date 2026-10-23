---
status: proposed
---

# Defer cross-node Ergo Automation execution until durable execution semantics exist

Ergo Automation actors remain local to each Spinner Node with Ergo networking disabled. Cross-node execution is a future option for capability-aware load distribution, but network acknowledgment only proves that a request reached a remote mailbox; it does not prove that an Automation completed exactly once. Enabling it before Gateway has durable execution identities, ownership leases, deduplication, terminal-result lookup, cancellation propagation, payload limits, and idempotent delivery handling would create ambiguous outcomes and duplicate side effects when a node or connection fails.

## Considered options

- Keep local actor pools. Each ingress request or claimed Schedule run executes on the Spinner Node that received or claimed it. Planner's existing claim leases continue to coordinate scheduled work across nodes.
- Enable Ergo networking immediately and retry remote calls on failure. This is rejected because a lost response cannot distinguish work that never started from work that completed before the connection failed.
- Introduce cross-node execution behind a durable coordination protocol. This remains the proposed future direction when distribution is justified by measured capacity or placement needs.

## Preconditions for reconsideration

Gateway should reconsider cross-node execution only after it has:

- mutually authenticated Spinner Node membership, stable identities, certificate rotation, and remote spawning disabled;
- capability and load discovery so work is sent only to a node that has the required Automation engine and available capacity;
- a versioned, size-bounded execution and result contract that works during rolling deployments;
- globally unique execution IDs, durable ownership leases, deduplication, and queryable terminal results;
- end-to-end deadlines and cancellation propagation from the originating request or Schedule run;
- explicit `outcome_unknown` handling and idempotency keys for every external side effect;
- metrics, tracing, draining, mixed-version tests, and failure tests covering lost requests, lost responses, node death, and network partitions.

## Rollout path

Start with authenticated two-node health and capability exchange, then distribute side-effect-free Automations. Scheduled Automations are the first state-changing candidate because their run IDs, claims, leases, and completion records already provide part of the required recovery model. Synchronous ingress should move last, after remote cancellation and ambiguous-result recovery are proven. Until then, actor pools provide bounded concurrency, waiting, supervision, and observability inside one Spinner process without changing Gateway's distributed execution semantics.
