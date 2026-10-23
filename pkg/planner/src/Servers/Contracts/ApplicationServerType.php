<?php

declare(strict_types=1);

namespace Webong\Gateway\Servers\Contracts;

use Webong\Gateway\Servers\Models\Application;
use Webong\Gateway\Servers\Models\Server;

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
