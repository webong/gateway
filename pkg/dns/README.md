# Webong DNS

Gateway Extension for authoritative DNS and DNS hooks.

The Composer entrypoint contributes Planner zones, policy, and management APIs.
The Go `spinner` package owns UDP/TCP DNS serving. Its installable
`cmd/spinner` process receives listener limits from Planner's revisioned
`/extensions` resource. The DNS Planner entrypoint projects active `/dns/zones`
resources into the same integrity-bound runtime snapshot, so Spinner core does
not import DNS, parse DNS environment variables, or maintain a second zone
catalog. Updating either source replaces the process with the new validated
snapshot. Both entrypoints are released as one Extension identity and version.
