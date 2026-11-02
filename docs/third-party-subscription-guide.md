# Connect a third-party application to Gateway

This walkthrough shows how a Gateway operator connects an application that
receives webhook deliveries. It covers endpoint setup, subscription creation,
a test event, delivery checks, and the current credential limits.

A subscription tells Gateway where to send matching events. An endpoint
identifies the incoming route those events belong to.

~~~text
event sender -> Gateway endpoint -> matching subscription
                                   -> application's HTTPS callback
~~~

## Decide who manages the subscription

Gateway has two management credentials:

- <code>REGISTRY_TOKEN</code> belongs to the Gateway operator. It can manage all
  endpoints, accounts, and route grants. Keep it in the operator's secret
  manager.
- An account token belongs to one client account. It can create, read, update,
  pause, and remove that account's HTTP relay subscriptions on routes the
  operator granted to it. It cannot create endpoints or grant access.

The account token is issued by the operator through <code>POST /accounts</code>.
On a shared Gateway, the operator also grants the account a particular route.
For example, after the endpoint exists, this grants account
<code>acme-orders-production</code> access to the bundled Router's
<code>path</code> route <code>/hooks/orders</code>. Run operator commands only
from a trusted server with <code>REGISTRY_TOKEN</code> loaded securely:

~~~sh
curl --fail-with-body --silent --show-error --include \
  -X POST "$GATEWAY_URL/accounts" \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"id":"acme-orders-production"}'

curl --fail-with-body --silent --show-error --include \
  -X PUT "$GATEWAY_URL/registry/endpoints/orders/accounts/acme-orders-production" \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{"routing_scope":"path","routing_key":"/hooks/orders"}'
~~~

Give the client its account ID, account token, endpoint key, ingress URL, and
the exact route scope and key. The token is returned once. An account has one
token; the operator can rotate it with
<code>POST /accounts/{accountId}/token/rotate</code>, which immediately
invalidates the old token. Some other account-owned Gateway APIs also accept
this account credential, so protect it as an account-wide service secret.

To withdraw a route grant, the operator sends the same route pair to
<code>DELETE /registry/endpoints/{endpointKey}/accounts/{accountId}</code>.
This blocks further management by that account on the route. Existing active
subscriptions continue delivering until the operator pauses or removes them.

If you operate a Gateway just for your application, keep the operator token
and account token in trusted server-side secret storage. If you use someone
else's Gateway, ask its operator to provision your account and route. Do not
ask them for <code>REGISTRY_TOKEN</code>.

Never put either token in browser code or send it with incoming event requests.
Neither token authenticates a webhook delivery.

## Gather the setup details

The client-side commands below assume <code>GATEWAY_URL</code> is the public
Gateway origin and <code>ACCOUNT_TOKEN</code> is loaded from the client's secret
manager:

~~~sh
export GATEWAY_URL=https://gateway.example.com
# Load ACCOUNT_TOKEN from the account's secret manager.
~~~

Copy <code>ENDPOINT_KEY</code> from the operator. After creating the
subscription, copy its returned <code>id</code> into
<code>DESTINATION_ID</code>; this is distinct from your
<code>subscription_id</code>. Save the response <code>ETag</code> for later
changes.

Before registering, agree on these values with the Gateway operator:

- The Gateway HTTPS base URL.
- The endpoint key and public ingress URL that event senders will call.
- Your application's public HTTPS callback URL.
- Your stable account ID and the subscription ID you want to use.
- The route scope and key used by that endpoint.
- Which events your app should receive, and the fields used to identify them.

For the bundled Gateway Router, the default ingress path is
<code>/hooks/{endpoint_key}</code>. For example, the endpoint key <code>orders</code>
uses <code>https://gateway.example.com/hooks/orders</code>. A separately
integrated Gateway may use an application-defined path. The endpoint and
subscription must agree on the same route scope and key.

## 1. Create or obtain an endpoint

If the Gateway operator has already created an endpoint, use the exact
<code>endpoint_key</code> they provide and skip this step.

On a Gateway you own, create an endpoint from a trusted server. This example
creates the endpoint used below:

~~~sh
curl --fail-with-body --silent --show-error --include \
  -X POST "$GATEWAY_URL/registry/endpoints" \
  -H "Authorization: Bearer $REGISTRY_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{
    "client":"acme-orders",
    "external_id":"store-42",
    "endpoint_key":"orders",
    "managed":true,
    "metadata":{"application":"orders"}
  }'
~~~

Save the returned <code>endpoint_key</code>. Use the returned value in later
requests; registries may resolve a requested key to a different key.

Creating an endpoint does not create a subscription or configure the event
sender. The Gateway operator must also make sure incoming traffic for the
chosen public path resolves to this endpoint.

## 2. Register your application as a subscriber

Create one subscription for each callback destination or event group. Replace
the example domain and match values with your application's real values.

~~~sh
curl --fail-with-body --silent --show-error --include \
  -X POST "$GATEWAY_URL/registry/endpoints/$ENDPOINT_KEY/subscriptions" \
  -H "Authorization: Bearer $ACCOUNT_TOKEN" \
  -H 'Content-Type: application/json' \
  --data '{
    "subscription_id":"store-42-order-created-v1",
    "type":"relay",
    "webhook_group":"orders",
    "routing_scope":"path",
    "routing_key":"/hooks/orders",
    "url":"https://app.example.com/webhooks/gateway/orders",
    "metadata":{"environment":"production"},
    "match":{
      "version":"v1",
      "rules":{
        "headers.x-event-type":["required","in:order.created"],
        "body.store_id":["required","in:store-42"]
      }
    }
  }'
~~~

The response contains both <code>id</code> and <code>subscription_id</code>.
<code>id</code> is Gateway’s destination ID; use it in the management URL path.
<code>subscription_id</code> is your stable logical ID. Save the response
<code>ETag</code> header too; updates and removal require it. A successful
registration means the subscription was saved, not that a callback was delivered.

In this example, <code>routing_scope</code> and <code>routing_key</code> select
the route, while <code>match</code> filters events on that route. For the
bundled Router, use scope <code>path</code> and the exact path as the key. In
another deployment, ask the operator for the values used by its route mapping.
The subscription API does not create or change that mapping.

Omitting <code>match</code> means the subscription matches every event on its
route. Match rules use Gateway's versioned JSON DSL; see the
[Subscription JSON DSL reference](subscription-dsl-v1.md) for field names,
operators, and updates.

## 3. Point the event sender at Gateway

Configure the provider or source application to send events to the Gateway
ingress URL. For the endpoint in this example:

~~~sh
curl --fail-with-body --silent --show-error --include \
  -X POST "$GATEWAY_URL/hooks/orders" \
  -H 'Content-Type: application/json' \
  -H 'X-Event-Type: order.created' \
  --data '{
    "store_id":"store-42",
    "order":{"id":"ord-123"}
  }'
~~~

The request body and header match the subscription above. If the event does not
match, Gateway can accept the ingress request while skipping this subscriber.
Check the receiver rather than treating the ingress response as proof of
delivery.

The registry token is not sent by the event sender. The public endpoint key
and URL are route identifiers, not proof of sender identity. Before production,
confirm how the incoming source is authenticated by your Gateway deployment;
do not assume that knowing the endpoint URL authenticates the sender.

## 4. Make the callback safe to retry

Gateway may retry a failed delivery, so the receiver should handle duplicates.
Use <code>X-Webhook-Forwarder-Delivery-Id</code> as an idempotency or
correlation key when it is present. It is not a cryptographic signature and
does not authenticate Gateway to your callback.

The v1 subscription record does not define a per-subscriber callback secret,
authorization header, or signing key. Gateway therefore does not provide an
application-level credential your callback can use to prove a delivery came
from Gateway. Do not treat the delivery ID or an obscure URL as authentication.
If the callback handles sensitive actions, put an authenticated intermediary or
other independently configured access control in front of it before enabling
production traffic.

Return a success response only after the callback has accepted the event. If
processing takes longer, persist or enqueue the event before returning success.
See [delivery operations](deliveries.md) for retries and at-least-once behavior.

## 5. Verify and maintain the subscription

Use the destination ID from the creation response to read the saved resource:

~~~sh
curl --fail-with-body --silent --show-error --include \
  "$GATEWAY_URL/registry/endpoints/$ENDPOINT_KEY/subscriptions/$DESTINATION_ID" \
  -H "Authorization: Bearer $ACCOUNT_TOKEN"
~~~

Check that its status is active, its URL is correct, and its match rules are
the ones you intended. Send a test event, then confirm your callback received
it. For delivery state, use the management API's
[delivery status and retry guide](deliveries.md).

Use the current <code>ETag</code> response header as <code>If-Match</code>. A
successful mutation returns a new ETag; save it before the next change. To pause
or resume a subscription, send <code>{"status":"paused"}</code> or
<code>{"status":"active"}</code> to
<code>PATCH /registry/endpoints/{endpointKey}/subscriptions/{id}/status</code>.
To change its callback URL, for example:

~~~sh
curl --fail-with-body --silent --show-error --include \
  -X PATCH "$GATEWAY_URL/registry/endpoints/$ENDPOINT_KEY/subscriptions/$DESTINATION_ID" \
  -H "Authorization: Bearer $ACCOUNT_TOKEN" \
  -H "If-Match: $SUBSCRIPTION_ETAG" \
  -H 'Content-Type: application/json' \
  --data '{"url":"https://app.example.com/webhooks/gateway/orders-v2"}'
~~~

To remove it, use the latest ETag in the same way:

~~~sh
curl --fail-with-body --silent --show-error --include \
  -X DELETE "$GATEWAY_URL/registry/endpoints/$ENDPOINT_KEY/subscriptions/$DESTINATION_ID" \
  -H "Authorization: Bearer $ACCOUNT_TOKEN" \
  -H "If-Match: $SUBSCRIPTION_ETAG"
~~~

See the [full subscription API reference](subscription-dsl-v1.md) for lifecycle
requests and incremental match updates.

## Security and network requirements

- Keep <code>REGISTRY_TOKEN</code> on the operator backend. Client backends use
  their account token; neither credential belongs in browser code.
- Use HTTPS for the Gateway API and your callback.
- Callback URLs must be reachable from Gateway. Private, loopback, and
  link-local callback addresses are blocked.
- Do not treat the endpoint key, the management token, or the delivery ID as
  interchangeable credentials.
- Keep callback handling idempotent because retries can repeat an event.

For copyable request files and a shell example, see
[subscription examples](examples/subscriptions/README.md).
