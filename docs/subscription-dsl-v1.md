# Subscription JSON DSL v1

This document is the normative reference for registering a Gateway
subscription over HTTP. The machine-readable companion is
[`schemas/subscription-v1.schema.json`](schemas/subscription-v1.schema.json).
Incremental match updates use
[`schemas/subscription-match-patch-v1.schema.json`](schemas/subscription-match-patch-v1.schema.json).
Lifecycle updates use
[`schemas/subscription-update-v1.schema.json`](schemas/subscription-update-v1.schema.json)
and
[`schemas/subscription-status-v1.schema.json`](schemas/subscription-status-v1.schema.json).

Copy-pasteable direct PHP and remote HTTP examples live in
[`examples/subscriptions`](examples/subscriptions/README.md).

## Endpoint and authentication

```http
POST /registry/endpoints/{endpointKey}/subscriptions
Authorization: Bearer {REGISTRY_TOKEN}
Content-Type: application/json
```

Shell target creation and mutation additionally require Planner's
`GATEWAY_SHELL_SUBSCRIPTION_TOKEN` in the
`X-Gateway-Shell-Subscription` header. The registry token alone is not
operator authority to execute a local hook.

Use the resolved `endpoint_key` returned by `POST /registry/endpoints`, not the
requested key: `webong/proxy` may suffix unmanaged endpoint keys.

## Complete request

```json
{
  "subscriber_id": "workspace-42",
  "subscription_id": "workspace-42-messages",
  "type": "relay",
  "webhook_group": "meta",
  "routing_scope": "application",
  "routing_key": "app-123",
  "url": "https://subscriber.example.test/webhooks/meta",
  "profile": "proxy",
  "channel": null,
  "metadata": {
    "environment": "production"
  },
  "match": {
    "version": "v1",
    "rules": {
      "method": ["required", "in:POST"],
      "headers.x-event-type": ["required", "in:message.created,message.updated"],
      "query.source": ["nullable", "string", "in:provider"],
      "body.account.id": ["required", "string", "in:account-42"]
    }
  }
}
```

Unknown properties are not part of the v1 contract and must not be sent.

## Subscription properties

| Property | Required | Type and limit | Meaning |
| --- | --- | --- | --- |
| `subscriber_id` | yes | non-empty string, max 255 | Stable identity of the subscriber. |
| `subscription_id` | no | non-empty string, max 255 | Stable registration identity. Defaults to `subscriber_id`. |
| `type` | no | `relay` or `reply` | Defaults to `relay`. A route may have only one synchronous `reply` subscription. |
| `webhook_group` | yes | non-empty string, max 255 | Group used by `webong/proxy` to manage the destination. |
| `routing_scope` | yes | non-empty string, max 255 | Route namespace used during destination selection. |
| `routing_key` | yes | non-empty string, max 255 | Key inside `routing_scope`. |
| `target_kind` | no | `http` or `shell` | Defaults to `http`. Shell targets require the optional Shell extension and `type: reply`. |
| `url` | for HTTP | absolute HTTP(S) URL | Go sends matching ingress traffic to this destination. Not accepted for Shell targets. |
| `hook` | for Shell | non-hidden relative hook key, max 1024 | Operator-deployed local Shell hook selected by Planner. Not accepted for HTTP targets. |
| `profile` | HTTP only | non-empty string, max 64 | Name of an active endpoint-scoped `webong/proxy` profile used for outbound delivery. |
| `channel` | no | string, max 255, or `null` | Selects a configured `proxy` channel. The default channel is used when omitted or `null`. |
| `metadata` | no | JSON object | Subscriber metadata. Keys beginning `_`, plus `subscriber_id`, `delivery_mode`, and `profile`, are reserved. |
| `match` | no | match object | Versioned ingress-event selection rules. Omission means match every event. |

For an HTTP request that should be answered by an operator-deployed Shell hook,
use the same endpoint with a Shell reply target:

```json
{
  "subscriber_id": "workspace-42",
  "subscription_id": "workspace-42-deploy",
  "type": "reply",
  "target_kind": "shell",
  "hook": "github/deploy",
  "webhook_group": "meta",
  "routing_scope": "application",
  "routing_key": "app-123",
  "match": {"version": "v1", "rules": {"method": ["in:POST"]}}
}
```

Only one active reply—HTTP or Shell—may exist for the route. A Shell
subscription's hook is not uploaded by this request: it must already exist
under every eligible Spinner's configured Shell hook directory. Planner
matches the subscription and emits an automation plan; Spinner executes the
hook and returns its response to the original HTTP caller. The returned
resource contains `target_kind: shell` and `hook`, never the internal storage
locator. Updating `hook` uses the usual `PATCH` with `If-Match`; target kinds
cannot be changed in place. SSH credentials and remote execution are not part
of this local Shell reply contract. The operator must first allow the exact
hook under `gateway.shell.subscription_hooks[endpoint_key][subscriber_id]`;
the default empty map denies registration and execution. Removing an allowlist
entry blocks an existing subscription immediately, but does not automatically
remove its reply slot. The extra operator token is required when creating a
Shell subscription or changing its hook, match, status, metadata, or lifecycle;
read-only lookups use the registry token alone.

The profile itself is registered and stored by `webong/proxy` on the endpoint.
Subscriptions retain only its name. Planner resolves the active profile and
normalizes its HTTP proxy, no-proxy, connect-timeout, and request-timeout
settings into the Go delivery contract; Spinner performs the network request.
Proxy credentials therefore stay encrypted in the Proxy registry and are not
copied into subscription metadata or returned by the subscription API.

Register the profile on the endpoint before a PHP or remote HTTP subscriber
selects it:

```php
use Webong\WebProxy\ProfileDefinition;

$endpoint->registerProfile(new ProfileDefinition(
    name: 'proxy',
    configuration: [
        'proxy' => [
            'http' => 'http://proxy-user:proxy-pass@proxy.example.test:8080',
            'https' => 'socks5h://proxy.example.test:1080',
            'no' => ['.internal.example.test'],
        ],
        'options' => [
            'connect_timeout' => 5,
            'timeout' => 15,
        ],
    ],
));
```

Both the PHP `SubscriptionDefinition` DSL and the HTTP JSON DSL use the same
profile name. Registration fails closed when the profile is absent, inactive,
malformed, or uses a non-HTTP driver.

## Match object

`match` contains exactly two properties:

```json
{
  "version": "v1",
  "rules": {}
}
```

- `version` must be the exact string `v1`.
- `rules` is an object with at most 64 field entries.
- Each field name is at most 512 bytes.
- An empty `rules` object matches every ingress event.
- Every field entry must pass. Rules for a field use Laravel validation
  semantics and run in their listed order.
- A failed match skips only that subscription. It does not reject the provider
  webhook or prevent other subscriptions from matching.
- Malformed persisted rules fail closed for the affected subscription.
- HTTP-only fields never match a non-HTTP event, including when their rule is
  `nullable`; use `protocol` explicitly when a subscription should select only
  one protocol.
- Every matching document includes `protocol`, `event`, `session_id`, `host`,
  `route`, `headers`, `payload`, and `attributes`. Only HTTP events also include
  the compatibility fields `method`, `scheme`, `path`, `query`, and `body`.
  Protocol adapters expose bounded scalar metadata below `attributes`, such as
  `attributes.qtype` for authoritative DNS queries.

### Rule encodings

The canonical representation is an array containing 1 to 16 rule strings:

```json
{
  "body.account.id": ["required", "string", "in:account-42"]
}
```

Each rule string is non-empty and at most 1024 bytes. The HTTP API also accepts
Laravel-style pipe shorthand:

```json
{
  "body.account.id": "required|string|in:account-42"
}
```

Pipe shorthand is normalized to the canonical array in storage and responses.
Use arrays when a rule parameter itself needs unambiguous punctuation.
Parameterless rules reject a colon and arguments. `between` requires exactly
two bounded decimal values in ascending order; `min`, `max`, and `size` require
one bounded decimal value (at most 32 characters, no exponent); `in`, `not_in`,
`starts_with`, and `ends_with` require a non-empty value list. Malformed
parameters, associative rule lists, and field names that collide after
normalization are rejected during registration or patching. The JSON Schema
checks the basic shape; Planner is authoritative for these semantic constraints.

## Incremental match updates

Rules can be added or removed without resending the complete subscription or
match object. Use the destination `id` returned by subscription creation:

```http
PATCH /registry/endpoints/{endpointKey}/subscriptions/{destinationId}/match
Authorization: Bearer {REGISTRY_TOKEN}
If-Match: "{revision}"
Content-Type: application/json
```

```json
{
  "version": "v1",
  "operations": [
    {
      "op": "remove",
      "field": "body.account.id",
      "rules": ["in:account-42"]
    },
    {
      "op": "add",
      "field": "body.account.id",
      "rules": ["in:account-99"]
    },
    {
      "op": "remove",
      "field": "headers.x-old-event"
    }
  ]
}
```

Operations are ordered and the final result is validated before one metadata
update is written:

- `add` requires `rules`. It appends rules that are not already present and
  creates the field when necessary.
- `remove` with `rules` removes only exact normalized rule strings.
- `remove` without `rules` removes the complete field.
- Adding an existing rule, removing an absent rule, or removing an absent field
  is an idempotent no-op.
- A request contains 1 to 128 operations. The resulting match object must still
  satisfy the v1 limit of 64 fields and 16 rules per field.
- Field names and rule strings use the same normalization, allowlist, and
  security restrictions as full subscription registration.
- The optional `channel` property selects the registry channel exactly as it
  does during subscription creation.

The successful HTTP `200` response returns the complete normalized result:

```json
{
  "endpoint_key": "meta-messenger-a1b2c3d4e5f60718",
  "id": "01J...",
  "status": "active",
  "revision": "d41d8cd98f00b204e9800998ecf8427e",
  "match": {
    "version": "v1",
    "rules": {
      "body.account.id": ["required", "string", "in:account-99"]
    }
  }
}
```

The response includes `ETag: "d41d8cd98f00b204e9800998ecf8427e"`.
Use that exact value as `If-Match` on the next mutation. A stale value returns
`412` with the current ETag and does not write anything.

The equivalent fluent PHP API uses the same patch object:

```php
use Webong\Gateway\PatchSubscriptionMatch;
use Webong\Gateway\Protocol\MatchRulesPatch;

$patch = MatchRulesPatch::make()
    ->remove('body.account.id', 'in:account-42')
    ->add('body.account.id', 'in:account-99')
    ->remove('headers.x-old-event');

$subscription = app(PatchSubscriptionMatch::class)->handle(
    endpointKey: $endpointKey,
    destinationId: $destinationId,
    patch: $patch,
    expectedRevision: $revision,
);
```

Conditional mutations are serialized with a route-scoped atomic cache lock.
Use a shared lock store such as Redis when multiple workers or application
instances are running. Together, the lock and `If-Match` check prevent two
callers that read the same revision from silently overwriting one another.

## Subscription lifecycle API

All lifecycle routes use the same bearer token as subscription creation.

### List a route

`proxy` exposes provider-neutral destination selection by route, so the
collection API is deliberately route-scoped:

```http
GET /registry/endpoints/{endpointKey}/subscriptions?routing_scope=application&routing_key=app-123
Authorization: Bearer {REGISTRY_TOKEN}
```

The response is `{"data": [...]}`. Active and paused subscriptions are
included. Removed subscriptions are omitted unless `include_removed=true` is
provided. The optional `channel` query parameter selects the registry channel.

### Inspect a subscription

```http
GET /registry/endpoints/{endpointKey}/subscriptions/{destinationId}
Authorization: Bearer {REGISTRY_TOKEN}
```

The response includes the complete normalized subscription and its `ETag`.
Inspection continues to work after removal so a subscriber can be reactivated.

### Update URL, metadata, delivery type, or network profile

```http
PATCH /registry/endpoints/{endpointKey}/subscriptions/{destinationId}
Authorization: Bearer {REGISTRY_TOKEN}
If-Match: "{revision}"
Content-Type: application/json

{
  "url": "https://subscriber.example.test/webhooks/meta-v2",
  "type": "relay",
  "profile": "proxy",
  "metadata": {
    "environment": "production",
    "obsolete_key": null
  }
}
```

The request follows
[`schemas/subscription-update-v1.schema.json`](schemas/subscription-update-v1.schema.json).
Metadata is patched by key; a `null` value removes that public metadata key.
Reserved internal keys cannot be changed. Switching to `reply` is rejected if
another active synchronous reply already owns the route.
Set `profile` to another active endpoint profile to change the outbound network
configuration, or to `null` to return to Spinner's default HTTP client.

### Pause, reactivate, or remove

Pause or reactivate without resending the subscription:

```http
PATCH /registry/endpoints/{endpointKey}/subscriptions/{destinationId}/status
Authorization: Bearer {REGISTRY_TOKEN}
If-Match: "{revision}"
Content-Type: application/json

{"status": "paused"}
```

Use `{"status":"active"}` to reactivate either a paused or removed
subscription. Pause and removal are Gateway lifecycle states stored in
reserved destination metadata, which keeps the behavior portable across
`proxy` providers. PHP excludes both states from route plans.

Remove a subscription with its current revision:

```http
DELETE /registry/endpoints/{endpointKey}/subscriptions/{destinationId}
Authorization: Bearer {REGISTRY_TOKEN}
If-Match: "{revision}"
```

Removal returns `204` and a new ETag. It is recoverable through the status
endpoint. This avoids assuming that every custom `proxy` provider supports
hard deletion or inactive-record lookup.

## Matching document

Rules are evaluated against a PHP-owned normalized gateway event. For an HTTP
request, the document is:

```json
{
  "protocol": "http",
  "event": "request",
  "session_id": "",
  "host": "relay.example.test",
  "route": "/provider/events/app-123",
  "headers": {
    "x-event-type": "message.created",
    "x-tag": ["one", "two"]
  },
  "payload": {
    "account": {
      "id": "account-42"
    }
  },
  "attributes": {},
  "method": "POST",
  "scheme": "https",
  "path": "/provider/events/app-123",
  "query": {
    "source": "provider"
  },
  "body": {
    "account": {
      "id": "account-42"
    }
  }
}
```

| Field | Normalization |
| --- | --- |
| `protocol` | Ingress protocol, for example `http`, `smtp`, `websocket`, or `dns`. |
| `event` | Protocol event kind, such as `request`, `transaction`, `authenticate`, `connect`, `message`, `close`, or `query`. |
| `session_id` | Session identifier when the protocol has one; otherwise an empty string. |
| `host` | Ingress host when supplied by the adapter; otherwise an empty string. |
| `route` | Protocol-specific routing key: HTTP/WebSocket path, SMTP recipient or authentication username, or DNS hook path. |
| `headers` | Header names are lowercase, and differently cased names are merged. One value is a string; repeated values are an array. |
| `payload` | JSON is decoded once into PHP values. An empty payload becomes an empty array; non-JSON input remains a string. |
| `attributes` | Protocol-specific scalar metadata supplied by the ingress adapter. |
| `method`, `scheme`, `path`, `query`, `body` | HTTP-only compatibility fields. `query` uses PHP query-string parsing; `body` is the same decoded value as `payload`. These keys are absent for SMTP, DNS, and WebSocket events, even when the planner's internal adapter uses HTTP-shaped transport fields. |

For example, a DNS subscription can select a TXT query with:

```json
{
  "protocol": ["in:dns"],
  "event": ["in:query"],
  "route": ["in:/my-hook"],
  "attributes.qtype": ["in:TXT"],
  "payload.data": ["required", "string"]
}
```

An SMTP subscription can match `event: transaction`, a recipient in `route`,
the sender in `attributes.mail_from`, and raw message text in `payload`. A
WebSocket subscription can match `event: message`, its `session_id` and `route`,
`attributes.message_type`, and a JSON message field such as `payload.kind`.
The WebSocket upgrade's HTTP method and query are not exposed as HTTP match
fields for message events.

Valid rule field names are:

- the complete top-level fields `protocol`, `event`, `session_id`, `host`,
  `route`, `headers`, `payload`, `attributes`, `method`, `scheme`, `path`,
  `query`, and `body`; or
- nested paths beginning `headers.`, `payload.`, `attributes.`, `query.`, or
  `body.`.

Nested path segments may contain ASCII letters, digits, `_`, `-`, and `*`.
Empty segments and `..` are rejected. Header paths are normalized to lowercase.
Laravel wildcard paths are supported, for example `payload.items.*.type`.
As with Laravel validation, a wildcard alone does not require an array to have
elements. To require at least one matching item, also constrain the parent,
for example `payload.items: ["required", "array", "min:1"]`.

## Allowed rules

Only the following rules are accepted. Rule names are shown in canonical
lowercase form.

| Rule | Typical form | Purpose |
| --- | --- | --- |
| `accepted` | `accepted` | Value must be an accepted boolean-like value. |
| `array` | `array` | Value must be an array. |
| `bail` | `bail` | Stop validating the field after its first failure. |
| `between` | `between:min,max` | Size or numeric value must be within the range. |
| `boolean` | `boolean` | Value must be boolean-like. |
| `declined` | `declined` | Value must be a declined boolean-like value. |
| `ends_with` | `ends_with:value,...` | String must end with one of the values. |
| `filled` | `filled` | Present value must not be empty. |
| `in` | `in:value,...` | Value must be one of the listed values. |
| `integer` | `integer` | Value must be an integer. |
| `list` | `list` | Array keys must form a consecutive list. |
| `max` | `max:value` | Value must not exceed the maximum. |
| `min` | `min:value` | Value must meet the minimum. |
| `not_in` | `not_in:value,...` | Value must not be one of the listed values. |
| `nullable` | `nullable` | `null` is allowed and later non-implicit rules are skipped. |
| `numeric` | `numeric` | Value must be numeric. |
| `present` | `present` | Field must exist, even when empty. |
| `required` | `required` | Field must exist and not be empty. |
| `size` | `size:value` | Value must have the specified size. |
| `starts_with` | `starts_with:value,...` | String must start with one of the values. |
| `string` | `string` | Value must be a string. |

Database, filesystem, network, regular-expression, custom class, closure, and
other unlisted Laravel rules are rejected. This prevents a remote subscriber
from causing application-owned I/O or code execution during matching.

## Successful response

The API returns HTTP `201` and the normalized canonical match object:

```json
{
  "id": "01J...",
  "endpoint_key": "meta-messenger-a1b2c3d4e5f60718",
  "subscriber_id": "workspace-42",
  "subscription_id": "workspace-42-messages",
  "type": "relay",
  "url": "https://subscriber.example.test/webhooks/meta",
  "status": "active",
  "revision": "d41d8cd98f00b204e9800998ecf8427e",
  "match": {
    "version": "v1",
    "rules": {
      "headers.x-event-type": ["required", "in:message.created,message.updated"],
      "body.account.id": ["required", "string", "in:account-42"]
    }
  }
}
```

## Errors

| Status | Meaning |
| --- | --- |
| `401` | Bearer token is absent or does not match `REGISTRY_TOKEN`. |
| `404` | The resolved endpoint key is not registered. |
| `412` | `If-Match` is stale; the response carries the current revision and ETag. |
| `422` | Subscription fields, match shape, field path, rule, URL, metadata, or reply uniqueness are invalid. |
| `428` | A mutation omitted a valid quoted `If-Match` revision. |
| `503` | The registry API is not configured or the mutation lock cannot be acquired. |

Validation failures use this shape:

```json
{
  "message": "The subscription is invalid.",
  "errors": {
    "match": ["Match rule [exists] is not allowed."]
  }
}
```

## PHP equivalent

The PHP DSL and HTTP JSON compile to the same `webong/proxy` destination
metadata. This PHP definition is equivalent to the request above:

```php
$definition = SubscriptionDefinition::matching(
    subscriberId: 'workspace-42',
    subscriptionId: 'workspace-42-messages',
    webhookGroup: 'meta',
    routingScope: 'application',
    routingKey: 'app-123',
    url: 'https://subscriber.example.test/webhooks/meta',
    type: 'relay',
    rules: [
        'headers.x-event-type' => ['required', 'in:message.created,message.updated'],
        'body.account.id' => ['required', 'string', 'in:account-42'],
    ],
);
```
