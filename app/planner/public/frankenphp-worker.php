<?php

declare(strict_types=1);

// `octane:start --server=frankenphp` requires a frankenphp-worker.php in the
// public path and names it in the Caddyfile it generates. The worker itself
// belongs with the other servers.
require dirname(__DIR__, 2).'/servers/frankenphp/worker.php';
