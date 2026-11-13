<?php

declare(strict_types=1);

namespace Webong\Gateway\Symfony\DependencyInjection;

use Symfony\Component\DependencyInjection\ContainerBuilder;
use Symfony\Component\DependencyInjection\Extension\Extension;
use Symfony\Component\DependencyInjection\Reference;
use Webong\Gateway\Contracts\ProtocolPlanner;
use Webong\Gateway\Contracts\RoutePlanner;
use Webong\Gateway\Migrations\GatewayMigrationRunner;
use Webong\Gateway\Planning\PlanningEndpoint;
use Webong\Gateway\Symfony\Console\MigrateGatewayCommand;
use Webong\Gateway\Symfony\Http\PlanningController;

final class GatewayExtension extends Extension
{
    public function load(array $configs, ContainerBuilder $container): void
    {
        $config = $this->processConfiguration(new Configuration(), $configs);
        $container->register(PlanningEndpoint::class)->setArguments([$config['internal_token'], $config['max_body_bytes']]);
        $container->register(PlanningController::class)
            ->setArguments([new Reference(PlanningEndpoint::class), new Reference(RoutePlanner::class), new Reference(ProtocolPlanner::class)])
            ->addTag('controller.service_arguments')->setPublic(true);
        if (isset($config['connection'])) {
            $container->register(GatewayMigrationRunner::class)
                ->setFactory([GatewayMigrationRunner::class, 'fromConnection'])
                ->setArguments([$config['connection'], dirname(__DIR__, 3).'/migrations', $config['tables'] ?? []]);
            $container->register(MigrateGatewayCommand::class)
                ->setArguments([new Reference(GatewayMigrationRunner::class)])
                ->addTag('console.command');
        }
    }

    public function getAlias(): string
    {
        return 'gateway';
    }
}
