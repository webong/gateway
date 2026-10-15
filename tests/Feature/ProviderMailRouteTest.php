<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Email\EmailProviderRoute;
use Webong\Gateway\Email\EmailProviderRouteResolver;
use Webong\Gateway\Email\TxtResolver;
use Webong\Gateway\Protocol\EventKind;
use Webong\Gateway\Protocol\GatewayAction;
use Webong\Gateway\Protocol\GatewayEvent;
use Webong\Gateway\Protocol\Protocol;

it('routes one verified recipient to a deployment registered hosted mailbox adapter', function (): void {
    $txt = new class implements TxtResolver {
        public string $token = '';
        public function records(string $name): array { return ['gateway-verification='.$this->token]; }
    };
    app()->instance(TxtResolver::class, $txt);
    $domain = $this->withToken('registry-secret')->postJson('/email/domains', ['domain' => 'hosted.test'])->assertCreated()->json();
    $txt->token = substr($domain['verification']['value'], strlen('gateway-verification='));
    $this->withToken('registry-secret')->postJson('/email/domains/'.$domain['id'].'/verify')->assertOk();
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => true])->assertOk();
    $resolver = new class implements EmailProviderRouteResolver {
        public function resolve(string $recipient): ?EmailProviderRoute
        {
            return $recipient === 'support@hosted.test'
                ? new EmailProviderRoute('zorvia-account', 'zorvia.mailbox', 'mailbox-42') : null;
        }
    };
    app()->instance(get_class($resolver), $resolver);
    config()->set('gateway.email.provider_route_resolver', get_class($resolver));
    $planner = app(ProtocolPlanner::class);
    $recipient = $planner->planEvent(new GatewayEvent(
        id: 'hosted-rcpt', protocol: Protocol::SMTP, kind: EventKind::RECIPIENT,
        route: 'support@hosted.test', attributes: ['accepted_recipients' => '[]'],
    ));
    expect($recipient->action)->toBe(GatewayAction::ACCEPT);
    $mime = "From: sender@outside.test\r\nTo: support@hosted.test\r\n\r\nhello\r\n";
    $decision = $planner->planEvent(new GatewayEvent(
        id: 'hosted-data', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'support@hosted.test', attributes: ['mail_from' => 'sender@outside.test',
            'recipients' => 'support@hosted.test'], payload: $mime,
    ));
    expect($decision->action)->toBe(GatewayAction::DELIVER)
        ->and($decision->deliveries)->toHaveCount(1)
        ->and($decision->deliveries[0]->adapter)->toBe('zorvia.mailbox')
        ->and($decision->deliveries[0]->target)->toBe('mailbox-42')
        ->and($decision->deliveries[0]->subscriberId)->toBe('zorvia-account')
        ->and($decision->deliveries[0]->payload)->toBe($mime);

    $mixed = $planner->planEvent(new GatewayEvent(
        id: 'hosted-mixed', protocol: Protocol::SMTP, kind: EventKind::TRANSACTION,
        route: 'support@hosted.test', attributes: ['mail_from' => 'sender@outside.test',
            'recipients' => 'support@hosted.test,other@hosted.test'], payload: $mime,
    ));
    expect($mixed->action)->toBe(GatewayAction::REJECT);
    $this->withToken('registry-secret')->patchJson('/email/domains/'.$domain['id'], ['enabled' => false])->assertOk();
    $disabled = $planner->planEvent(new GatewayEvent(
        id: 'hosted-disabled', protocol: Protocol::SMTP, kind: EventKind::RECIPIENT,
        route: 'support@hosted.test', attributes: ['accepted_recipients' => '[]'],
    ));
    expect($disabled->action)->toBe(GatewayAction::REJECT);
});
