<?php

declare(strict_types=1);

header('Content-Type: text/plain');
header('X-Gateway-Managed-Smoke: reached');
http_response_code(201);
echo $_SERVER['REQUEST_METHOD'].' '.$_SERVER['REQUEST_URI'];
