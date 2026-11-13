<?php

declare(strict_types=1);

namespace Webong\Gateway\Tests\Architecture;

use PhpParser\Node;
use PhpParser\NodeFinder;
use PhpParser\ParserFactory;
use PHPUnit\Framework\TestCase;

final class PlannerBoundaryTest extends TestCase
{
    public function testSharedPlannerDoesNotDependOnFrameworkIntegrations(): void
    {
        $root = dirname(__DIR__, 2).'/pkg/planner/src/';
        $parser = (new ParserFactory())->createForNewestSupportedVersion();
        $finder = new NodeFinder();
        $violations = [];
        foreach (new \RecursiveIteratorIterator(new \RecursiveDirectoryIterator($root)) as $file) {
            if ($file->getExtension() !== 'php') {
                continue;
            }
            $relative = substr($file->getPathname(), strlen($root));
            $integration = str_starts_with($relative, 'Laravel/') || str_starts_with($relative, 'Symfony/');
            $ast = $parser->parse(file_get_contents($file->getPathname()));
            foreach ($finder->findInstanceOf($ast, Node\Name::class) as $name) {
                $value = $name->toString();
                $forbidden = $integration
                    ? (str_starts_with($relative, 'Symfony/') ? ['Illuminate\\', 'Laravel\\', 'Webong\\Proxy\\', 'Webong\\Gateway\\Laravel\\'] : ['Webong\\Gateway\\Symfony\\'])
                    : ['Illuminate\\', 'Laravel\\', 'Webong\\Proxy\\', 'Webong\\Gateway\\Laravel\\', 'Webong\\Gateway\\Symfony\\', 'Symfony\\Bundle\\', 'Symfony\\Component\\Http', 'Symfony\\Component\\DependencyInjection\\', 'Symfony\\Component\\Routing\\'];
                foreach ($forbidden as $prefix) {
                    if (str_starts_with($value, $prefix)) {
                        $violations[] = "$relative imports $value";
                    }
                }
            }
            if (! str_starts_with($relative, 'Laravel/')) {
                foreach ($finder->findInstanceOf($ast, Node\Expr\FuncCall::class) as $call) {
                    if ($call->name instanceof Node\Name && in_array($call->name->toString(), [
                        'app', 'config', 'resolve', 'response', 'request', 'abort', 'abort_if', 'abort_unless',
                        'collect', 'auth', 'now', 'storage_path', 'base_path', 'config_path', 'database_path',
                        'dispatch', 'event', 'report', 'logger', 'validator', 'retry', 'cache', 'env',
                    ], true)) {
                        $violations[] = "$relative calls Laravel helper {$call->name}";
                    }
                }
            }
        }

        self::assertSame([], $violations);
    }
}
