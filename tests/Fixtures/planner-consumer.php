<?php

declare(strict_types=1);

use Symfony\Bundle\FrameworkBundle\FrameworkBundle;
use Symfony\Component\Config\Loader\LoaderInterface;
use Symfony\Component\Console\Tester\CommandTester;
use Symfony\Component\DependencyInjection\ContainerBuilder;
use Symfony\Component\HttpFoundation\Request;
use Symfony\Component\HttpKernel\Kernel;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Protocols\GatewayAction;
use Webong\Gateway\Protocols\GatewayDecision;
use Webong\Gateway\Protocols\GatewayEvent;
use Webong\Gateway\Protocols\IngressRequest;
use Webong\Gateway\Protocols\Response;
use Webong\Gateway\Protocols\RoutePlan;
use Webong\Gateway\Symfony\GatewayBundle;

require $argv[1].'/vendor/autoload.php';

function check(bool $condition, string $message): void
{
    if (! $condition) {
        throw new RuntimeException($message);
    }
}

final class ConsumerPlanner implements RoutePlanner, ProtocolPlanner
{
    public function plan(IngressRequest $request): RoutePlan
    {
        return RoutePlan::respond(new Response(202, [], $request->body));
    }

    public function planEvent(GatewayEvent $event): GatewayDecision
    {
        return new GatewayDecision($event->protocol, GatewayAction::RESPOND);
    }
}

final class ConsumerKernel extends Kernel
{
    public function __construct(private string $directory)
    {
        parent::__construct('test', true);
    }

    public function registerBundles(): iterable
    {
        yield new FrameworkBundle();
        yield new GatewayBundle();
    }

    public function getCacheDir(): string
    {
        return $this->directory.'/cache';
    }

    public function getLogDir(): string
    {
        return $this->directory.'/logs';
    }

    public function registerContainerConfiguration(LoaderInterface $loader): void
    {
        $loader->load(function (ContainerBuilder $container): void {
            $container->loadFromExtension('framework', [
                'secret' => 'test', 'test' => true,
                'router' => ['resource' => $this->directory.'/vendor/webong/gateway/config/symfony-routes.php'],
            ]);
            $container->loadFromExtension('gateway', [
                'internal_token' => 'consumer-secret',
                'connection' => ['driver' => 'sqlite', 'database' => $this->directory.'/gateway.sqlite'],
            ]);
            $container->register(RoutePlanner::class, ConsumerPlanner::class);
            $container->register(ProtocolPlanner::class, ConsumerPlanner::class);
        });
    }
}

check(! class_exists(\Illuminate\Foundation\Application::class), 'Laravel must not be installed.');
check(! class_exists(\Webong\Proxy\WebProxyRegistryManager::class), 'Proxy must not be installed.');
$source = $argv[1].'/vendor/webong/gateway/src/';
foreach (new RecursiveIteratorIterator(new RecursiveDirectoryIterator($source)) as $file) {
    if ($file->getExtension() !== 'php') {
        continue;
    }
    $relative = substr($file->getPathname(), strlen($source));
    if (str_starts_with($relative, 'Laravel/')) {
        continue;
    }
    $class = 'Webong\\Gateway\\'.str_replace('/', '\\', substr($relative, 0, -4));
    check(class_exists($class) || interface_exists($class), 'Cannot autoload portable Planner class '.$class);
}
$kernel = new ConsumerKernel($argv[1]);
foreach (['one', 'two'] as $body) {
    $request = Request::create('/_internal/gateway/plan', 'POST', server: ['HTTP_X_GATEWAY_INTERNAL' => 'consumer-secret'], content: json_encode([
        'delivery_id' => $body, 'method' => 'POST', 'path' => '/', 'body' => base64_encode($body),
    ], JSON_THROW_ON_ERROR));
    $response = $kernel->handle($request);
    check($response->getStatusCode() === 200, 'Symfony planning failed: '.$response->getContent());
    check(json_decode($response->getContent(), true)['immediate_response']['body'] === base64_encode($body), 'Incorrect planning payload.');
    $kernel->terminate($request, $response);
}
$response = $kernel->handle(Request::create('/_internal/gateway/event', 'POST', server: ['HTTP_X_GATEWAY_INTERNAL' => 'consumer-secret'], content: '{"id":"dns","protocol":"dns","kind":"query","route":"/dns"}'));
check($response->getStatusCode() === 200, 'Protocol planning failed.');
$console = new \Symfony\Bundle\FrameworkBundle\Console\Application($kernel);
$command = new CommandTester($console->find('gateway:migrate'));
check($command->execute([]) === 0, 'Migration command failed.');
check($command->execute([]) === 0, 'Repeated migration failed.');
$pdo = new PDO('sqlite:'.$argv[1].'/gateway.sqlite');
check((int) $pdo->query('SELECT count(*) FROM gateway_migrations')->fetchColumn() > 0, 'Gateway migration ledger missing.');
$kernel->shutdown();
echo "Standalone Symfony consumer: HTTP planning, protocol planning, and migrations passed without Laravel.\n";
