<?php

declare(strict_types=1);

namespace Webong\Gateway\Symfony;

use Symfony\Component\DependencyInjection\Extension\ExtensionInterface;
use Symfony\Component\HttpKernel\Bundle\Bundle;
use Webong\Gateway\Symfony\DependencyInjection\GatewayExtension;

final class GatewayBundle extends Bundle
{
    public function getContainerExtension(): ?ExtensionInterface
    {
        return $this->extension ??= new GatewayExtension();
    }
}
