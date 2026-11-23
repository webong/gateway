# Subscription examples

For the full endpoint-to-callback setup, see the
[third-party application walkthrough](../../third-party-subscription-guide.md).

These examples show the two supported Planner management paths. The remote
examples use the Gateway operator token; third-party apps should use their
account token and an operator-granted route as described in the walkthrough.

- [`direct-php.php`](direct-php.php) uses the Laravel container directly. It
  creates an endpoint profile and then registers one direct subscription and
  one subscription that selects the profile.
- [`remote-direct.json`](remote-direct.json) is an HTTP JSON subscription that
  uses Spinner's default outbound network configuration.
- [`remote-profile.json`](remote-profile.json) is an HTTP JSON subscription
  that selects an existing endpoint profile.
- [`remote-http.sh`](remote-http.sh) sends both JSON examples and shows how to
  switch or remove a profile using the subscription's ETag.

## What `profile` means

The value is an endpoint-scoped name, not an embedded proxy configuration:

```json
{"profile": "proxy"}
```

Before that subscription can be created, the endpoint owner must have
registered a profile named `proxy`. Different endpoints may use the same name
for different configurations. Several subscriptions on one endpoint may also
share the same profile.

Omit `profile` to use Spinner's default HTTP transport. Set it to `null` in a
conditional subscription PATCH to remove an existing selection.

The remote subscription API deliberately does not accept proxy URLs or
credentials. It can only select an existing profile. Profile creation currently
uses the PHP `webong/proxy` API, as shown in `direct-php.php`.

## Running the remote example

The endpoint and its optional `proxy` profile must already exist. Then run:

```bash
REGISTRY_BASE_URL=https://relay.example.test \
REGISTRY_TOKEN=replace-me \
ENDPOINT_KEY=resolved-endpoint-key \
bash docs/examples/subscriptions/remote-http.sh
```

The script requires `curl`. Set `PROFILE_DESTINATION_ID` and `PROFILE_ETAG` to
also run its profile update examples. Use the `id` and `ETag` returned by the
profiled subscription creation response.


## Conditions: rules, jq and compositions

Existing `match: {"version":"v1","rules":{...}}` documents remain accepted.
Planner explicitly registers Workflow's Illuminate rules driver and jq driver.
`all`, `any` and `not` compose their leaves, for example:

```json
{
  "version": "v1",
  "all": [
    {"rules": {"headers.X-Event": "required|in:created"}},
    {"syntax": "jq", "expression": ".payload.account.id == \"account-42\""}
  ]
}
```

jq reads Gateway's normalized matching document directly: `protocol`, `event`,
`session_id`, `host`, `route`, normalized lowercase `headers`, decoded `payload`
and `attributes`. HTTP also supplies `method`, `scheme`, `path`, `query` and `body`.
There is no workflow `context`/`input` wrapper. A jq result must be one boolean.

Every rules leaf retains Gateway's allowed fields and bounded validation rules.
Header field names are normalized before storage. Any rules leaf referring to
HTTP-only fields makes the condition HTTP-only, including inside `not`; absent
protocol fields cannot be used to bypass that policy. Invalid stored conditions
and evaluation failures are nonmatches with redacted diagnostic codes.

`PATCH .../subscriptions/{id}/match` remains a rules-field patch. It rejects jq
and composed conditions. Replace a whole condition using `PUT` on the same URL,
with the new match document as its body and the current `If-Match` ETag. Replacement
uses the same owner/route permissions, revision checks and cache invalidation as
rule patches. `channel` remains an optional transport field outside the condition.
