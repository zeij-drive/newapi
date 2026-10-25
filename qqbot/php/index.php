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
function httpJsonRequest(string $method, string $url, array $headers, $payload, int $timeoutSeconds = 5): array
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
        curl_setopt_array($curl, [
            CURLOPT_CUSTOMREQUEST => $method,
            CURLOPT_HTTPHEADER => $headerLines,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_CONNECTTIMEOUT => min($timeoutSeconds, 5),
            CURLOPT_TIMEOUT => $timeoutSeconds,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_PROTOCOLS => CURLPROTO_HTTPS | CURLPROTO_HTTP,
            CURLOPT_REDIR_PROTOCOLS => CURLPROTO_HTTPS | CURLPROTO_HTTP,
            CURLOPT_SSL_VERIFYPEER => true,
            CURLOPT_SSL_VERIFYHOST => 2,
        ]);
        if ($body !== null) {
            curl_setopt($curl, CURLOPT_POSTFIELDS, $body);
        }
        $responseBody = curl_exec($curl);
        $status = (int) curl_getinfo($curl, CURLINFO_RESPONSE_CODE);
        $error = curl_error($curl);
        curl_close($curl);
        if ($responseBody === false || $error !== '') {
            throw new RuntimeException('remote request failed');
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
    $responseBody = @file_get_contents($url, false, $context);
    $status = 0;
    foreach ($http_response_header ?? [] as $responseHeader) {
        if (preg_match('/^HTTP\/\S+\s+(\d{3})/', $responseHeader, $matches) === 1) {
            $status = (int) $matches[1];
        }
    }
    if ($responseBody === false) {
        throw new RuntimeException('remote request failed');
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

/** Sign the exact HTTP body using the bot-to-NewAPI request protocol. */
function signInviteRequest(string $secret, string $method, string $path, string $timestamp, string $nonce, string $body): string
{
    $bodyDigest = hash('sha256', $body);
    $canonical = implode("\n", [$method, $path, $timestamp, $nonce, $bodyDigest]);
    return hash_hmac('sha256', $canonical, $secret);
}

/** @param array<string, mixed> $settings */
function claimInvite(array $settings, string $openID): string
{
    $path = '/api/bot/registration-invite';
    $body = json_encode(['open_id' => $openID], JSON_UNESCAPED_UNICODE | JSON_UNESCAPED_SLASHES | JSON_THROW_ON_ERROR);
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
    if ($response['status'] < 200 || $response['status'] >= 300) {
        throw new RuntimeException('invite service returned HTTP ' . $response['status']);
    }
    $result = json_decode($response['body'], true, 16, JSON_THROW_ON_ERROR);
    if (!is_array($result)) {
        throw new RuntimeException('invite service returned invalid data');
    }
    $data = $result['data'] ?? null;
    $code = is_array($data) ? (string) ($data['code'] ?? '') : '';
    if (($result['success'] ?? false) !== true || $code === '' || strlen($code) > 128) {
        throw new RuntimeException('invite service did not issue an invite code');
    }
    return $code;
}

/** @param array<string, mixed> $settings */
function sendQQReply(array $settings, string $openID, string $messageID, string $content): void
{
    $token = getQQAccessToken($settings);
    $response = httpJsonRequest('POST', 'https://api.sgroup.qq.com/v2/users/' . rawurlencode($openID) . '/messages', [
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

/** @return array{0:string,1:bool} */
function parseCommand(string $content): array
{
    if (strlen($content) > 256) {
        return ['', false];
    }
    $fields = preg_split('/\s+/u', trim($content), -1, PREG_SPLIT_NO_EMPTY);
    if (!is_array($fields) || count($fields) === 0 || strtolower((string) $fields[0]) !== '/sapi') {
        return ['', false];
    }
    if (count($fields) === 1 || strtolower((string) $fields[1]) === 'help') {
        return ['help', true];
    }
    if ((string) $fields[1] === '邀请码') {
        return ['invite', true];
    }
    return ['help', true];
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
    $reply = "StarryAPI bot\n/sapi - 显示帮助\n/sapi 邀请码 - 私聊领取注册邀请码（每个 QQ 账号一个）";
    if ($command === 'invite') {
        try {
            $code = claimInvite($settings, $openID);
            $reply = "StarryAPI 注册邀请码：\n{$code}\n该 QQ 账号重复领取时会返回同一个邀请码。";
        } catch (Throwable $error) {
            error_log('StarryAPI QQ bot invite claim failed: ' . $error->getMessage());
            $reply = '邀请码生成失败，请稍后重试。';
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
        }
        respondJson(['op' => 12, 'd' => $success ? 0 : 1]);
    }
    respondText('unsupported callback operation', 400);
}

if (debug_backtrace(DEBUG_BACKTRACE_IGNORE_ARGS) === []) {
    main();
}
