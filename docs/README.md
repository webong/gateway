# Gateway

Gateway connects application traffic to services and is configured through an
HTTPS management API. The combined Docker distribution is the simplest way to
run it. There is no admin dashboard in the current release.

## Start here

- [Set up and use Gateway](using-gateway.md): start Gateway, get the management
  token, receive a Webhook, and choose another capability.
- [Gateway Router deployment](router.md): container and native installation.
- [Separate-service deployment](deployment.md): connect Gateway to an existing
  application or Laravel host.

## Set up a feature

| Feature | What you can do |
| --- | --- |
| Web | Receive HTTP Webhooks, proxy WebSocket connections, and relay Webstream (SSE). See [Web setup](using-gateway.md#choose-what-you-want-gateway-to-do). |
| DNS | Host authoritative zones, provide application-generated answers, or forward selected queries. See [DNS setup](using-gateway.md#dns) and the [public delegation checklist](dns-ha.md). |
| Email | Receive or relay SMTP, send mail from applications, and connect to existing IMAP/POP3 services. See [Email setup](using-gateway.md#email) and the [authentication checklist](email-authentication.md). |
| Automations | Run small event-driven scripts and scheduled work with installed languages. See [Automation setup](using-gateway.md#automations). |
| Managed realtime | Provision and route Reverb, Mercure, or Centrifugo workloads. See [managed deployment targets](provisioning-targets.md). |
| Database | Reserved; a runnable Database gateway is not currently shipped. |

Email provides mail transport and routing, not mailbox storage or webmail.

## Operate Gateway

- [Subscriptions and matching](subscription-dsl-v1.md)
- [Example management API requests](examples/subscriptions/README.md)
- [Endpoint event history](events.md)
- [Delivery status and retry behavior](deliveries.md)
- [Extension release and activation](planner-extensions.md#github-release-artifacts)
- [Kubernetes deployment](kubernetes.md)

## Additional references

- [DNS resource and answer behavior](../pkg/dns/README.md)
- [Email receiving and forwarding flows](mail-forwarding.md#application-inboxes-primary-receiving-flow)
- [Email application hooks](mail-forwarding.md#mx-mail-hooks)
- [Connect an existing mailbox](mail-forwarding.md#customer-mailbox-connections)
- [Automation capabilities, languages, and schedules](automations.md)
- [Deployment configuration reference](configuration-ownership.md)
- [Planner schema reference](planner-schema.md)
