<?php
declare(strict_types=1);

require __DIR__ . '/index.php';

if (strtoupper((string) ($_SERVER['REQUEST_METHOD'] ?? 'GET')) !== 'GET') {
    respondText('method not allowed', 405);
}

try {
    $settings = validateConfig(loadUserConfig());
    $state = loadGptStatusState($settings);
    $requestPath = parse_url((string) ($_SERVER['REQUEST_URI'] ?? '/'), PHP_URL_PATH);
    if (!is_string($requestPath) || !isValidGptStatusImageRequest($settings, $state, $_GET, $requestPath) || !is_array($state['status'] ?? null)) {
        respondText('not found', 404);
    }
    $image = renderGptStatusPng($state['status']);
    http_response_code(200);
    header('Content-Type: image/png');
    header('Cache-Control: no-store, max-age=0');
    header('X-Content-Type-Options: nosniff');
    header('Content-Length: ' . strlen($image));
    echo $image;
    exit;
} catch (Throwable $error) {
    error_log('StarryAPI GPT status image error: ' . $error->getMessage());
    respondText('not found', 404);
}
