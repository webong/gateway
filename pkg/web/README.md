# Webong Web

Gateway Extension for Webhook, WebSocket, and Webstream (SSE).

The Composer entrypoint contributes Planner policy, management APIs, and event
history. The Go `spinner` package owns WebSocket and Webstream transport. Spinner
core owns the HTTP edge. Both
entrypoints are released as one Extension identity and version.

WebSocket admission and duration plus Webstream heartbeat, retry, admission,
duration, and private-network ceiling are fields of the revisioned
`webong/web` runtime configuration. Generic hosts select the Extension only
for WebSocket upgrades and SSE requests; ordinary HTTP continues through the
built-in edge when Web is absent. Hosts do not contain Web-specific runtime
code or environment settings.
