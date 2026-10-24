<?php

declare(strict_types=1);

namespace Webong\Gateway\Companion;

use RuntimeException;

final class CompanionActionIdempotencyConflict extends RuntimeException {}
