<?php

declare(strict_types=1);

namespace Webong\Gateway\Laravel\Servers\Contracts;

use Webong\Gateway\Laravel\Servers\Models\Application;
use Webong\Gateway\Laravel\Servers\Models\Server;

interface ApplicationServerType extends ServerType
{
    /**
     * @param  array<string, mixed>  $input
     * @return array<string, mixed>
     */
    public function validateApplication(array $input, Server $server, ?Application $application): array;

    /**
     * @param  array<string, mixed>  $validated
     * @return array<string, mixed>
     */
    public function applicationForStorage(array $validated, ?Application $application): array;

    /** @return array<string, mixed> */
    public function applicationResource(Application $application): array;
}
