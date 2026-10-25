<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\Dns\DnsProtocolPlanner;
use Webong\Gateway\Dns\DnsServiceProvider;
use Webong\Gateway\Email\EmailProtocolPlanner;
use Webong\Gateway\Email\EmailServiceProvider;
use Webong\Gateway\Planning\ProtocolPlannerRegistry;
use Webong\Gateway\Protocols\Protocol;
use Webong\Gateway\Resources\ConfigurationKindRegistry;
use Webong\Gateway\Web\WebProtocolPlanner;
use Webong\Gateway\Web\WebServiceProvider;
use Webong\Gateway\Database\DatabaseServiceProvider;

it('discovers first-party Planner entrypoints through the Gateway extension registry', function (): void {
    expect(ExtensionProviderRegistry::providers())->toContain(
        WebServiceProvider::class,
        DnsServiceProvider::class,
        EmailServiceProvider::class,
        DatabaseServiceProvider::class,
    );
});

it('lets installed Gateway extensions own their protocol policy and resource kinds', function (): void {
    $planners = app(ProtocolPlannerRegistry::class);
    $kinds = app(ConfigurationKindRegistry::class);

    expect($planners->resolve(Protocol::WEBSOCKET))->toBeInstanceOf(WebProtocolPlanner::class)
        ->and($planners->resolve(Protocol::WEBSTREAM))->toBeInstanceOf(WebProtocolPlanner::class)
        ->and($planners->resolve(Protocol::DNS))->toBeInstanceOf(DnsProtocolPlanner::class)
        ->and($planners->resolve(Protocol::SMTP))->toBeInstanceOf(EmailProtocolPlanner::class)
        ->and($planners->resolve(Protocol::IMAP))->toBeInstanceOf(EmailProtocolPlanner::class)
        ->and($planners->resolve(Protocol::POP3))->toBeInstanceOf(EmailProtocolPlanner::class)
        ->and($kinds->contains('webstream.private-source'))->toBeTrue()
        ->and($kinds->contains('dns.zone'))->toBeTrue()
        ->and($kinds->contains('email.domain'))->toBeTrue()
        ->and($kinds->contains('database.connection'))->toBeFalse();
});

it('keeps Planner core independent of transport extension namespaces', function (): void {
    $planner = dirname(__DIR__, 2).'/pkg/planner';
    $source = '';
    $files = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($planner.'/src'));

    foreach ($files as $file) {
        if ($file->isFile() && $file->getExtension() === 'php') {
            $source .= file_get_contents($file->getPathname());
        }
    }

    expect($source)->not->toMatch('/use Webong\\\\Gateway\\\\(?:Dns|Email|Events|Web|Webstream)\\\\/');

    $manifest = json_decode((string) file_get_contents($planner.'/composer.json'), true, flags: JSON_THROW_ON_ERROR);
    expect(array_keys($manifest['require']))
        ->not->toContain('webong/web', 'webong/dns', 'webong/email', 'webong/database');
});
