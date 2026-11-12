<?php

declare(strict_types=1);

namespace Webong\Gateway\Symfony\DependencyInjection;

use Symfony\Component\Config\Definition\Builder\TreeBuilder;
use Symfony\Component\Config\Definition\ConfigurationInterface;

final class Configuration implements ConfigurationInterface
{
    public function getConfigTreeBuilder(): TreeBuilder
    {
        $tree = new TreeBuilder('gateway');
        $tree->getRootNode()->children()
            ->scalarNode('internal_token')->defaultValue('%env(GATEWAY_INTERNAL_TOKEN)%')->end()
            ->integerNode('max_body_bytes')->min(1)->defaultValue(1048576)->end()
            ->arrayNode('connection')->children()
                ->enumNode('driver')->values(['sqlite', 'pgsql', 'mysql', 'mariadb'])->isRequired()->end()
                ->scalarNode('database')->isRequired()->cannotBeEmpty()->end()
                ->scalarNode('host')->end()
                ->integerNode('port')->min(1)->max(65535)->end()
                ->scalarNode('username')->end()
                ->scalarNode('password')->end()
                ->scalarNode('charset')->end()
                ->scalarNode('sslmode')->end()
                ->scalarNode('sslcert')->end()
                ->scalarNode('sslkey')->end()
                ->scalarNode('sslrootcert')->end()
                ->variableNode('search_path')->end()
                ->variableNode('options')->end()
            ->end()->end()
            ->arrayNode('tables')->useAttributeAsKey('name')->scalarPrototype()->end()->end()
        ->end();

        return $tree;
    }
}
