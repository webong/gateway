# Gateway Zorvia mailbox adapter

This optional Planner extension maps Gateway's provider-neutral mailbox grant
interface to Zorvia's connection setup callback. Install
`webong/gateway-zorvia-mailbox`, then register a credential source with adapter
`zorvia`, the absolute Zorvia-generated setup URL, and its connection-owned
`connections:setup` bearer token.

The adapter sends a bounded HTTPS `PATCH` request by default. Publish the
extension config and choose `PUT` when the Zorvia deployment requires it. The
body is `{"context":{"account_id":"...","connection_id":"...","protocol":"imap|smtp","address":"..."}}`.
Gateway's core validates the returned short-lived generic mailbox grant before
using it.
