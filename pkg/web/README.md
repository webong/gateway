# Webong Web

Gateway Extension for HTTP webhooks, WebSocket, and Webstream (SSE).

The Composer entrypoint contributes Planner policy, management APIs, and event
history. The Go `spinner` package owns the public Web transport runtime. Both
entrypoints are released as one Extension identity and version.

WebSocket admission and duration plus Webstream heartbeat, retry, admission,
duration, and private-network ceiling are fields of the revisioned
`webong/web` runtime configuration. Generic hosts mount the Extension's HTTP
surface and do not contain Web-specific code or environment settings.
