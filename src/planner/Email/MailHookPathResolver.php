<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

use Webong\Gateway\Contracts\PathResolver;
use Webong\Gateway\Protocol\IngressRequest;
use Webong\Gateway\Protocol\PathBinding;

class MailHookPathResolver implements PathResolver
{
    public function __construct(protected readonly MailHookRegistry $hooks, protected readonly ?PathResolver $fallback = null) {}

    public function resolve(IngressRequest $request): ?PathBinding
    {
        if ($request->protocol !== 'smtp') { return $this->fallback?->resolve($request); }
        $recipient = strtolower($request->path);
        $at = strrpos($recipient, '@');
        if ($at === false) { return null; }
        $hook = $this->hooks->activeForAddress($recipient);
        if ($hook === null) { return $this->fallback?->resolve($request); }

        return new PathBinding(endpointKey: $hook->endpoint_key, scope: 'smtp', key: 'recipient');
    }
}
