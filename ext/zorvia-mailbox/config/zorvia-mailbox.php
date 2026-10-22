<?php

declare(strict_types=1);

return [
    // Zorvia's setup endpoint accepts PATCH or PUT. Keep the choice in this
    // client extension rather than adding its HTTP contract to Gateway core.
    'method' => 'PATCH',
];
