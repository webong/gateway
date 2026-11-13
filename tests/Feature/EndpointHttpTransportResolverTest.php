<?php

declare(strict_types=1);

use Webong\Gateway\Laravel\EndpointHttpTransportResolver;
use Webong\Gateway\Protocols\HttpTransport;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Tests\Support\TestPathResolver;
use Webong\Proxy\Endpoint;
use Webong\Proxy\EndpointDefinition;
use Webong\Proxy\EnsureEndpoint;
use Webong\Proxy\GuzzleClientFactory;
use Webong\Proxy\Http\HttpProfile;
use Webong\Proxy\Http\HttpProfileNormalizer;
use Webong\Proxy\ProfileDefinition;

/**
 * The same fixture drives Spinner's forwarder tests
 * (pkg/spinner/internal/forwarding/profile_fixture_test.go), so a profile that
 * resolves here is the profile Go routes with, and a profile rejected here is
 * one the Planner can never hand to Spinner.
 *
 * @return array{profiles: list<array<string, mixed>>, invalid_profiles: list<array<string, mixed>>, invalid_wire: list<array<string, mixed>>}
 */
function httpTransportFixture(): array
{
    $path = __DIR__.'/../../mod/arranger/integration/testdata/http_transport_profiles.json';

    return json_decode((string) file_get_contents($path), true, flags: JSON_THROW_ON_ERROR);
}

function httpTransportEndpoint(): Endpoint
{
    return app(EnsureEndpoint::class)->handle(new EndpointDefinition(
        client: 'relay-test',
        externalId: 'provider-app-transport',
        signingSecret: 'provider-secret',
        verificationToken: 'verification-token',
        endpointKey: 'transport-endpoint',
        credentialOwnerId: 'endpoint-owner',
        managed: false,
    ));
}

/** @param array<string, mixed> $configuration */
function httpTransportProfile(Endpoint $endpoint, string $name, array $configuration, string $driver = 'guzzle'): void
{
    $endpoint->registerProfile(new ProfileDefinition(name: $name, configuration: $configuration, driver: $driver));
}

dataset('http transport profiles', function () {
    foreach (httpTransportFixture()['profiles'] as $case) {
        yield $case['name'] => [$case];
    }
});

dataset('invalid http transport profiles', function () {
    foreach (httpTransportFixture()['invalid_profiles'] as $case) {
        yield $case['name'] => [$case];
    }
});

it('resolves every shared fixture profile into the Go delivery contract', function (array $case): void {
    $endpoint = httpTransportEndpoint();
    httpTransportProfile($endpoint, $case['name'], $case['configuration']);

    $transport = app(EndpointHttpTransportResolver::class)->resolve($endpoint, $case['name']);

    expect($transport->toArray())->toEqual($case['wire'])
        ->and(HttpTransport::fromArray($transport->toArray())->toArray())->toEqual($case['wire']);
})->with('http transport profiles');

it('exports the same portable settings as Proxy for every shared fixture profile', function (array $case): void {
    $endpoint = httpTransportEndpoint();
    httpTransportProfile($endpoint, $case['name'], $case['configuration']);

    $exported = app(GuzzleClientFactory::class)->profile($endpoint, $case['name']);
    $wire = app(EndpointHttpTransportResolver::class)->resolve($endpoint, $case['name'])->toArray();
    unset($wire['stream_headers']);

    expect($exported)->toBeInstanceOf(HttpProfile::class)
        ->and(array_diff_key($exported->toArray(), ['version' => 1]))->toEqual($wire);
})->with('http transport profiles');

it('rejects every shared invalid profile and names the profile', function (array $case): void {
    $endpoint = httpTransportEndpoint();
    httpTransportProfile($endpoint, $case['name'], $case['configuration']);

    expect(fn () => app(EndpointHttpTransportResolver::class)->resolve($endpoint, $case['name']))
        ->toThrow(InvalidArgumentException::class, 'Proxy profile ['.$case['name'].']');
})->with('invalid http transport profiles');

it('never repeats credentials in a rejection', function (): void {
    $endpoint = httpTransportEndpoint();
    foreach ([
        'path' => 'http://proxy-user:proxy-secret@proxy.example.test:8080/relay',
        'query' => 'http://proxy-user:proxy-secret@proxy.example.test:8080/?token=1',
        'control-byte' => "http://proxy-user:proxy%0Asecret@proxy.example.test:8080",
        'socks' => 'socks5://proxy-user@proxy.example.test:1080',
    ] as $name => $proxy) {
        httpTransportProfile($endpoint, $name, ['proxy' => $proxy]);

        try {
            app(EndpointHttpTransportResolver::class)->resolve($endpoint, $name);
            $this->fail("Proxy URL [{$name}] was accepted.");
        } catch (InvalidArgumentException $exception) {
            expect($exception->getMessage())->not->toContain('proxy-secret')
                ->and($exception->getMessage())->not->toContain('proxy-user');
        }
    }
});

it('rejects an HTTP profile driver that is not HTTP-compatible', function (): void {
    $endpoint = httpTransportEndpoint();
    httpTransportProfile($endpoint, 'mail', ['proxy' => 'http://proxy.example.test:8080'], driver: 'smtp');

    expect(fn () => app(EndpointHttpTransportResolver::class)->resolve($endpoint, 'mail'))
        ->toThrow(InvalidArgumentException::class, 'Proxy profile [mail] does not use an HTTP-compatible driver.');
});

it('accepts the http driver as well as the guzzle driver', function (): void {
    $endpoint = httpTransportEndpoint();
    httpTransportProfile($endpoint, 'portable', ['proxy' => 'http://proxy.example.test:8080'], driver: 'http');

    expect(app(EndpointHttpTransportResolver::class)->resolve($endpoint, 'portable')->httpProxy)
        ->toBe('http://proxy.example.test:8080');
});

it('keeps Guzzle-only options out of the wire and the profile', function (): void {
    $endpoint = httpTransportEndpoint();
    $configuration = [
        'proxy' => 'http://proxy.example.test:8080',
        'options' => ['verify' => '/etc/ssl/private-ca.pem', 'allow_redirects' => false, 'auth' => ['user', 'password'], 'cookies' => true],
    ];
    httpTransportProfile($endpoint, 'native', $configuration);

    $json = json_encode(app(EndpointHttpTransportResolver::class)->resolve($endpoint, 'native')->toArray(), JSON_THROW_ON_ERROR);

    expect($json)->not->toContain('private-ca')->not->toContain('password')->not->toContain('allow_redirects')
        ->and((new HttpProfileNormalizer())->normalize($configuration)->toArray())
        ->toBe(['version' => 1, 'proxy' => ['http' => 'http://proxy.example.test:8080', 'https' => 'http://proxy.example.test:8080']]);
});

it('now honors a proxy configured only under options like the native Guzzle client does', function (): void {
    // Before Proxy's normalizer, Gateway read only the top-level proxy and quietly
    // delivered directly. Proxy documents options.proxy as equivalent, with the
    // top-level value winning.
    $endpoint = httpTransportEndpoint();
    httpTransportProfile($endpoint, 'nested', ['options' => ['proxy' => 'http://nested.example.test:3128']]);
    httpTransportProfile($endpoint, 'both', ['proxy' => 'http://top.example.test:3128', 'options' => ['proxy' => 'http://nested.example.test:3128']]);

    $resolver = app(EndpointHttpTransportResolver::class);

    expect($resolver->resolve($endpoint, 'nested')->httpProxy)->toBe('http://nested.example.test:3128')
        ->and($resolver->resolve($endpoint, 'both')->httpProxy)->toBe('http://top.example.test:3128');
});

it('carries timeouts as floats and leaves omitted timeouts omitted', function (): void {
    $endpoint = httpTransportEndpoint();
    httpTransportProfile($endpoint, 'timed', ['options' => ['connect_timeout' => 5, 'timeout' => 15]]);
    httpTransportProfile($endpoint, 'untimed', ['proxy' => 'http://proxy.example.test:8080']);

    $resolver = app(EndpointHttpTransportResolver::class);
    $timed = $resolver->resolve($endpoint, 'timed');
    $untimed = $resolver->resolve($endpoint, 'untimed');

    expect($timed->connectTimeoutSeconds)->toBe(5.0)->and($timed->requestTimeoutSeconds)->toBe(15.0)
        ->and($untimed->connectTimeoutSeconds)->toBeNull()->and($untimed->requestTimeoutSeconds)->toBeNull();
});

it('serializes a planned delivery that Spinner forwards through the profile', function (): void {
    $root = dirname(__DIR__, 2);
    $go = trim((string) shell_exec('command -v go 2>/dev/null'));
    $needed = preg_match('/^go\s+(\S+)/m', (string) file_get_contents($root.'/pkg/spinner/go.mod'), $directive) === 1 ? $directive[1] : '0';
    $installed = $go === '' ? '' : ltrim(trim((string) shell_exec('GOTOOLCHAIN=local '.escapeshellarg($go).' env GOVERSION 2>/dev/null')), 'go');
    if ($installed === '' || version_compare($installed, $needed, '<') || getenv('GATEWAY_SKIP_GO_INTEGRATION') === '1') {
        $this->markTestSkipped("Needs Go {$needed} or newer on PATH to route the serialized delivery through Spinner.");
    }

    $endpoint = httpTransportEndpoint();
    TestPathResolver::$endpointKey = $endpoint->record->endpoint_key;
    httpTransportProfile($endpoint, 'egress', [
        'proxy' => ['https' => 'http://gateway:p%40ss@proxy.example.test:8080', 'no' => ['.internal.example.test']],
        'options' => ['connect_timeout' => 5, 'timeout' => 15],
    ]);
    $this->withToken('registry-secret')->postJson('/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions', [
        'subscriber_id' => 'profiled-subscriber',
        'group' => 'meta',
        'routing_scope' => 'application',
        'routing_key' => 'app-123',
        'url' => 'https://example.com/hook',
        'profile' => 'egress',
    ])->assertCreated();

    $plan = app(Webong\Gateway\Web\RegistryRoutePlanner::class)->plan(new IngressRequest(
        deliveryId: 'e2e-delivery', method: 'POST', host: 'relay.example.test', path: '/provider/events/app-123',
        rawQuery: 'source=meta', headers: ['Content-Type' => ['application/json']], body: '{"event":"created"}',
    ));
    expect($plan->relays)->toHaveCount(1);

    $file = tempnam(sys_get_temp_dir(), 'gateway-delivery-');
    file_put_contents($file, json_encode($plan->relays[0]->toArray(), JSON_THROW_ON_ERROR));
    try {
        exec(sprintf(
            'GOTOOLCHAIN=local GOWORK=off GATEWAY_PLANNER_DELIVERY_JSON=%s %s -C %s test -v -count=1 -run %s ./internal/forwarding 2>&1',
            escapeshellarg($file), escapeshellarg($go), escapeshellarg($root.'/pkg/spinner'),
            escapeshellarg('^TestPlannerSerializedDeliveryIsForwardedThroughItsProfile$'),
        ), $output, $status);
        expect($status)->toBe(0, implode("\n", $output))
            ->and(implode("\n", $output))->toContain('--- PASS: TestPlannerSerializedDeliveryIsForwardedThroughItsProfile');
    } finally {
        unlink($file);
    }
});

it('refuses to subscribe with a profile outside the portable contract', function (): void {
    $endpoint = httpTransportEndpoint();
    TestPathResolver::$endpointKey = $endpoint->record->endpoint_key;
    httpTransportProfile($endpoint, 'cidr', ['proxy' => ['http' => 'http://proxy.example.test:8080', 'no' => ['10.0.0.0/8']]]);

    $this->withToken('registry-secret')->postJson('/registry/endpoints/'.$endpoint->record->endpoint_key.'/subscriptions', [
        'subscriber_id' => 'profiled-subscriber',
        'group' => 'meta',
        'routing_scope' => 'application',
        'routing_key' => 'app-123',
        'url' => 'https://profiled.example.test/hook',
        'profile' => 'cidr',
    ])->assertUnprocessable()
        ->assertJsonPath('errors.subscription.0', fn (string $message): bool => str_starts_with($message, 'Proxy profile [cidr] is not a valid portable HTTP profile:'));
});
