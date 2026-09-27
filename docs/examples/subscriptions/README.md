# Subscription examples

These examples show the two supported Planner management paths:

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
