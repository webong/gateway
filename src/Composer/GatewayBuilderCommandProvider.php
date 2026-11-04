<?php

declare(strict_types=1);

namespace Webong\GatewayBuilder\Composer;

use Composer\Plugin\Capability\CommandProvider;

final class GatewayBuilderCommandProvider implements CommandProvider
{
    public function getCommands(): array
    {
        return [new BuildExtensionCommand];
    }
}
