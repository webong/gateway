# Webong DNS

Gateway Extension for authoritative DNS and DNS hooks.

The Composer entrypoint contributes Planner zones, policy, and management APIs.
The Go `spinner` package owns UDP/TCP DNS serving and live zone reconciliation.
Both entrypoints are released as one Extension identity and version.
