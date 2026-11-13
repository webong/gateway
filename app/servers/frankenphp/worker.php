<?php

declare(strict_types=1);

if (! defined('STDERR')) {
    define('STDERR', fopen('php://stderr', 'wb'));
}

// app/servers/frankenphp/worker.php: the Planner host is app/planner and the
// shared Composer vendor directory sits beside app/.
$appDirectory = dirname(__DIR__, 2);
$plannerDirectory = $appDirectory.'/planner';
$vendorDirectory = dirname($appDirectory).'/vendor';

$_SERVER['APP_BASE_PATH'] = $_ENV['APP_BASE_PATH'] ?? $_SERVER['APP_BASE_PATH'] ?? $plannerDirectory;
$_SERVER['APP_PUBLIC_PATH'] = $_ENV['APP_PUBLIC_PATH'] ?? $_SERVER['APP_PUBLIC_PATH'] ?? $plannerDirectory.'/public';
$_ENV['COMPOSER_VENDOR_DIR'] = $_SERVER['COMPOSER_VENDOR_DIR'] = $_ENV['COMPOSER_VENDOR_DIR']
    ?? $_SERVER['COMPOSER_VENDOR_DIR']
    ?? $vendorDirectory;

require $vendorDirectory.'/laravel/octane/bin/frankenphp-worker.php';
