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

## Endpoint and authentication

```http
POST /registry/endpoints/{endpointKey}/subscriptions
Authorization: Bearer {REGISTRY_TOKEN}
Content-Type: application/json
```

Use the resolved `endpoint_key` returned by `POST /registry/endpoints`, not the
requested key: `webong/web-proxy` may suffix unmanaged endpoint keys.

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
| `webhook_group` | yes | non-empty string, max 255 | Group used by `webong/web-proxy` to manage the destination. |
| `routing_scope` | yes | non-empty string, max 255 | Route namespace used during destination selection. |
| `routing_key` | yes | non-empty string, max 255 | Key inside `routing_scope`. |
| `url` | yes | absolute HTTP(S) URL | Go sends matching ingress traffic to this destination. |
| `channel` | no | string, max 255, or `null` | Selects a configured `web-proxy` channel. The default channel is used when omitted or `null`. |
| `metadata` | no | JSON object | Subscriber metadata. Keys beginning `_`, plus `subscriber_id` and `delivery_mode`, are reserved. |
| `match` | no | match object | Versioned request-selection rules. Omission means match every request. |

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
- An empty `rules` object matches every request.
- Every field entry must pass. Rules for a field use Laravel validation
  semantics and run in their listed order.
- A failed match skips only that subscription. It does not reject the provider
  webhook or prevent other subscriptions from matching.
- Malformed persisted rules fail closed for the affected subscription.
- The matching document includes `protocol`, `event`, `session_id`, `method`,
  `scheme`, `host`, `path`, `headers`, `query`, `body`, and `attributes`.
  Protocol adapters expose bounded scalar metadata below `attributes`, such as
  `attributes.qtype` and `attributes.data` for authoritative DNS queries.

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

`web-proxy` exposes provider-neutral destination selection by route, so the
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

### Update URL, metadata, or delivery type

```http
PATCH /registry/endpoints/{endpointKey}/subscriptions/{destinationId}
Authorization: Bearer {REGISTRY_TOKEN}
If-Match: "{revision}"
Content-Type: application/json

{
  "url": "https://subscriber.example.test/webhooks/meta-v2",
  "type": "relay",
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
`web-proxy` providers. PHP excludes both states from route plans.

Remove a subscription with its current revision:

```http
DELETE /registry/endpoints/{endpointKey}/subscriptions/{destinationId}
Authorization: Bearer {REGISTRY_TOKEN}
If-Match: "{revision}"
```

Removal returns `204` and a new ETag. It is recoverable through the status
endpoint. This avoids assuming that every custom `web-proxy` provider supports
hard deletion or inactive-record lookup.

## Matching document

Rules are evaluated against this PHP-owned normalized document:

```json
{
  "method": "POST",
  "scheme": "https",
  "host": "relay.example.test",
  "path": "/provider/events/app-123",
  "headers": {
    "x-event-type": "message.created",
    "x-tag": ["one", "two"]
  },
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
| `method` | Uppercase HTTP method received by Go. |
| `scheme` | Ingress scheme, normally `http` or `https`. |
| `host` | Original ingress host. |
| `path` | Exact public request path. |
| `headers` | Header names are lowercase. One value is a string; repeated values are an array. |
| `query` | Parsed from the raw query string using PHP query-string semantics. |
| `body` | JSON is decoded once into PHP values. An empty body becomes an empty array; non-JSON input remains a string. |

Valid rule field names are:

- the complete top-level fields `method`, `scheme`, `host`, `path`, `headers`,
  `query`, and `body`; or
- nested paths beginning `headers.`, `query.`, or `body.`.

Nested path segments may contain ASCII letters, digits, `_`, `-`, and `*`.
Empty segments and `..` are rejected. Header paths are normalized to lowercase.
Laravel wildcard paths are supported, for example `body.items.*.type`.

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

The PHP DSL and HTTP JSON compile to the same `webong/web-proxy` destination
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
