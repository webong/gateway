<?php

declare(strict_types=1);

use Webong\Gateway\Contracts\ExtensionProviderRegistry;
use Webong\Gateway\Companion\ExtensionRuntimeConfigurationRegistry;
use Webong\Gateway\Database\DatabaseServiceProvider;
use Webong\Gateway\Dns\DnsProtocolPlanner;
use Webong\Gateway\Dns\DnsServiceProvider;
use Webong\Gateway\Email\EmailProtocolPlanner;
use Webong\Gateway\Email\EmailServiceProvider;
use Webong\Gateway\Planning\ProtocolPlannerRegistry;
use Webong\Gateway\Protocols\Protocol;
use Webong\Gateway\Resources\ConfigurationKindRegistry;
use Webong\Gateway\Web\WebProtocolPlanner;
use Webong\Gateway\Web\WebServiceProvider;

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

it('lets the DNS extension project its typed zone catalog into runtime desired state', function (): void {
    $configuration = app(ExtensionRuntimeConfigurationRegistry::class)->project('webong/dns', [
        'address' => ':5353',
        'queries_per_second' => 100,
    ]);

    expect($configuration)->toMatchArray([
        'address' => ':5353',
        'queries_per_second' => 100,
        'zones' => [],
    ]);
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

    expect($source)->not->toMatch('/use Webong\\\\Gateway\\\\(?:Dns|Email|Web|Webstream)\\\\/');

    $manifest = json_decode((string) file_get_contents($planner.'/composer.json'), true, flags: JSON_THROW_ON_ERROR);
    expect(array_keys($manifest['require']))
        ->not->toContain('webong/web', 'webong/dns', 'webong/email', 'webong/database');
});

it('keeps each Gateway extension self-contained across Planner and Spinner', function (): void {
    $root = dirname(__DIR__, 2);

    foreach (['web', 'dns', 'email', 'database'] as $extension) {
        expect($root."/pkg/{$extension}/composer.json")->toBeFile()
            ->and($root."/pkg/{$extension}/go.mod")->toBeFile()
            ->and($root."/pkg/{$extension}/spinner")->toBeDirectory();

        $spinnerSource = '';
        $files = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($root."/pkg/{$extension}/spinner"));
        foreach ($files as $file) {
            if ($file->isFile() && $file->getExtension() === 'go') {
                $spinnerSource .= file_get_contents($file->getPathname());
            }
        }
        expect($spinnerSource)->not->toContain('github.com/webong/gateway/pkg/spinner');
    }

    expect($root.'/mod/arranger/integration/traffic_contract.go')->toBeFile()
        ->and($root.'/pkg/web/spinner/websocket')->toBeDirectory()
        ->and($root.'/pkg/web/spinner/webstream')->toBeDirectory()
        ->and($root.'/pkg/dns/spinner')->toBeDirectory()
        ->and($root.'/pkg/email/spinner/smtp')->toBeDirectory()
        ->and($root.'/pkg/email/spinner/imap')->toBeDirectory()
        ->and($root.'/pkg/email/spinner/pop3')->toBeDirectory();

    foreach (['protocol', 'runtime', 'router'] as $legacyPackage) {
        expect(glob($root."/mod/arranger/{$legacyPackage}/*.go"))->toBe([]);
    }
    expect(glob($root.'/mod/arranger/automation/extension/*.go'))->toBe([]);

    $routerSource = '';
    $routerFiles = new RecursiveIteratorIterator(new RecursiveDirectoryIterator($root.'/pkg/spinner/router'));
    foreach ($routerFiles as $file) {
        if ($file->isFile() && $file->getExtension() === 'go') {
            $routerSource .= file_get_contents($file->getPathname());
        }
    }
    expect($routerSource)->not->toMatch('#github.com/webong/gateway/pkg/(?:web|dns|email|database)/#');

    foreach (['dns', 'websocket', 'webstream', 'smtp', 'smtpout', 'imap', 'pop3'] as $legacy) {
        expect($root."/pkg/spinner/bridge/{$legacy}")->not->toBeDirectory();
    }

    expect($root.'/pkg/spinner/internal/mailpoll')->not->toBeDirectory()
        ->and(file_get_contents($root.'/pkg/web/spinner/edge.go'))
        ->not->toContain('/email/submissions');
});

it('keeps the shared event ledger in Planner instead of making DNS depend on Web', function (): void {
    $root = dirname(__DIR__, 2);
    $dns = json_decode((string) file_get_contents($root.'/pkg/dns/composer.json'), true, flags: JSON_THROW_ON_ERROR);

    expect($root.'/pkg/planner/src/Events/Models/GatewayEndpointEvent.php')->toBeFile()
        ->and($root.'/pkg/planner/src/database/migrations/2026_08_30_000004_create_events_table.php')->toBeFile()
        ->and($root.'/pkg/web/src/Events')->not->toBeDirectory()
        ->and(array_keys($dns['require']))->not->toContain('webong/web');
});
