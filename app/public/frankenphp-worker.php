<?php

declare(strict_types=1);

if (! defined('STDERR')) {
    define('STDERR', fopen('php://stderr', 'wb'));
}

$_SERVER['APP_BASE_PATH'] = $_ENV['APP_BASE_PATH'] ?? $_SERVER['APP_BASE_PATH'] ?? dirname(__DIR__);
$_SERVER['APP_PUBLIC_PATH'] = $_ENV['APP_PUBLIC_PATH'] ?? $_SERVER['APP_PUBLIC_PATH'] ?? __DIR__;
$_ENV['COMPOSER_VENDOR_DIR'] = $_SERVER['COMPOSER_VENDOR_DIR'] = $_ENV['COMPOSER_VENDOR_DIR']
    ?? $_SERVER['COMPOSER_VENDOR_DIR']
    ?? dirname(__DIR__, 2).'/vendor';

require dirname(__DIR__, 2).'/vendor/laravel/octane/bin/frankenphp-worker.php';
