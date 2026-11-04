<?php

declare(strict_types=1);

namespace Webong\GatewayBuilder\Composer;

use Composer\Composer;
use Composer\IO\IOInterface;
use Composer\Plugin\Capable;
use Composer\Plugin\PluginInterface;

final class GatewayBuilderPlugin implements Capable, PluginInterface
{
    public function activate(Composer $composer, IOInterface $io): void {}

    public function deactivate(Composer $composer, IOInterface $io): void {}

    public function uninstall(Composer $composer, IOInterface $io): void {}

    public function getCapabilities(): array
    {
        return [
            \Composer\Plugin\Capability\CommandProvider::class => GatewayBuilderCommandProvider::class,
        ];
    }
}
