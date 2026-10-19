<?php

declare(strict_types=1);

namespace Webong\Gateway;

use Throwable;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Dns\DnsHookEventRecorder;
use Webong\Gateway\Email\EmailRegistry;
use Webong\Gateway\Email\EmailRouteRejected;
use Webong\Gateway\Email\EmailMessagePolicy;
use Webong\Gateway\Email\EmailPolicyVerdict;
use Webong\Gateway\Email\EmailSubmissionAuthenticator;
use Webong\Gateway\Email\EmailSubmissionQuota;
use Webong\Gateway\Email\EmailRuntimePolicyStore;
use Webong\Gateway\Email\EmailProviderRoute;
use Webong\Gateway\Email\EmailProviderRouteResolver;
use Webong\Gateway\Email\EmailSuppressionStore;
use Webong\Gateway\Email\MailboxAuthenticator;
use Webong\Gateway\Email\MailHookRegistry;
use Webong\Gateway\Email\DmarcEvaluator;
use Webong\Gateway\Email\SrsReturnStore;
use Webong\Gateway\Protocol\Delivery;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayDecision;
use Webong\Gateway\Protocol\GatewayDelivery;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\Protocol;
use Webong\Gateway\Protocol\RoutePlan;

/**
 * Adapts the existing PHP registry planner to protocol-neutral events.
 *
 * HTTP destinations remain the first supported egress for SMTP/WebSocket/DNS
 * events. The original event payload is carried to each destination so the
 * Go transport can deliver it without PHP owning network I/O.
 */
final class RegistryProtocolPlanner implements ProtocolPlanner
{
    public function __construct(
        private readonly RoutePlanner $routePlanner,
        private readonly ?DnsHookEventRecorder $dnsEvents = null,
        private readonly ?EmailRegistry $email = null,
        private readonly ?DmarcEvaluator $dmarc = null,
        private readonly ?SrsReturnStore $srs = null,
        private readonly ?EmailMessagePolicy $emailPolicy = null,
        private readonly ?EmailSubmissionAuthenticator $submissionAuthenticator = null,
        private readonly ?EmailSubmissionQuota $submissionQuota = null,
        private readonly ?MailboxAuthenticator $mailboxAuthenticator = null,
        private readonly ?MailHookRegistry $mailHooks = null,
        private readonly ?EmailProviderRouteResolver $providerRoutes = null,
        private readonly ?EmailSuppressionStore $suppressions = null,
        private readonly ?\Webong\Gateway\Webstream\WebstreamPrivateSourceStore $webstreamPrivateSources = null,
        private readonly ?\Webong\Gateway\Webstream\WebstreamRoutePolicyStore $webstreamRoutePolicies = null,
        private readonly ?\Webong\Gateway\Email\EmailSubmissionPolicyStore $emailSubmissionPolicies = null,
        private readonly ?EmailRuntimePolicyStore $emailRuntimePolicy = null,
    ) {}

    public function planEvent(GatewayEvent $event): GatewayDecision
    {
        try {
            $decision = $this->plan($event);
            $this->dnsEvents?->record($event, $decision);

            return $decision;
        } catch (Throwable $exception) {
            $this->dnsEvents?->record($event, failure: $exception);

            throw $exception;
        }
    }

    private function plan(GatewayEvent $event): GatewayDecision
    {
        if ($event->protocol === Protocol::WEBSTREAM && ($event->attributes['upload_mode'] ?? '') === 'stream') {
            $policy = $this->webstreamPolicy($event);
            if (($policy['upload'] ?? false) !== true || $event->method !== 'POST' || $event->payload !== '') {
                return new GatewayDecision(protocol: $event->protocol, action: GatewayAction::REJECT, statusCode: 403, message: 'Streaming uploads are not enabled for this route.');
            }
        }
        if (in_array($event->protocol, [Protocol::POP3, Protocol::IMAP], true)) {
            if ($event->kind !== EventKind::AUTHENTICATE) {
                return new GatewayDecision(protocol: $event->protocol, action: GatewayAction::ACCEPT);
            }
            $grant = $this->mailboxAuthenticator?->authorize($event);
            if ($grant === null) {
                return new GatewayDecision(protocol: $event->protocol, action: GatewayAction::REJECT, message: 'Mailbox authentication failed.');
            }

            return new GatewayDecision(
                protocol: $event->protocol,
                action: GatewayAction::ACCEPT,
                metadata: [
                    'identity' => $grant->identity,
                    'upstream_id' => $grant->upstreamId,
                    'upstream_username' => $grant->upstreamUsername,
                    'upstream_password' => $grant->upstreamPassword,
                    'upstream_auth_mechanism' => $grant->upstreamAuthMechanism,
                    ...($grant->upstreamAddress !== '' ? [
                        'upstream_address' => $grant->upstreamAddress,
                        'upstream_server_name' => $grant->upstreamServerName,
                        'upstream_network_scope' => $grant->upstreamNetworkScope,
                    ] : []),
                ],
            );
        }
        if ($event->protocol === Protocol::SMTP && $event->kind === EventKind::AUTHENTICATE) {
            // Registry destinations are outbound callbacks, not credential
            // authorities. Never forward an AUTH event (including its password)
            // through a subscription or treat a callback reply as login proof.
            $grant = $this->submissionAuthenticator?->authorize($event);
            if ($grant === null) {
                return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 535, message: 'SMTP authentication failed.');
            }

            return new GatewayDecision(
                protocol: Protocol::SMTP,
                action: GatewayAction::ACCEPT,
                statusCode: 235,
                metadata: [
                    'auth_identity' => $grant->identity,
                    'authorized_mail_from' => json_encode($grant->authorizedSenders, JSON_THROW_ON_ERROR),
                    ...($grant->connectionId !== null ? ['mailbox_connection_id' => $grant->connectionId] : []),
                    ...($grant->dailyLimit !== null ? ['submission_daily_limit' => (string) $grant->dailyLimit] : []),
                    ...($grant->dailyByteLimit !== null ? ['submission_daily_byte_limit' => (string) $grant->dailyByteLimit] : []),
                ],
            );
        }
        if ($event->protocol === Protocol::SMTP && $event->kind === EventKind::TRANSACTION) {
            $quotaDecision = $this->submissionQuotaDecision($event);
            if ($quotaDecision !== null) {
                return $quotaDecision;
            }
        }
        if ($event->protocol === Protocol::SMTP && $event->kind === EventKind::RECIPIENT) {
            if ($this->srs !== null && $this->srs->isCandidate($event->route)) {
                return $this->returnRecipient($event);
            }
            if (($provider = $this->providerRoutes?->resolve(strtolower($event->route))) !== null) {
                return $this->providerRecipient($event);
            }
            if ($this->mailHookDomain($event->route) !== null) {
                return $this->mailHookRecipient($event);
            }
            return $this->email !== null
                ? $this->managedEmailRecipient($event)
                : new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::ACCEPT, statusCode: 250);
        }
        if ($event->protocol === Protocol::SMTP && $event->kind === EventKind::TRANSACTION && $this->email !== null) {
            if ($this->srs !== null && $this->hasReturnRecipient($event)) {
                return $this->returnTransaction($event);
            }
            if (($provider = $this->providerRoutes?->resolve(strtolower($event->route))) !== null) {
                return $this->providerTransaction($event, $provider);
            }
            if ($this->mailHookDomain($event->route) !== null) {
                return $this->mailHookTransaction($event);
            }
            $managed = $this->managedEmail($event);
            if ($managed !== null) {
                return $managed;
            }
        }
        $plan = $this->routePlanner->plan(new IngressRequest(
            deliveryId: $event->id,
            method: $event->method !== '' ? $event->method : 'POST',
            host: $event->host,
            path: $event->route,
            rawQuery: $event->rawQuery,
            headers: $event->headers,
            body: $event->payload,
            scheme: match ($event->protocol) {
                Protocol::SMTP => 'smtp',
                Protocol::DNS => 'dns',
                default => 'https',
            },
            protocol: $event->protocol->value,
            event: $event->kind->value,
            sessionId: $event->sessionId,
            attributes: $event->attributes,
        ));

        return match ($plan->action) {
            RoutePlan::RESPOND => $this->respond($event, $plan),
            RoutePlan::RELAY => $event->protocol === Protocol::WEBSTREAM
                ? $this->stream($event, $plan)
                : $this->deliver($event, $plan),
            RoutePlan::PASS_THROUGH => new GatewayDecision(
                protocol: $event->protocol,
                action: GatewayAction::REJECT,
                statusCode: 550,
                message: 'No registered gateway route.',
            ),
        };
    }

    private function submissionQuotaDecision(GatewayEvent $event): ?GatewayDecision
    {
        $identity = $event->attributes['auth_identity'] ?? '';
        if ($identity !== '' && $this->suppressions?->containsAny(array_values(array_filter(explode(',', $event->attributes['recipients'] ?? $event->route)))) === true) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Recipient is suppressed.');
        }
        $username = $event->attributes['auth_username'] ?? '';
        $policy = $this->emailSubmissionPolicies?->limits() ?? [
            'daily_limit' => 0, 'daily_byte_limit' => 0,
            'domain_daily_limit' => 0, 'domain_daily_byte_limit' => 0,
        ];
        $defaultLimit = $policy['daily_limit'];
        $defaultByteLimit = $policy['daily_byte_limit'];
        $domainLimit = $policy['domain_daily_limit'];
        $domainByteLimit = $policy['domain_daily_byte_limit'];
        $rawLimit = $event->attributes['submission_daily_limit'] ?? '';
        $rawByteLimit = $event->attributes['submission_daily_byte_limit'] ?? '';
        if ($defaultLimit < 0 || $defaultLimit > 1_000_000) {
            throw new \RuntimeException('Gateway Email submission daily limit is invalid.');
        }
        if ($defaultByteLimit < 0 || $defaultByteLimit > 1_000_000_000_000) {
            throw new \RuntimeException('Gateway Email submission daily byte limit is invalid.');
        }
        if ($domainLimit < 0 || $domainLimit > 1_000_000 || $domainByteLimit < 0 || $domainByteLimit > 1_000_000_000_000) {
            throw new \RuntimeException('Gateway Email sender-domain submission quota is invalid.');
        }
        if ($rawLimit !== '' && (! ctype_digit($rawLimit) || (int) $rawLimit < 1 || (int) $rawLimit > 1_000_000)) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 451, message: 'Submission quota policy is invalid.');
        }
        if ($rawByteLimit !== '' && (! ctype_digit($rawByteLimit) || (int) $rawByteLimit < 1 || (int) $rawByteLimit > 1_000_000_000_000)) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 451, message: 'Submission byte quota policy is invalid.');
        }
        $limit = $rawLimit !== '' ? (int) $rawLimit : $defaultLimit;
        $byteLimit = $rawByteLimit !== '' ? (int) $rawByteLimit : $defaultByteLimit;
        if (($limit === 0 && $byteLimit === 0 && $domainLimit === 0 && $domainByteLimit === 0)
            || ($identity === '' && $username === '' && $rawLimit === '' && $rawByteLimit === '')) {
            return null;
        }
        if ($identity === '' || $this->submissionQuota === null) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 451, message: 'Submission quota is unavailable.');
        }
        try {
            $allowed = $this->submissionQuota->consumeSubmission(
                $identity,
                (string) ($event->attributes['mail_from'] ?? ''),
                $limit,
                strlen($event->payload),
                $byteLimit,
            );
        } catch (\InvalidArgumentException) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 451, message: 'Submission quota policy is invalid.');
        }
        if (! $allowed) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 452, message: 'Daily submission quota exceeded.');
        }

        return null;
    }

    private function mailHookDomain(string $address): ?string
    {
        if ($this->mailHooks === null || ! str_contains($address, '@')) { return null; }
        $domain = strtolower(rtrim(substr($address, (int) strrpos($address, '@') + 1), '.'));

        return $this->mailHooks->activeForAddress($address) !== null ? $domain : null;
    }

    private function providerRecipient(GatewayEvent $event): GatewayDecision
    {
        if ($this->email === null) { return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 451, message: 'Mail domain registry is unavailable.'); }
        $accepted = json_decode($event->attributes['accepted_recipients'] ?? '[]', true);
        if ($accepted !== []) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT,
                statusCode: 550, message: 'Hosted mailbox routes accept one recipient per SMTP transaction.');
        }
        $domain = substr($event->route, (int) strrpos($event->route, '@') + 1);
        try { $this->email->requireActiveDomain($domain); }
        catch (EmailRouteRejected) { return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Mail domain is unavailable.'); }
        catch (Throwable) { return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 451, message: 'Mail domain verification is unavailable.'); }

        return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::ACCEPT, statusCode: 250);
    }

    private function providerTransaction(GatewayEvent $event, EmailProviderRoute $route): GatewayDecision
    {
        if ($this->email === null) { return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 451, message: 'Mail domain registry is unavailable.'); }
        $recipients = array_values(array_filter(explode(',', $event->attributes['recipients'] ?? $event->route)));
        if (count($recipients) !== 1 || strcasecmp($recipients[0], $event->route) !== 0) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT,
                statusCode: 550, message: 'Hosted mailbox routes accept one recipient per SMTP transaction.');
        }
        $domain = substr($event->route, (int) strrpos($event->route, '@') + 1);
        try { $this->email->requireActiveDomain($domain); }
        catch (EmailRouteRejected) { return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Mail domain is unavailable.'); }
        catch (Throwable) { return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 451, message: 'Mail domain verification is unavailable.'); }
        if ($this->suppressions?->containsAny($recipients) === true) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Recipient is suppressed.');
        }
        $assessment = $this->dmarc?->evaluate($event);
        $policy = $this->emailPolicy?->assess($event, $recipients, $assessment) ?? EmailPolicyVerdict::Accept;
        if ($policy !== EmailPolicyVerdict::Accept) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT,
                statusCode: $policy === EmailPolicyVerdict::Defer ? 451 : 550, message: 'Message rejected by email policy.');
        }
        if (($this->emailRuntimePolicy?->current()['dmarc_enforce_enabled'] ?? false) && $assessment !== null) {
            if ($assessment->result === 'temperror') {
                return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT,
                    statusCode: 451, message: 'Sender authentication is temporarily unavailable.');
            }
            if ($assessment->result === 'fail' && in_array($assessment->effectivePolicy, ['quarantine', 'reject'], true)) {
                return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::QUARANTINE, statusCode: 250);
            }
        }
        return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::DELIVER,
            statusCode: 250, deliveries: [new GatewayDelivery(
                protocol: Protocol::SMTP, adapter: $route->adapter, target: $route->mailboxKey,
                subscriberId: $route->accountId, payload: $event->payload,
                attributes: ['event_id' => $event->id, 'account_id' => $route->accountId,
                    ...($route->hostedAdapterId !== null ? ['hosted_adapter_id' => $route->hostedAdapterId] : []),
                    'mail_from' => $event->attributes['mail_from'] ?? '',
                    'recipient' => $recipients[0], 'recipients' => json_encode($recipients, JSON_THROW_ON_ERROR)],
            )]);
    }

    private function mailHookRecipient(GatewayEvent $event): GatewayDecision
    {
        $domain = $this->mailHookDomain($event->route);
        $accepted = json_decode($event->attributes['accepted_recipients'] ?? '[]', true);
        if ($domain === null || ! is_array($accepted) || $accepted !== []) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Mail hooks accept one recipient per SMTP transaction.');
        }
        try { $this->email->requireActiveDomain($domain); }
        catch (EmailRouteRejected) { return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Mail domain is unavailable.'); }
        catch (Throwable) { return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 451, message: 'Mail domain verification is unavailable.'); }

        return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::ACCEPT, statusCode: 250);
    }

    private function mailHookTransaction(GatewayEvent $event): GatewayDecision
    {
        $domain = $this->mailHookDomain($event->route);
        $recipients = array_values(array_filter(explode(',', $event->attributes['recipients'] ?? $event->route)));
        if ($domain === null || count($recipients) !== 1 || $this->mailHookDomain($recipients[0]) !== $domain) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Mail hooks accept one recipient per SMTP transaction.');
        }
        try { $this->email->requireActiveDomain($domain); }
        catch (EmailRouteRejected) { return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Mail domain is unavailable.'); }
        catch (Throwable) { return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 451, message: 'Mail domain verification is unavailable.'); }
        if ($this->suppressions?->containsAny($recipients) === true) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Recipient is suppressed.');
        }
        $assessment = $this->dmarc?->evaluate($event);
        $policy = $this->emailPolicy?->assess($event, $recipients, $assessment) ?? EmailPolicyVerdict::Accept;
        if ($policy !== EmailPolicyVerdict::Accept) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT,
                statusCode: $policy === EmailPolicyVerdict::Defer ? 451 : 550,
                message: 'Message rejected by email policy.');
        }
        if (($this->emailRuntimePolicy?->current()['dmarc_enforce_enabled'] ?? false) && $assessment !== null) {
            if ($assessment->result === 'temperror') {
                return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT,
                    statusCode: 451, message: 'Sender authentication is temporarily unavailable.');
            }
            if ($assessment->result === 'fail' && in_array($assessment->effectivePolicy, ['quarantine', 'reject'], true)) {
                return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::QUARANTINE, statusCode: 250);
            }
        }
        $plan = $this->routePlanner->plan(new IngressRequest(
            deliveryId: $event->id, method: 'POST', host: $event->host, path: $event->route, rawQuery: '',
            headers: $event->headers, body: $event->payload, scheme: 'smtp',
            protocol: 'smtp', event: $event->kind->value, sessionId: $event->sessionId,
            attributes: $event->attributes,
        ));
        if ($plan->action !== RoutePlan::RELAY || $plan->reply !== null || $plan->relays === []) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'No mail hook subscribers are active.');
        }
        $deliveries = array_map(function (Delivery $target) use ($event): GatewayDelivery {
            $delivery = $this->delivery($target, $event);
            return new GatewayDelivery(
                protocol: Protocol::HTTP, target: $delivery->target,
                subscriberId: $delivery->subscriberId, headers: $delivery->headers,
                attributes: $delivery->attributes, payload: $event->payload,
                transport: $delivery->transport,
            );
        }, $plan->relays);

        return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::DELIVER,
            statusCode: 250, deliveries: $deliveries);
    }

    private function managedEmailRecipient(GatewayEvent $event): GatewayDecision
    {
        $accepted = json_decode($event->attributes['accepted_recipients'] ?? '[]', true);
        if (! is_array($accepted) || ! array_is_list($accepted) || count(array_filter($accepted, 'is_string')) !== count($accepted)) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Invalid accepted recipient list.');
        }
        if ($this->srs !== null && count(array_filter($accepted, $this->srs->isCandidate(...))) > 0) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Return and ordinary recipients cannot be mixed.');
        }
        try {
            $this->email->resolveRecipients([...$accepted, $event->route]);
        } catch (EmailRouteRejected $exception) {
            return new GatewayDecision(
                protocol: Protocol::SMTP,
                action: GatewayAction::REJECT,
                statusCode: 550,
                message: $exception->getMessage(),
            );
        }

        return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::ACCEPT, statusCode: 250);
    }

    private function managedEmail(GatewayEvent $event): ?GatewayDecision
    {
        $addresses = array_values(array_filter(explode(',', $event->attributes['recipients'] ?? $event->route)));
        try {
            $recipients = $this->email->resolveRecipients($addresses);
        } catch (EmailRouteRejected $exception) {
            return new GatewayDecision(
                protocol: Protocol::SMTP,
                action: GatewayAction::REJECT,
                statusCode: 550,
                message: $exception->getMessage(),
            );
        }
        if ($recipients === null) {
            return null;
        }
        if ($this->suppressions?->containsAny($recipients) === true) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Recipient is suppressed.');
        }

        $assessment = $this->dmarc?->evaluate($event);
        $metadata = $assessment?->metadata() ?? [];
        $policy = $this->emailPolicy?->assess($event, $recipients, $assessment) ?? EmailPolicyVerdict::Accept;
        if ($policy !== EmailPolicyVerdict::Accept) {
            return new GatewayDecision(
                protocol: Protocol::SMTP,
                action: GatewayAction::REJECT,
                statusCode: $policy === EmailPolicyVerdict::Defer ? 451 : 550,
                message: $policy === EmailPolicyVerdict::Defer
                    ? 'Email policy is temporarily unavailable.'
                    : 'Message rejected by email policy.',
                metadata: $metadata,
            );
        }

        if (($this->emailRuntimePolicy?->current()['dmarc_enforce_enabled'] ?? false) && $assessment !== null) {
            if ($assessment->result === 'temperror') {
                return new GatewayDecision(
                    protocol: Protocol::SMTP,
                    action: GatewayAction::REJECT,
                    statusCode: 451,
                    message: 'Sender authentication is temporarily unavailable.',
                    metadata: $metadata,
                );
            }
            if ($assessment->result === 'fail' && in_array($assessment->effectivePolicy, ['quarantine', 'reject'], true)) {
                return new GatewayDecision(
                    protocol: Protocol::SMTP,
                    action: GatewayAction::QUARANTINE,
                    statusCode: 250,
                    metadata: $metadata,
                );
            }
        }

        $mailFrom = $event->attributes['mail_from'] ?? '';
        if ($mailFrom !== '' && $this->srs?->rewriteEnabled()) {
            try {
                $mailFrom = $this->srs->issue($mailFrom);
            } catch (EmailRouteRejected $exception) {
                return new GatewayDecision(
                    protocol: Protocol::SMTP,
                    action: GatewayAction::REJECT,
                    statusCode: 451,
                    message: $exception->getMessage(),
                    metadata: $metadata,
                );
            }
        }

        return new GatewayDecision(
            protocol: Protocol::SMTP,
            action: GatewayAction::SUBMIT,
            statusCode: 250,
            deliveries: [new GatewayDelivery(
                protocol: Protocol::SMTP,
                target: $recipients[0],
                attributes: [
                    'mail_from' => $mailFrom,
                    'recipients' => json_encode($recipients, JSON_THROW_ON_ERROR),
                ],
                payload: $event->payload,
            )],
            metadata: $metadata,
        );
    }

    private function hasReturnRecipient(GatewayEvent $event): bool
    {
        foreach (explode(',', $event->attributes['recipients'] ?? $event->route) as $address) {
            if ($this->srs->isCandidate($address)) {
                return true;
            }
        }

        return false;
    }

    private function returnRecipient(GatewayEvent $event): GatewayDecision
    {
        $accepted = json_decode($event->attributes['accepted_recipients'] ?? '[]', true);
        if ($accepted !== [] || ($event->attributes['mail_from'] ?? '') !== '' || $this->srs->resolve($event->route) === null) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Invalid return address or SMTP envelope.');
        }

        return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::ACCEPT, statusCode: 250);
    }

    private function returnTransaction(GatewayEvent $event): GatewayDecision
    {
        $addresses = array_values(array_filter(explode(',', $event->attributes['recipients'] ?? $event->route)));
        $sender = count($addresses) === 1 && ($event->attributes['mail_from'] ?? '') === ''
            ? $this->srs->resolve($addresses[0])
            : null;
        if ($sender === null) {
            return new GatewayDecision(protocol: Protocol::SMTP, action: GatewayAction::REJECT, statusCode: 550, message: 'Invalid return address or SMTP envelope.');
        }

        return new GatewayDecision(
            protocol: Protocol::SMTP,
            action: GatewayAction::SUBMIT,
            statusCode: 250,
            deliveries: [new GatewayDelivery(
                protocol: Protocol::SMTP,
                target: $sender,
                attributes: ['mail_from' => '', 'recipients' => json_encode([$sender], JSON_THROW_ON_ERROR)],
                payload: $event->payload,
            )],
        );
    }

    private function stream(GatewayEvent $event, RoutePlan $plan): GatewayDecision
    {
        if ($plan->reply === null) {
            return new GatewayDecision(
                protocol: $event->protocol,
                action: GatewayAction::REJECT,
                statusCode: 404,
                message: 'No Webstream source is registered.',
            );
        }

        $source = $this->delivery($plan->reply, $event);
        $method = strtoupper($event->method ?: 'GET');
        if (! in_array($method, ['GET', 'POST'], true) || ($method === 'POST' && $plan->reply->method !== '' && strtoupper($plan->reply->method) !== 'POST')) {
            return new GatewayDecision(protocol: $event->protocol, action: GatewayAction::REJECT, statusCode: 405, message: 'Webstream source method is not permitted.');
        }
        $headers = $source->headers;
        $upload = ($event->attributes['upload_mode'] ?? '') === 'stream';
        $policy = $this->webstreamPolicy($event);
        $retry = ! $upload && $method === 'POST' && ($policy['retry'] ?? false) === true;
        if (($upload || $retry) && (($policy['source'] ?? '') !== $source->target || ($upload && $plan->reply->body !== ''))) {
            return new GatewayDecision(protocol: $event->protocol, action: GatewayAction::REJECT, statusCode: 403, message: 'Webstream policy does not permit this source.');
        }
        if ($retry) {
            $keys = [];
            foreach ($event->headers as $name => $values) {
                if (strcasecmp($name, 'Idempotency-Key') === 0) {
                    $keys = [...$keys, ...$values];
                }
            }
            if (count($keys) !== 1 || trim($keys[0]) === '' || strlen($keys[0]) > 255) {
                return new GatewayDecision(protocol: $event->protocol, action: GatewayAction::REJECT, statusCode: 400, message: 'One Idempotency-Key is required.');
            }
            $headers = array_filter($headers, static fn (string $name): bool => strcasecmp($name, 'Idempotency-Key') !== 0, ARRAY_FILTER_USE_KEY);
            $headers['Idempotency-Key'] = $keys;
        }
        if ($method === 'POST') {
            $hasContentType = false;
            foreach ($headers as $name => $values) {
                $hasContentType = $hasContentType || strcasecmp($name, 'Content-Type') === 0;
            }
            foreach ($event->headers as $name => $values) {
                if (! $hasContentType && strcasecmp($name, 'Content-Type') === 0) {
                    $headers['Content-Type'] = $values;
                }
            }
        }
        $lastEventId = null;
        foreach ($event->headers as $name => $values) {
            if (strcasecmp($name, 'Last-Event-ID') === 0) {
                $lastEventId = $values[0] ?? null;
                break;
            }
        }
        if ($lastEventId !== null) {
            $headers = array_filter(
                $headers,
                static fn (string $name): bool => strcasecmp($name, 'Last-Event-ID') !== 0,
                ARRAY_FILTER_USE_KEY,
            );
            $headers['Last-Event-ID'] = [$lastEventId];
        }
        $source = new GatewayDelivery(
            protocol: $source->protocol,
            target: $source->target,
            subscriberId: $source->subscriberId,
            headers: $headers,
            attributes: [...$source->attributes, 'method' => $method, 'raw_query' => $plan->reply->rawQuery !== '' ? $plan->reply->rawQuery : $event->rawQuery, 'upload_mode' => $upload ? 'stream' : 'buffered', 'retry_idempotent' => $retry ? 'true' : 'false', 'private_source_cidrs' => $this->privateSourceCidrs($source->target)],
            payload: $method === 'POST' ? $source->payload : '',
            adapter: $source->adapter,
            transport: $source->transport,
        );

        return new GatewayDecision(
            protocol: $event->protocol,
            action: GatewayAction::STREAM,
            stream: $source,
            metadata: $plan->metadata,
        );
    }

    private function privateSourceCidrs(string $target): string
    {
        if ($this->webstreamPrivateSources === null) {
            return '';
        }
        $parts = parse_url($target);
        if ($parts === false || ! isset($parts['scheme'], $parts['host'], $parts['port'])) {
            return '';
        }
        $origin = strtolower($parts['scheme']).'://'.strtolower($parts['host']).':'.$parts['port'];

        return implode(',', $this->webstreamPrivateSources->activeCidrs($origin));
    }

    /** @return array{source?: string, upload?: bool, retry?: bool} */
    private function webstreamPolicy(GatewayEvent $event): array
    {
        $stored = $this->webstreamRoutePolicies?->forRoute($event->host, $event->route);
        if ($stored !== null) {
            return (bool) $stored->enabled ? [
                'source' => $stored->source,
                'upload' => (bool) $stored->upload,
                'retry' => (bool) $stored->retry,
            ] : [];
        }

        return [];
    }

    private function respond(GatewayEvent $event, RoutePlan $plan): GatewayDecision
    {
        $response = $plan->immediateResponse;

        return new GatewayDecision(
            protocol: $event->protocol,
            action: GatewayAction::RESPOND,
            statusCode: $response?->statusCode ?? 500,
            headers: $response?->headers ?? [],
            payload: $response?->body ?? '',
        );
    }

    private function deliver(GatewayEvent $event, RoutePlan $plan): GatewayDecision
    {
        $reply = $event->protocol === Protocol::DNS && $plan->reply !== null
            ? $this->delivery($plan->reply, $event)
            : null;
        $sourceDeliveries = $event->protocol === Protocol::DNS
            ? $plan->relays
            : array_filter([$plan->reply, ...$plan->relays]);
        $deliveries = array_map(
            fn (Delivery $delivery): GatewayDelivery => $this->delivery($delivery, $event),
            $sourceDeliveries,
        );

        if ($reply === null && $deliveries === []) {
            return new GatewayDecision(
                protocol: $event->protocol,
                action: GatewayAction::ACCEPT,
                statusCode: 250,
            );
        }

        return new GatewayDecision(
            protocol: $event->protocol,
            action: GatewayAction::DELIVER,
            statusCode: 250,
            reply: $reply,
            deliveries: array_values($deliveries),
            metadata: $plan->metadata,
        );
    }

    private function delivery(Delivery $delivery, GatewayEvent $event): GatewayDelivery
    {
        $headers = $delivery->headers;
        if ($event->protocol === Protocol::DNS && ! isset($headers['Content-Type'])) {
            $headers['Content-Type'] = ['application/json'];
        }
        if ($event->protocol === Protocol::SMTP) {
            $headers['Content-Type'] = ['message/rfc822'];
            $headers['X-Gateway-Event-Id'] = [$event->id];
            $headers['X-Gateway-Envelope'] = [base64_encode(json_encode([
                'mail_from' => $event->attributes['mail_from'] ?? '',
                'recipients' => array_values(array_filter(explode(',', $event->attributes['recipients'] ?? $event->route))),
            ], JSON_THROW_ON_ERROR))];
        }

        return new GatewayDelivery(
            protocol: Protocol::HTTP,
            target: $delivery->url,
            subscriberId: $delivery->subscriberId,
            headers: $headers,
            attributes: array_filter([
                'delivery_id' => $delivery->id,
                'event_id' => $event->id,
                'method' => $delivery->method,
                'raw_query' => $delivery->rawQuery,
            ], static fn (mixed $value): bool => $value !== ''),
            payload: $delivery->body !== '' ? $delivery->body : $event->payload,
            transport: $delivery->transport,
        );
    }
}
