---
status: accepted
---

# Support managed, provided, and forwarded DNS without coupling observation to answers

Gateway DNS supports three answer modes. **Managed DNS** is answered directly from Planner-managed records. **Provided DNS** keeps Gateway authoritative while a normal synchronous Webhook, Automation, or Integration supplies the answer document. **Forwarded DNS** proxies a policy-scoped query to an upstream resolver that owns recursion, caching, and DNSSEC validation. Logging, analytics, webhook notifications, event history, and other observers remain separate from all answer paths and must not delay or alter the DNS response. This broadens Gateway's DNS surface while adopting the useful query-and-response observation pipeline demonstrated by systems such as `fantasticmao/dnshooks`.

## Decision

- Managed zones take precedence. Their answer path validates one scoped authoritative query and first resolves exact Planner-managed records.
- A dynamic in-zone query uses Provided DNS: Planner selects exactly one ordinary synchronous reply action. HTTP Webhooks and installed Automations or Integrations receive the normalized query and must return the same bounded DNS answer document within the DNS deadline. Spinner validates and encodes that document, and fails closed when the action is missing, incompatible, unavailable, or malformed.
- When no managed zone matches, Spinner may select a Planner-managed Forwarded DNS policy by longest domain suffix and permitted client network. It forwards the original query to the policy's upstream resolver with a strict deadline, falls back from UDP to TCP only for a truncated upstream response, and returns the resolver's response without claiming authority.
- A Forwarded DNS policy requires an explicit upstream, domain scope, and client-network scope. An unmatched or unauthorized query is refused, so enabling the feature does not implicitly create an open resolver. The upstream resolver—not Gateway—owns recursive behavior, caching, DNSSEC validation, filtering, and upstream failover.
- After an answer outcome is known, Spinner emits an immutable, versioned DNS observation containing the event ID, normalized question, source, transport, selected zone and hook, Planner decision, response code, bounded answer summary, timing, and failure classification. Extension contracts use this language-neutral document rather than exposing `miekg/dns` objects or raw in-process hooks.
- Observation is non-blocking with respect to the DNS client. A bounded host queue accepts observations; saturation drops or coalesces observational work and records metrics instead of holding the response open. Destinations requiring retries or durable delivery receive the observation through Gateway's existing delivery machinery after it has been accepted by that queue.
- Observers cannot change the DNS response. Behavior that must generate an answer is declared separately as an answer-producing reply and remains subject to the DNS deadline, response schema, record limits, integrity checks, and fail-closed behavior.
- Gateway Extensions may contribute observers through Arranger Integration or Automation contracts. Spinner does not load arbitrary Go plugins, and DNS does not introduce a second extension lifecycle alongside Gateway's verified package model.
- The normalized observation may describe the effective question and response but does not persist unrestricted raw packets by default. Payload size, retention, access control, and potentially sensitive query-name data remain governed by Planner policy.

## Considered options

- Implement recursion, caching, and DNSSEC validation inside Gateway. Rejected because these belong to the upstream resolver and would turn the DNS Extension into a full recursive resolver rather than a policy-enforcing Gateway.
- Forward every unmatched query without client and domain policy. Rejected because a public Spinner listener would become an open resolver and amplification surface.
- Run every observer inline before returning the DNS response. Rejected because a slow analytics store, webhook, or installed extension would add DNS latency and could turn an optional observer into an availability dependency.
- Expose raw Go DNS message types as the observer contract. Rejected because it couples Extensions to Spinner's implementation library and process, prevents language-neutral out-of-process packages, and makes compatibility and payload bounds difficult to enforce.
- Make every hook asynchronous. Rejected because dynamic authoritative answers sometimes require application logic; these are explicit synchronous replies, not observers.

## Consequences

- Static records and NS/SOA authority retain their present semantics. Provided answers use the shared reply and Automation Interfaces instead of a DNS-specific callback Interface.
- Forwarded DNS adds Planner-owned resolver desired state and one bounded forwarding implementation; it does not add a DNS-specific package lifecycle.
- Query history and usage may become eventually consistent, while answer correctness remains synchronous.
- Observer overload is visible through queue-depth, accepted, dropped, delivery-failure, and end-to-end latency metrics without degrading DNS response latency.
- The observation contract requires regression coverage for UDP and TCP, success and failure response codes, synchronous reply deadlines, observer saturation, ordering by event ID, bounded payloads, redaction policy, and the guarantee that observer failure cannot change an already selected answer.
- Applications may opt into arbitrary-domain forwarding with the root domain scope, but only for explicitly permitted client networks. Gateway still delegates recursive correctness to the selected forwarded resolver.

## Implementation status

- [x] Execute Provided DNS through the shared synchronous Webhook and Automation contracts.
- [x] Advertise runtime Automation languages and fail closed on missing or incompatible capabilities.
- [x] Store versioned Forwarded DNS policies in Planner and project active policies into the DNS Extension runtime snapshot.
- [x] Select forwarded resolvers by managed-zone precedence, longest domain suffix, and most-specific permitted client network.
- [x] Forward over bounded UDP or TCP and retry a truncated UDP response over TCP.
- [x] Refuse unmatched clients and reject unspecified catch-all client networks.
- [x] Emit the versioned post-answer observation through a bounded non-blocking host queue.
- [x] Route shared event history and usage through the asynchronous observation path, with a fan-out registry for additional analytics and notification recorders.
