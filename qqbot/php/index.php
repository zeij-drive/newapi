<?php
/**
 * StarryAPI QQ Bot webhook for PHP-only virtual-hosting panels.
 *
 * Requires PHP 7.4+ with sodium. cURL is preferred; PHP stream HTTP is used
 * as a fallback when cURL is not available. Store credentials in the panel's
 * environment variables or in config.php outside the public document root.
 */
declare(strict_types=1);

const STARRY_API_MAX_BODY_BYTES = 1048576;
const STARRY_API_TOKEN_SKEW_SECONDS = 60;
const GPTSTATUS_PAGE_URL = 'https://status.openai.com/';
const GPTSTATUS_MAX_RESPONSE_BYTES = 2 * 1024 * 1024;
const GPTSTATUS_MAX_COMPONENTS = 40;
const GPTSTATUS_MAX_INCIDENTS = 8;

/** @return array<string, mixed> */
function loadUserConfig(): array
{
    $configuredPath = getenv('QQ_BOT_CONFIG_FILE');
    $path = is_string($configuredPath) && trim($configuredPath) !== ''
        ? trim($configuredPath)
        : __DIR__ . '/config.php';
    if (!is_file($path)) {
        return [];
    }
    $config = require $path;
    return is_array($config) ? $config : [];
}

/** @param array<string, mixed> $config */
function configValue(array $config, string $envName, string $configName, string $default = ''): string
{
    $value = getenv($envName);
    if ($value !== false && trim((string) $value) !== '') {
        return trim((string) $value);
    }
    $value = $config[$configName] ?? $default;
    return trim((string) $value);
}

/** @param array<string, mixed> $payload */
function respondJson(array $payload, int $status = 200): void
{
    http_response_code($status);
    header('Content-Type: application/json; charset=utf-8');
    header('Cache-Control: no-store');
    $encoded = json_encode($payload, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES);
    echo $encoded === false ? '{"error":"response encoding failed"}' : $encoded;
    exit;
}

function respondText(string $message, int $status): void
{
    http_response_code($status);
    header('Content-Type: text/plain; charset=utf-8');
    header('Cache-Control: no-store');
    echo $message;
    exit;
}

function requestHeader(string $name): string
{
    $headers = function_exists('getallheaders') ? getallheaders() : [];
    if (!is_array($headers)) {
        $headers = [];
    }
    foreach ($headers as $headerName => $value) {
        if (strcasecmp((string) $headerName, $name) === 0) {
            return trim((string) $value);
        }
    }
    $serverName = 'HTTP_' . strtoupper(str_replace('-', '_', $name));
    return trim((string) ($_SERVER[$serverName] ?? ''));
}

/** @param array<string, mixed>|null|string $payload
 *  @return array{status:int, body:string}
 */
function httpJsonRequest(string $method, string $url, array $headers, $payload, int $timeoutSeconds = 5, int $maxResponseBytes = 0): array
{
    $body = $payload === null
        ? null
        : (is_string($payload)
            ? $payload
            : json_encode($payload, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR));
    $headerLines = $headers;
    if ($body !== null) {
        $headerLines[] = 'Content-Length: ' . strlen($body);
    }

    if (function_exists('curl_init')) {
        $curl = curl_init($url);
        if ($curl === false) {
            throw new RuntimeException('could not initialize HTTP client');
        }
        $curlOptions = [
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_HTTPHEADER => $headerLines,
            CURLOPT_RETURNTRANSFER => $maxResponseBytes <= 0,
            CURLOPT_CONNECTTIMEOUT => min($timeoutSeconds, 5),
            CURLOPT_TIMEOUT => $timeoutSeconds,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_PROTOCOLS => CURLPROTO_HTTPS | CURLPROTO_HTTP,
            CURLOPT_REDIR_PROTOCOLS => CURLPROTO_HTTPS | CURLPROTO_HTTP,
            CURLOPT_SSL_VERIFYPEER => true,
            CURLOPT_SSL_VERIFYHOST => 2,
        ];
        $responseBody = '';
        if ($maxResponseBytes > 0) {
            $curlOptions[CURLOPT_WRITEFUNCTION] = static function ($handle, string $chunk) use (&$responseBody, $maxResponseBytes): int {
                if (strlen($responseBody) + strlen($chunk) > $maxResponseBytes) {
                    return 0;
                }
                $responseBody .= $chunk;
                return strlen($chunk);
            };
        }
        curl_setopt_array($curl, $curlOptions);
        if ($body !== null) {
            curl_setopt($curl, CURLOPT_POSTFIELDS, $body);
        }
        $curlResult = curl_exec($curl);
        if ($maxResponseBytes <= 0) {
            $responseBody = $curlResult;
        }
        $status = (int) curl_getinfo($curl, CURLINFO_RESPONSE_CODE);
        $error = curl_error($curl);
        curl_close($curl);
        if ($curlResult === false || $error !== '' || !is_string($responseBody)) {
            throw new RuntimeException('remote request failed');
        }
        if ($maxResponseBytes > 0 && strlen($responseBody) > $maxResponseBytes) {
            throw new RuntimeException('remote response is too large');
        }
        return ['status' => $status, 'body' => (string) $responseBody];
    }

    $context = stream_context_create([
        'http' => [
            'method' => $method,
            'header' => implode("\r\n", $headerLines),
            'content' => $body ?? '',
            'timeout' => $timeoutSeconds,
            'ignore_errors' => true,
            'follow_location' => 0,
            'max_redirects' => 0,
        ],
        'ssl' => [
            'verify_peer' => true,
            'verify_peer_name' => true,
        ],
    ]);
    $responseBody = $maxResponseBytes > 0
        ? @file_get_contents($url, false, $context, 0, $maxResponseBytes + 1)
        : @file_get_contents($url, false, $context);
    $status = 0;
    foreach ($http_response_header ?? [] as $responseHeader) {
        if (preg_match('/^HTTP\/\S+\s+(\d{3})/', $responseHeader, $matches) === 1) {
            $status = (int) $matches[1];
        }
    }
    if ($responseBody === false) {
        throw new RuntimeException('remote request failed');
    }
    if ($maxResponseBytes > 0 && strlen($responseBody) > $maxResponseBytes) {
        throw new RuntimeException('remote response is too large');
    }
    return ['status' => $status, 'body' => (string) $responseBody];
}

/** @param array<string, mixed> $config */
function validateConfig(array $config): array
{
    if (!function_exists('sodium_crypto_sign_verify_detached')) {
        throw new RuntimeException('PHP sodium extension is required');
    }
    $settings = [
        'app_id' => configValue($config, 'QQ_BOT_APP_ID', 'qq_bot_app_id'),
        'app_secret' => configValue($config, 'QQ_BOT_APP_SECRET', 'qq_bot_app_secret'),
        'new_api_url' => rtrim(configValue($config, 'NEW_API_URL', 'new_api_url'), '/'),
        'bot_secret' => configValue($config, 'STARRY_API_BOT_SECRET', 'starry_api_bot_secret'),
        'token_cache_file' => configValue($config, 'QQ_BOT_TOKEN_CACHE_FILE', 'qq_bot_token_cache_file'),
        'public_url' => rtrim(configValue($config, 'QQ_BOT_PUBLIC_URL', 'qq_bot_public_url'), '/'),
        'gptstatus_state_file' => configValue($config, 'GPTSTATUS_STATE_FILE', 'gptstatus_state_file'),
        'gptstatus_cache_dir' => configValue($config, 'GPTSTATUS_CACHE_DIR', 'gptstatus_cache_dir'),
        'gptstatus_image_url' => rtrim(configValue($config, 'GPTSTATUS_IMAGE_URL', 'gptstatus_image_url'), '/'),
    ];
    if ($settings['app_id'] === '' || $settings['app_secret'] === '') {
        throw new RuntimeException('QQ_BOT_APP_ID and QQ_BOT_APP_SECRET are required');
    }
    if ($settings['new_api_url'] === '' || $settings['bot_secret'] === '') {
        throw new RuntimeException('NEW_API_URL and STARRY_API_BOT_SECRET are required');
    }
    if (strlen($settings['bot_secret']) < 32) {
        throw new RuntimeException('STARRY_API_BOT_SECRET must be at least 32 characters');
    }
    $parts = parse_url($settings['new_api_url']);
    if (!is_array($parts) || empty($parts['host']) || !empty($parts['user']) || !empty($parts['query']) || !empty($parts['fragment'])) {
        throw new RuntimeException('NEW_API_URL is invalid');
    }
    $scheme = strtolower((string) ($parts['scheme'] ?? ''));
    $host = strtolower((string) $parts['host']);
    $loopback = $host === 'localhost' || $host === '127.0.0.1' || $host === '::1';
    if ($scheme !== 'https' && !($scheme === 'http' && $loopback)) {
        throw new RuntimeException('NEW_API_URL must use HTTPS, except for loopback development');
    }
    if ($settings['token_cache_file'] === '') {
        $settings['token_cache_file'] = sys_get_temp_dir() . '/starryapi-qqbot-token-' . hash('sha256', $settings['app_id']) . '.json';
    }
    if ($settings['gptstatus_cache_dir'] === '') {
        $settings['gptstatus_cache_dir'] = sys_get_temp_dir() . '/starryapi-qqbot-gptstatus-' . hash('sha256', $settings['app_id']);
    }
    if ($settings['gptstatus_state_file'] === '') {
        $settings['gptstatus_state_file'] = rtrim($settings['gptstatus_cache_dir'], DIRECTORY_SEPARATOR) . DIRECTORY_SEPARATOR . 'state.json';
    }
    if ($settings['gptstatus_image_url'] === '' && $settings['public_url'] !== '') {
        $settings['gptstatus_image_url'] = $settings['public_url'] . '/gptstatus-image.php';
    }
    if ($settings['public_url'] !== '') {
        $publicParts = parse_url($settings['public_url']);
        $publicHost = is_array($publicParts) ? strtolower((string) ($publicParts['host'] ?? '')) : '';
        $publicScheme = is_array($publicParts) ? strtolower((string) ($publicParts['scheme'] ?? '')) : '';
        $publicLoopback = in_array($publicHost, ['localhost', '127.0.0.1', '::1'], true);
        if (!is_array($publicParts) || $publicHost === '' || !empty($publicParts['user']) || !empty($publicParts['query']) || !empty($publicParts['fragment']) || ($publicScheme !== 'https' && !($publicScheme === 'http' && $publicLoopback))) {
            throw new RuntimeException('QQ_BOT_PUBLIC_URL must use HTTPS, except for loopback development');
        }
    }
    if ($settings['gptstatus_image_url'] !== '') {
        $imageParts = parse_url($settings['gptstatus_image_url']);
        $imageHost = is_array($imageParts) ? strtolower((string) ($imageParts['host'] ?? '')) : '';
        $imageScheme = is_array($imageParts) ? strtolower((string) ($imageParts['scheme'] ?? '')) : '';
        $imageLoopback = in_array($imageHost, ['localhost', '127.0.0.1', '::1'], true);
        if (!is_array($imageParts) || $imageHost === '' || !empty($imageParts['user']) || !empty($imageParts['query']) || !empty($imageParts['fragment']) || ($imageScheme !== 'https' && !($imageScheme === 'http' && $imageLoopback))) {
            throw new RuntimeException('GPTSTATUS_IMAGE_URL must use HTTPS, except for loopback development');
        }
    }
    return $settings;
}

/** @return array{public:string, secret:string} */
function signingKeyPair(string $secret): array
{
    static $cache = [];
    if (isset($cache[$secret])) {
        return $cache[$secret];
    }
    $seed = $secret;
    while (strlen($seed) < SODIUM_CRYPTO_SIGN_SEEDBYTES) {
        $seed .= $seed;
    }
    $keyPair = sodium_crypto_sign_seed_keypair(substr($seed, 0, SODIUM_CRYPTO_SIGN_SEEDBYTES));
    return $cache[$secret] = [
        'public' => sodium_crypto_sign_publickey($keyPair),
        'secret' => sodium_crypto_sign_secretkey($keyPair),
    ];
}

function verifyQQSignature(string $appSecret, string $timestamp, string $signature, string $body): bool
{
    if ($timestamp === '' || strlen($signature) !== SODIUM_CRYPTO_SIGN_BYTES * 2 || !ctype_xdigit($signature)) {
        return false;
    }
    $signatureBytes = hex2bin($signature);
    if ($signatureBytes === false) {
        return false;
    }
    $keyPair = signingKeyPair($appSecret);
    return sodium_crypto_sign_verify_detached($signatureBytes, $timestamp . $body, $keyPair['public']);
}

/** @param array<string, mixed> $settings */
function getQQAccessToken(array $settings): string
{
    $path = $settings['token_cache_file'];
    $directory = dirname($path);
    if (!is_dir($directory)) {
        @mkdir($directory, 0700, true);
    }
    $handle = @fopen($path, 'c+');
    if ($handle === false) {
        throw new RuntimeException('could not open token cache');
    }
    @chmod($path, 0600);
    $locked = false;
    try {
        if (!flock($handle, LOCK_EX)) {
            throw new RuntimeException('could not lock token cache');
        }
        $locked = true;
        rewind($handle);
        $cached = json_decode((string) stream_get_contents($handle), true);
        if (is_array($cached) && is_string($cached['access_token'] ?? null) && (int) ($cached['expires_at'] ?? 0) > time() + STARRY_API_TOKEN_SKEW_SECONDS) {
            return $cached['access_token'];
        }
        $response = httpJsonRequest('POST', 'https://bots.qq.com/app/getAppAccessToken', ['Content-Type: application/json'], [
            'appId' => $settings['app_id'],
            'clientSecret' => $settings['app_secret'],
        ]);
        if ($response['status'] < 200 || $response['status'] >= 300) {
            throw new RuntimeException('QQ token request returned HTTP ' . $response['status']);
        }
        $result = json_decode($response['body'], true, 16, JSON_THROW_ON_ERROR);
        if (!is_array($result)) {
            throw new RuntimeException('QQ token response is invalid');
        }
        if (isset($result['code']) && (int) $result['code'] !== 0) {
            throw new RuntimeException('QQ token response returned an error');
        }
        $token = (string) ($result['access_token'] ?? '');
        $expiresIn = (int) ($result['expires_in'] ?? 0);
        if ($token === '' || $expiresIn <= 0) {
            throw new RuntimeException('QQ token response is invalid');
        }
        ftruncate($handle, 0);
        rewind($handle);
        fwrite($handle, json_encode(['access_token' => $token, 'expires_at' => time() + $expiresIn], JSON_THROW_ON_ERROR));
        fflush($handle);
        @chmod($path, 0600);
        return $token;
    } finally {
        if ($locked) {
            flock($handle, LOCK_UN);
        }
        fclose($handle);
    }
}

function gptstatusEnsureDirectory(string $path): void
{
    $directory = dirname($path);
    if (!is_dir($directory)) {
        if (!@mkdir($directory, 0700, true) && !is_dir($directory)) {
            throw new RuntimeException('could not create GPT status cache directory');
        }
    }
    if ($directory !== '' && $directory !== '.') {
        @chmod($directory, 0700);
    }
}

/** @return array<string, mixed> */
function loadGptStatusState(array $settings): array
{
    $state = [];
    $path = $settings['gptstatus_state_file'];
    if (is_file($path)) {
        $decoded = json_decode((string) @file_get_contents($path), true);
        if (is_array($decoded)) {
            $state = $decoded;
        }
    }
    if (!is_string($state['image_token'] ?? null)
        || strlen($state['image_token']) !== 48
        || !ctype_xdigit($state['image_token'])) {
        $state['image_token'] = bin2hex(random_bytes(24));
    }
    return $state;
}

/** @param array<string, mixed> $state */
function saveGptStatusState(array $settings, array $state): void
{
    $path = $settings['gptstatus_state_file'];
    gptstatusEnsureDirectory($path);
    $temporary = $path . '.tmp.' . getmypid();
    $encoded = json_encode($state, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR);
    if (@file_put_contents($temporary, $encoded, LOCK_EX) === false || !@rename($temporary, $path)) {
        @unlink($temporary);
        throw new RuntimeException('could not save GPT status cache');
    }
    @chmod($path, 0600);
}

function gptstatusText($value, int $limit = 240): string
{
    $text = trim((string) $value);
    if (function_exists('mb_strlen') && function_exists('mb_substr')) {
        if (mb_strlen($text, 'UTF-8') > $limit) {
            $text = mb_substr($text, 0, max(0, $limit - 3), 'UTF-8') . '...';
        }
        return $text;
    }
    if (strlen($text) > $limit) {
        $text = substr($text, 0, max(0, $limit - 3));
        while ($text !== '' && @preg_match('//u', $text) !== 1) {
            $text = substr($text, 0, -1);
        }
        $text .= '...';
    }
    return $text;
}

/** @return array{indicator:string,description:string,components:array<int,array{name:string,status:string}>,incidents:array<int,array<string,string>>} */
function parseGptStatusHtml(string $html): array
{
    $indicatorPattern = '(operational|degraded|partial-outage|full-outage|maintenance)';
    $headsUpStart = strpos($html, 'data-testid="heads-up"');
    $systemStatusStart = $headsUpStart === false ? false : strpos($html, 'data-testid="system-status"', $headsUpStart);
    if ($headsUpStart === false || $systemStatusStart === false || $systemStatusStart - $headsUpStart > 32768) {
        throw new RuntimeException('OpenAI status page did not contain its status sections');
    }
    $headsUpHtml = substr($html, $headsUpStart, $systemStatusStart - $headsUpStart);
    $headsUpPattern = '~text-icon-' . $indicatorPattern . '\\b[\\s\\S]{0,8192}?</svg>\\s*([^<]+)</div>~i';
    if (preg_match($headsUpPattern, $headsUpHtml, $headsUp) !== 1) {
        throw new RuntimeException('OpenAI status page did not contain its status banner');
    }
    $statusMap = [
        'operational' => 'operational',
        'degraded' => 'degraded_performance',
        'partial-outage' => 'partial_outage',
        'full-outage' => 'major_outage',
        'maintenance' => 'under_maintenance',
    ];
    $indicator = strtolower($headsUp[1]);
    $description = html_entity_decode(trim($headsUp[2]), ENT_QUOTES | ENT_HTML5, 'UTF-8');
    if ($description === '') {
        throw new RuntimeException('OpenAI status page has an empty status banner');
    }

    $componentHtml = substr($html, $systemStatusStart);
    if (preg_match_all('~<h3\b[^>]*>(.*?)</h3>~is', $componentHtml, $headings, PREG_OFFSET_CAPTURE) === false || count($headings[0]) === 0) {
        throw new RuntimeException('OpenAI status page did not contain its component list');
    }
    $componentsByName = [];
    foreach ($headings[0] as $heading) {
        $headingPosition = $heading[1];
        $name = trim(html_entity_decode(strip_tags($heading[0]), ENT_QUOTES | ENT_HTML5, 'UTF-8'));
        $name = preg_replace('/\\s+/u', ' ', $name) ?? '';
        if ($name === '' || strlen($name) > 160) {
            continue;
        }
        $headingPrefix = substr($componentHtml, max(0, $headingPosition - 2048), min(2048, $headingPosition));
        if (preg_match_all('~class="[^"]*\\btext-icon-' . $indicatorPattern . '\\b[^"]*"~i', $headingPrefix, $componentStatuses) === false || count($componentStatuses[1]) === 0) {
            continue;
        }
        $componentStatus = strtolower((string) end($componentStatuses[1]));
        $componentsByName[$name] = ['name' => $name, 'status' => $statusMap[$componentStatus] ?? 'unknown'];
        if (count($componentsByName) >= GPTSTATUS_MAX_COMPONENTS) {
            break;
        }
    }
    if (!isset($componentsByName['ChatGPT'])) {
        throw new RuntimeException('OpenAI status page did not contain the ChatGPT service status');
    }

    $bannerDetails = '';
    if (preg_match('~class="text-slate-900 dark:text-slate-50 p-4">([^<]*)</div>~i', $headsUpHtml, $details) === 1) {
        $bannerDetails = html_entity_decode(trim($details[1]), ENT_QUOTES | ENT_HTML5, 'UTF-8');
    }
    if ($bannerDetails !== '') {
        $description .= ' — ' . $bannerDetails;
    }

    return [
        'indicator' => $statusMap[$indicator] ?? 'unknown',
        'description' => gptstatusText($description, 240),
        'components' => array_values($componentsByName),
        'incidents' => [],
    ];
}

/** @return array{fingerprint:string,summary:array<string,mixed>,fetched_at:int} */
function fetchGptStatus(): array
{
    $response = httpJsonRequest('GET', GPTSTATUS_PAGE_URL, [
        'Accept: text/html',
        'User-Agent: StarryAPI-QQBot/1.0',
    ], null, 12, GPTSTATUS_MAX_RESPONSE_BYTES);
    if ($response['status'] < 200 || $response['status'] >= 300) {
        throw new RuntimeException('OpenAI status page returned HTTP ' . $response['status']);
    }
    if (stripos($response['body'], '<html') === false) {
        throw new RuntimeException('OpenAI status page response is not HTML');
    }
    $summary = parseGptStatusHtml($response['body']);
    $fingerprint = hash('sha256', json_encode($summary, JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR));
    return ['fingerprint' => $fingerprint, 'summary' => $summary, 'fetched_at' => time()];
}

function gptstatusImageText(string $value): string
{
    if (function_exists('iconv')) {
        $converted = @iconv('UTF-8', 'ASCII//TRANSLIT//IGNORE', $value);
        if (is_string($converted)) {
            $value = $converted;
        }
    }
    $value = preg_replace('/[^\x20-\x7E]/', '?', $value) ?? '';
    return trim($value);
}

/** @param array<string, mixed> $status */
function renderGptStatusPng(array $status): string
{
    if (!function_exists('imagecreatetruecolor') || !function_exists('imagepng')) {
        throw new RuntimeException('PHP GD extension is required for GPT status images');
    }
    $summary = is_array($status['summary'] ?? null) ? $status['summary'] : [];
    $indicator = strtolower((string) ($summary['indicator'] ?? 'unknown'));
    $colors = [
        'operational' => [15, 157, 88],
        'degraded_performance' => [217, 119, 6],
        'partial_outage' => [217, 119, 6],
        'under_maintenance' => [124, 58, 237],
        'major_outage' => [220, 38, 38],
        'minor' => [217, 119, 6],
        'major' => [220, 38, 38],
        'critical' => [124, 58, 237],
    ];
    [$red, $green, $blue] = $colors[$indicator] ?? [100, 116, 139];
    $width = 1200;
    $components = is_array($summary['components'] ?? null) ? $summary['components'] : [];
    $incidents = is_array($summary['incidents'] ?? null) ? $summary['incidents'] : [];
    $componentCount = min(count($components), GPTSTATUS_MAX_COMPONENTS);
    $incidentCount = min(count($incidents), GPTSTATUS_MAX_INCIDENTS);
    $height = 195 + $componentCount * 31 + ($incidentCount > 0 ? 58 + $incidentCount * 42 : 0);
    $image = imagecreatetruecolor($width, $height);
    if ($image === false) {
        throw new RuntimeException('could not create GPT status image');
    }
    $white = imagecolorallocate($image, 255, 255, 255);
    $dark = imagecolorallocate($image, 15, 23, 42);
    $muted = imagecolorallocate($image, 100, 116, 139);
    $line = imagecolorallocate($image, 226, 232, 240);
    $accent = imagecolorallocate($image, $red, $green, $blue);
    $soft = imagecolorallocate($image, 247, 249, 252);
    imagefilledrectangle($image, 0, 0, $width, $height, $white);
    imagefilledrectangle($image, 0, 0, $width, 105, $accent);
    imagestring($image, 5, 44, 28, 'OpenAI / GPT Status', $white);
    imagestring($image, 5, $width - imagefontwidth(5) * strlen(strtoupper($indicator)) - 44, 28, strtoupper(gptstatusImageText($indicator)), $white);
    imagestring($image, 5, 44, 74, gptstatusImageText(gptstatusText($summary['description'] ?? 'Unknown', 120)), $white);
    $y = 133;
    $rows = [];
    foreach (array_slice($components, 0, GPTSTATUS_MAX_COMPONENTS) as $index => $component) {
        if (!is_array($component)) {
            continue;
        }
        $rows[] = [
            gptstatusImageText(gptstatusText($component['name'] ?? '', 105)),
            gptstatusImageText(gptstatusText($component['status'] ?? 'unknown', 32)),
        ];
    }
    foreach ($rows as [$name, $componentStatus]) {
        imagefilledrectangle($image, 32, $y - 4, $width - 32, $y + 24, $soft);
        imagestring($image, 4, 44, $y, substr($name, 0, 112), $dark);
        $statusWidth = imagefontwidth(4) * strlen($componentStatus);
        imagestring($image, 4, $width - 44 - $statusWidth, $y, $componentStatus, $muted);
        $y += 31;
    }
    if ($incidentCount > 0) {
        $y += 12;
        imagestring($image, 5, 44, $y, 'Active incidents', $dark);
        $y += 28;
        foreach (array_slice($incidents, 0, GPTSTATUS_MAX_INCIDENTS) as $incident) {
            if (!is_array($incident)) {
                continue;
            }
            $incidentText = gptstatusImageText(gptstatusText($incident['name'] ?? '', 112));
            $incidentStatus = gptstatusImageText(gptstatusText($incident['status'] ?? '', 28));
            imagestring($image, 3, 52, $y, substr($incidentText, 0, 118), $dark);
            imagestring($image, 3, 52, $y + 17, $incidentStatus, $muted);
            $y += 42;
        }
    }
    imagestring($image, 2, 44, $height - 18, 'Source: status.openai.com | ' . date('Y-m-d H:i:s T'), $muted);
    ob_start();
    imagepng($image);
    imagedestroy($image);
    return (string) ob_get_clean();
}

function buildGptStatusImageUrl(array $settings, array $state, string $fingerprint = ''): string
{
    $base = $settings['gptstatus_image_url'];
    if ($base === '') {
        throw new RuntimeException('QQ_BOT_PUBLIC_URL or GPTSTATUS_IMAGE_URL is required for status images');
    }
    $path = parse_url($base, PHP_URL_PATH);
    if (!is_string($path) || $path === '' || !is_string($state['image_token'] ?? null)) {
        throw new RuntimeException('GPT status image URL is invalid');
    }
    $expires = (string) (time() + 900);
    $version = $fingerprint !== '' ? $fingerprint : (string) ($state['fingerprint'] ?? '');
    $signature = hash_hmac('sha256', $path . "\n" . $expires . "\n" . $version, $state['image_token']);
    $query = http_build_query(['exp' => $expires, 'v' => $version, 'sig' => $signature], '', '&', PHP_QUERY_RFC3986);
    return $base . '?' . $query;
}

/** @param array<string, mixed> $query
 *  @param array<string, mixed> $state
 */
function isValidGptStatusImageRequest(array $settings, array $state, array $query, string $requestPath): bool
{
    $expectedPath = parse_url($settings['gptstatus_image_url'], PHP_URL_PATH);
    $expiresText = $query['exp'] ?? null;
    $version = $query['v'] ?? null;
    $signature = $query['sig'] ?? null;
    $now = time();
    if (!is_string($expectedPath)
        || $expectedPath === ''
        || $requestPath !== $expectedPath
        || !is_string($expiresText)
        || !is_string($version)
        || !is_string($signature)
        || !ctype_digit($expiresText)
        || strlen($version) !== 64
        || !ctype_xdigit($version)
        || strlen($signature) !== 64
        || !ctype_xdigit($signature)
        || !is_string($state['image_token'] ?? null)) {
        return false;
    }
    $expires = (int) $expiresText;
    if ($expires < $now || $expires > $now + 960) {
        return false;
    }
    $canonical = $requestPath . "\n" . $expiresText . "\n" . $version;
    $expectedSignature = hash_hmac('sha256', $canonical, $state['image_token']);
    return hash_equals($expectedSignature, strtolower($signature));
}

/** Sign the exact HTTP body using the bot-to-NewAPI request protocol. */
function signInviteRequest(string $secret, string $method, string $path, string $timestamp, string $nonce, string $body): string
{
    $bodyDigest = hash('sha256', $body);
    $canonical = implode("\n", [$method, $path, $timestamp, $nonce, $bodyDigest]);
    return hash_hmac('sha256', $canonical, $secret);
}

/** @param array<string, mixed> $settings */
function claimInvite(array $settings, string $openID, string $email): void
{
    $path = '/api/bot/registration-invite';
    $body = json_encode(['open_id' => $openID, 'email' => $email], JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR);
    $timestamp = (string) time();
    try {
        $nonce = bin2hex(random_bytes(16));
    } catch (Throwable $error) {
        throw new RuntimeException('could not create invite request', 0, $error);
    }
    $requestSignature = signInviteRequest($settings['bot_secret'], 'POST', $path, $timestamp, $nonce, $body);
    $response = httpJsonRequest('POST', $settings['new_api_url'] . '/api/bot/registration-invite', [
        'Content-Type: application/json',
        'X-QQ-Bot-Timestamp: ' . $timestamp,
        'X-QQ-Bot-Nonce: ' . $nonce,
        'X-QQ-Bot-Signature: ' . $requestSignature,
    ], $body);
    if ($response['status'] === 429) {
        throw new RuntimeException('invite email rate limited');
    }
    if ($response['status'] < 200 || $response['status'] >= 300) {
        throw new RuntimeException('invite service returned HTTP ' . $response['status']);
    }
    $result = json_decode($response['body'], true, 16, JSON_THROW_ON_ERROR);
    if (!is_array($result)) {
        throw new RuntimeException('invite service returned invalid data');
    }
    $data = $result['data'] ?? null;
    if (($result['success'] ?? false) !== true || !is_array($data) || ($data['sent'] ?? false) !== true) {
        throw new RuntimeException('invite email was not sent');
    }
}

/** @param array<string, mixed> $settings */
function sendQQMessage(array $settings, string $path, string $messageID, string $content): void
{
    $token = getQQAccessToken($settings);
    $response = httpJsonRequest('POST', 'https://api.sgroup.qq.com' . $path, [
        'Authorization: QQBot ' . $token,
        'X-Union-Appid: ' . $settings['app_id'],
        'Content-Type: application/json',
    ], [
        'content' => $content,
        'msg_id' => $messageID,
        'timestamp' => (int) round(microtime(true) * 1000),
        'message_reference' => [
            'message_id' => $messageID,
            'ignore_get_message_error' => true,
        ],
    ]);
    if ($response['status'] < 200 || $response['status'] >= 300) {
        throw new RuntimeException('QQ message request returned HTTP ' . $response['status']);
    }
}

/** @param array<string, mixed> $settings */
function sendQQReply(array $settings, string $openID, string $messageID, string $content): void
{
    sendQQMessage($settings, '/v2/users/' . rawurlencode($openID) . '/messages', $messageID, $content);
}

/** @param array<string, mixed> $settings */
function sendQQGroupReply(array $settings, string $groupID, string $messageID, string $content): void
{
    sendQQMessage($settings, '/v2/groups/' . rawurlencode($groupID) . '/messages', $messageID, $content);
}

/** @param array<string, mixed> $settings */
function sendQQGroupImage(array $settings, string $groupID, string $messageID, string $imageURL, string $content = ''): void
{
    $token = getQQAccessToken($settings);
    $headers = [
        'Authorization: QQBot ' . $token,
        'X-Union-Appid: ' . $settings['app_id'],
        'Content-Type: application/json',
    ];

    $upload = httpJsonRequest('POST', 'https://api.sgroup.qq.com/v2/groups/' . rawurlencode($groupID) . '/files', $headers, [
        'file_type' => 1,
        'url' => $imageURL,
        'srv_send_msg' => false,
    ], 10);
    if ($upload['status'] < 200 || $upload['status'] >= 300) {
        throw new RuntimeException('QQ group image upload returned HTTP ' . $upload['status']);
    }
    $uploadResult = json_decode($upload['body'], true, 16, JSON_THROW_ON_ERROR);
    $fileInfo = is_array($uploadResult) ? ($uploadResult['file_info'] ?? ($uploadResult['data']['file_info'] ?? null)) : null;
    if (!is_string($fileInfo) || $fileInfo === '') {
        throw new RuntimeException('QQ group image upload returned no file_info');
    }
    $message = [
        'msg_type' => 7,
        'media' => ['file_info' => $fileInfo],
    ];
    if ($content !== '') {
        $message['content'] = $content;
    }
    $message['msg_id'] = $messageID;
    $message['message_reference'] = [
        'message_id' => $messageID,
        'ignore_get_message_error' => true,
    ];
    $sent = httpJsonRequest('POST', 'https://api.sgroup.qq.com/v2/groups/' . rawurlencode($groupID) . '/messages', $headers, $message, 10);
    if ($sent['status'] < 200 || $sent['status'] >= 300) {
        throw new RuntimeException('QQ group image message returned HTTP ' . $sent['status']);
    }
}

/** @return array{0:string,1:bool} */
function parseCommand(string $content): array
{
    if (strlen($content) > 256) {
        return ['', false];
    }
    $fields = preg_split('/\s+/u', trim($content), -1, PREG_SPLIT_NO_EMPTY);
    if (!is_array($fields) || count($fields) === 0) {
        return ['', false];
    }
    if (strtolower((string) $fields[0]) !== '/sapi') {
        $startsWithSlash = substr((string) $fields[0], 0, 1) === '/';
        return [$startsWithSlash ? 'unknown' : '', $startsWithSlash];
    }
    if (count($fields) === 1 || (count($fields) === 2 && strtolower((string) $fields[1]) === 'help')) {
        return ['help', true];
    }
    if ((string) $fields[1] === '邀请码') {
        return count($fields) === 3 ? ['invite', true] : ['invite_usage', true];
    }
    return ['unknown', true];
}

function isValidInviteEmail(string $email): bool
{
    return $email !== ''
        && $email === trim($email)
        && strlen($email) <= 50
        && filter_var($email, FILTER_VALIDATE_EMAIL) !== false;
}

/** @return array{0:string,1:bool} */
function parseGroupCommand(string $content): array
{
    return parseCommand(stripQQGroupMentions($content));
}

function stripQQGroupMentions(string $content): string
{
    $content = trim($content);
    while (substr($content, 0, 2) === '<@') {
        $separator = strpos($content, '>');
        if ($separator === false) {
            return '';
        }
        $content = trim(substr($content, $separator + 1));
    }
    return $content;
}

function groupCommandReply(string $content): ?string
{
    [$command, $matched] = parseGroupCommand($content);
    if (!$matched) {
        return null;
    }
    if ($command === 'unknown') {
        return '指令不存在';
    }
    if ($command === 'invite_usage') {
        return '用法：/sapi 邀请码 邮箱';
    }
    return "StarryAPI bot\n/sapi - 显示帮助\n/sapi 邀请码 邮箱 - 邮件获取注册邀请码（每个 QQ 账号一个）\n/gptstatus - 群聊查询 GPT 状态图片";
}

/** @param array<string, mixed> $settings */
function inviteEmailCommandReply(array $settings, string $openID, string $content): string
{
    $fields = preg_split('/\\s+/u', trim($content), -1, PREG_SPLIT_NO_EMPTY);
    $email = is_array($fields) && isset($fields[2]) ? (string) $fields[2] : '';
    if (!is_array($fields) || count($fields) !== 3 || !isValidInviteEmail($email)) {
        return '邮箱格式不正确，请使用：/sapi 邀请码 邮箱';
    }
    try {
        claimInvite($settings, $openID, $email);
        return '邀请码邮件已发送，请查收。';
    } catch (Throwable $error) {
        error_log('StarryAPI QQ bot invite email delivery failed: ' . $error->getMessage());
        return $error->getMessage() === 'invite email rate limited'
            ? '请求过于频繁，请 1 分钟后重试。'
            : '邀请码邮件发送失败，请检查邮箱或稍后重试。';
    }
}

/** @return array{command:?string,matched:bool} */
function parseGroupGPTStatusCommand(string $content): array
{
    $content = trim($content);
    while (substr($content, 0, 2) === '<@') {
        $separator = strpos($content, '>');
        if ($separator === false) {
            return ['command' => null, 'matched' => false];
        }
        $content = trim(substr($content, $separator + 1));
    }
    if (strlen($content) > 256) {
        return ['command' => null, 'matched' => false];
    }
    $fields = preg_split('/\s+/u', $content, -1, PREG_SPLIT_NO_EMPTY);
    if (!is_array($fields) || count($fields) === 0 || strtolower((string) $fields[0]) !== '/gptstatus') {
        return ['command' => null, 'matched' => false];
    }
    if (count($fields) === 1) {
        return ['command' => 'status', 'matched' => true];
    }
    if (count($fields) === 2 && strtolower((string) $fields[1]) === 'help') {
        return ['command' => 'help', 'matched' => true];
    }
    return ['command' => 'unknown', 'matched' => true];
}

function gptStatusCaption(array $status): string
{
    $summary = is_array($status['summary'] ?? null) ? $status['summary'] : [];
    $description = gptstatusText($summary['description'] ?? 'Unknown', 180);
    return 'OpenAI / GPT status: ' . $description . "\nSource: https://status.openai.com/";
}

/** @param array<string,mixed> $fresh */
function recordGptStatusSnapshot(array $settings, array $fresh): array
{
    $path = $settings['gptstatus_state_file'];
    gptstatusEnsureDirectory($path);
    $lockPath = $path . '.lock';
    $lock = @fopen($lockPath, 'c+');
    if ($lock === false) {
        throw new RuntimeException('could not open GPT status cache lock');
    }
    @chmod($lockPath, 0600);
    try {
        if (!flock($lock, LOCK_EX)) {
            throw new RuntimeException('could not lock GPT status cache');
        }
        $previous = loadGptStatusState($settings);
        $state = [
            'image_token' => $previous['image_token'],
            'status' => $fresh,
            'fingerprint' => $fresh['fingerprint'],
        ];
        saveGptStatusState($settings, $state);
        return $state;
    } finally {
        flock($lock, LOCK_UN);
        fclose($lock);
    }
}

function handleGroupGPTStatusCommand(array $settings, string $groupID, string $messageID, string $content): bool
{
    $parsed = parseGroupGPTStatusCommand($content);
    if (!$parsed['matched']) {
        return false;
    }
    if ($parsed['command'] === 'help') {
        sendQQGroupReply($settings, $groupID, $messageID, '用法：/gptstatus 查询并发送 GPT 状态图片。');
        return true;
    }
    if ($parsed['command'] === 'unknown') {
        sendQQGroupReply($settings, $groupID, $messageID, '指令不存在');
        return true;
    }
    if ($settings['gptstatus_image_url'] === '') {
        sendQQGroupReply($settings, $groupID, $messageID, '状态图片功能尚未配置，请管理员设置 QQ_BOT_PUBLIC_URL 后重试。');
        return true;
    }
    try {
        $status = fetchGptStatus();
        $state = recordGptStatusSnapshot($settings, $status);
        sendQQGroupImage($settings, $groupID, $messageID, buildGptStatusImageUrl($settings, $state), gptStatusCaption($status));
        return true;
    } catch (Throwable $error) {
        error_log('StarryAPI QQ bot GPT status command failed: ' . $error->getMessage());
        sendQQGroupReply($settings, $groupID, $messageID, '获取 GPT 状态图片失败，请检查 Bot 图片配置或稍后重试。');
        return true;
    }
}

/** @param array<string, mixed> $settings */
function handleC2CMessage(array $settings, array $payload): bool
{
    if (($payload['t'] ?? '') !== 'C2C_MESSAGE_CREATE') {
        return true;
    }
    $data = $payload['d'] ?? null;
    if (!is_array($data) || !is_array($data['author'] ?? null)) {
        // Ignore malformed/non-user dispatches without asking QQ to retry
        // them forever. A send failure below still returns false for retry.
        return true;
    }
    $messageID = (string) ($data['id'] ?? '');
    $openID = (string) ($data['author']['id'] ?? $data['author']['user_openid'] ?? '');
    $content = (string) ($data['content'] ?? '');
    if ($messageID === '' || $openID === '') {
        return true;
    }
    [$command, $matched] = parseCommand($content);
    if (!$matched) {
        return true;
    }
    $reply = 'unknown' === $command
        ? '指令不存在'
        : ('invite_usage' === $command
            ? '用法：/sapi 邀请码 邮箱'
            : "StarryAPI bot\n/sapi - 显示帮助\n/sapi 邀请码 邮箱 - 私聊或群聊@机器人获取邀请码（每个 QQ 账号一个，邮件发送）\n/gptstatus - 群聊查询 GPT 状态图片");
    if ($command === 'invite') {
        $fields = preg_split('/\s+/u', trim($content), -1, PREG_SPLIT_NO_EMPTY);
        $email = is_array($fields) && isset($fields[2]) ? (string) $fields[2] : '';
        if (!isValidInviteEmail($email)) {
            $reply = '邮箱格式不正确，请使用：/sapi 邀请码 邮箱';
        } else {
            try {
                claimInvite($settings, $openID, $email);
                $reply = '邀请码邮件已发送，请查收。';
            } catch (Throwable $error) {
                error_log('StarryAPI QQ bot invite email delivery failed: ' . $error->getMessage());
                $reply = $error->getMessage() === 'invite email rate limited'
                    ? '请求过于频繁，请 1 分钟后重试。'
                    : '邀请码邮件发送失败，请检查邮箱或稍后重试。';
            }
        }
    }
    try {
        sendQQReply($settings, $openID, $messageID, $reply);
    } catch (Throwable $error) {
        error_log('StarryAPI QQ bot message send failed: ' . $error->getMessage());
        return false;
    }
    return true;
}

/** @param array<string, mixed> $settings */
function handleGroupMessage(array $settings, array $payload): bool
{
    if (($payload['t'] ?? '') !== 'GROUP_AT_MESSAGE_CREATE') {
        return true;
    }
    $data = $payload['d'] ?? null;
    if (!is_array($data) || !is_array($data['author'] ?? null)) {
        return true;
    }
    $messageID = (string) ($data['id'] ?? '');
    $groupID = (string) ($data['group_openid'] ?? $data['group_id'] ?? '');
    $openID = (string) ($data['author']['id'] ?? $data['author']['user_openid'] ?? '');
    $content = (string) ($data['content'] ?? '');
    if ($messageID === '' || $groupID === '' || $openID === '') {
        return true;
    }
    $gptStatusCommand = parseGroupGPTStatusCommand($content);
    if ($gptStatusCommand['matched']) {
        try {
            return handleGroupGPTStatusCommand($settings, $groupID, $messageID, $content);
        } catch (Throwable $error) {
            error_log('StarryAPI QQ bot GPT status command failed: ' . $error->getMessage());
            try {
                sendQQGroupReply($settings, $groupID, $messageID, 'GPT 状态处理失败，请稍后重试。');
                return true;
            } catch (Throwable $sendError) {
                error_log('StarryAPI QQ bot GPT status error reply failed: ' . $sendError->getMessage());
                return false;
            }
        }
    }
    [$command, $matched] = parseGroupCommand($content);
    if ($matched && ($command === 'invite' || $command === 'invite_usage')) {
        $reply = $command === 'invite'
            ? inviteEmailCommandReply($settings, $openID, stripQQGroupMentions($content))
            : '用法：/sapi 邀请码 邮箱';
        try {
            sendQQGroupReply($settings, $groupID, $messageID, $reply);
        } catch (Throwable $error) {
            error_log('StarryAPI QQ bot group message send failed: ' . $error->getMessage());
            return false;
        }
        return true;
    }
    $reply = groupCommandReply($content);
    if ($reply === null) {
        return true;
    }
    try {
        sendQQGroupReply($settings, $groupID, $messageID, $reply);
    } catch (Throwable $error) {
        error_log('StarryAPI QQ bot group message send failed: ' . $error->getMessage());
        return false;
    }
    return true;
}

function main(): void
{
    $method = strtoupper((string) ($_SERVER['REQUEST_METHOD'] ?? 'GET'));
    $requestPath = parse_url((string) ($_SERVER['REQUEST_URI'] ?? '/'), PHP_URL_PATH);
    if ($method === 'GET' && is_string($requestPath) && preg_match('~(?:^|/)healthz/?$~', $requestPath) === 1) {
        respondText('', 204);
    }
    if ($method === 'GET') {
        respondText('method not allowed', 405);
    }
    if ($method !== 'POST') {
        header('Allow: GET, POST');
        respondText('method not allowed', 405);
    }

    try {
        $userConfig = loadUserConfig();
        $settings = validateConfig($userConfig);
    } catch (Throwable $error) {
        error_log('StarryAPI QQ bot configuration error: ' . $error->getMessage());
        respondText('QQ bot is not configured', 500);
    }

    $contentLength = (int) ($_SERVER['CONTENT_LENGTH'] ?? 0);
    if ($contentLength > STARRY_API_MAX_BODY_BYTES) {
        respondText('request body too large', 413);
    }
    // Read at most one byte beyond the limit so an oversized callback cannot
    // force PHP to buffer an unbounded request body.
    $body = file_get_contents('php://input', false, null, 0, STARRY_API_MAX_BODY_BYTES + 1);
    if ($body === false || $body === '' || strlen($body) > STARRY_API_MAX_BODY_BYTES) {
        respondText('invalid callback body', 400);
    }
    if (!verifyQQSignature($settings['app_secret'], requestHeader('X-Signature-Timestamp'), requestHeader('X-Signature-Ed25519'), $body)) {
        respondText('invalid callback signature', 401);
    }
    try {
        $payload = json_decode($body, true, 32, JSON_THROW_ON_ERROR);
    } catch (Throwable $error) {
        respondText('invalid callback payload', 400);
    }
    if (!is_array($payload)) {
        respondText('invalid callback payload', 400);
    }
    $opCode = (int) ($payload['op'] ?? -1);
    if ($opCode === 13) {
        $data = $payload['d'] ?? null;
        if (!is_array($data) || !is_string($data['plain_token'] ?? null) || !is_string($data['event_ts'] ?? null) || $data['plain_token'] === '' || $data['event_ts'] === '') {
            respondText('invalid callback challenge', 400);
        }
        $keys = signingKeyPair($settings['app_secret']);
        // QQ validates the response against event_ts + plain_token, not the
        // timestamp from the incoming callback signature header.
        $signature = bin2hex(sodium_crypto_sign_detached($data['event_ts'] . $data['plain_token'], $keys['secret']));
        respondJson(['plain_token' => $data['plain_token'], 'signature' => $signature]);
    }
    if ($opCode === 1) {
        $sequence = $payload['d'] ?? 0;
        if (!is_int($sequence) && !is_float($sequence) && !is_numeric($sequence)) {
            respondText('invalid heartbeat payload', 400);
        }
        respondJson(['op' => 11, 'd' => (int) $sequence]);
    }
    if ($opCode === 0) {
        $success = true;
        if (($payload['t'] ?? '') === 'C2C_MESSAGE_CREATE') {
            $success = handleC2CMessage($settings, $payload);
        } elseif (($payload['t'] ?? '') === 'GROUP_AT_MESSAGE_CREATE') {
            $success = handleGroupMessage($settings, $payload);
        }
        respondJson(['op' => 12, 'd' => $success ? 0 : 1]);
    }
    respondText('unsupported callback operation', 400);
}

if (debug_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS) === []) {
    main();
}
