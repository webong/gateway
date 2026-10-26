# Webong Email

Gateway Extension for SMTP, outbound mail, and IMAP/POP3 retrieval.

The Composer entrypoint contributes Planner accounts, domains, routes, policy,
and management APIs. The Go `spinner` package owns Email protocol handling and
mail-network enforcement. Both entrypoints are released as one Extension
identity and version.

The installable Spinner entrypoint owns SMTP, IMAP, and POP3 listener lifecycle.
Listener configuration comes from Planner's revisioned `/extensions` resource,
not `GATEWAY_SMTP_ADDR`, `GATEWAY_IMAP_ADDR`, or `GATEWAY_POP3_ADDR` on the
generic Spinner host.

Email declares an exact `gateway.dns/v1 exchange` dependency. Spinner therefore
requires an active compatible `webong/dns` package, starts it before Email, and
routes DNS calls only to that selected package revision. Email uses the
capability for SPF, DKIM, sender-domain existence, and managed IMAP/POP3
upstream address resolution. The dependency is an Arranger contract: Email does
not import the DNS implementation, and DNS may run resolver-only without a
public port-53 listener.

Outbound relay and failure-notice behavior are owned by this Extension as well.
Set `smtp_relay` and optional `failure_notice_domain` in the `webong/email`
runtime configuration. Email advertises its adapter and terminal-failure
handler after startup; the generic queue only invokes the selected provider and
durably persists any returned follow-up delivery.
