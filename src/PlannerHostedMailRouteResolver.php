<?php

declare(strict_types=1);

namespace Webong\Gateway\Email;

/** CMS routes take precedence; installed provider modules remain an extension seam. */
final readonly class PlannerHostedMailRouteResolver implements EmailProviderRouteResolver
{
    public function __construct(private EmailHostedMailCatalog $catalog,
        private ?EmailProviderRouteResolver $fallback = null) {}

    public function resolve(string $recipient): ?EmailProviderRoute
    {
        return $this->catalog->route($recipient) ?? $this->fallback?->resolve($recipient);
    }
}
