<?php
declare(strict_types=1);

require __DIR__ . '/../php/index.php';

$actual = signInviteRequest(
    'test-signature-secret',
    'POST',
    '/api/bot/registration-invite',
    '1700000000',
    '000102030405060708090a0b0c0d0e0f',
    '{"open_id":"qq-user","email":"user@example.com"}'
);
$expected = '9ee69929f0f7581957fbcbdf81bc66540131e8c274f1ad39568be41584bc5f6b';
if (!hash_equals($expected, $actual)) {
    throw new RuntimeException('PHP QQ bot signature protocol regression');
}

$groupMessage = [
    't' => 'GROUP_AT_MESSAGE_CREATE',
    'd' => [
        'id' => 'group-message-id',
        'content' => '<@!bot-id> /sapi 邀请码',
        'author' => ['id' => 'qq-user'],
    ],
];
if (!handleC2CMessage([], $groupMessage)) {
    throw new RuntimeException('PHP QQ bot must ignore group events in the C2C handler');
}
$groupInviteCommand = parseGroupCommand('<@!bot-id> /sapi 邀请码 user@example.com');
if ($groupInviteCommand !== ['invite', true]
    || !isValidInviteEmail('user@example.com')
    || strpos(groupCommandReply('<@!bot-id> /sapi'), '仅支持私聊') !== false) {
    throw new RuntimeException('PHP QQ bot must recognize group invite commands and allow group use');
}
if (groupCommandReply('<@!bot-id> /sapi') === null) {
    throw new RuntimeException('PHP QQ bot must recognize commands after a group mention');
}
if (groupCommandReply('<@!bot-id> /sapi typo') !== '指令不存在'
    || groupCommandReply('<@!bot-id> /unknown') !== '指令不存在') {
    throw new RuntimeException('PHP QQ bot must report unknown group commands');
}
$unknownPrivateCommand = parseCommand('/sapi typo');
if ($unknownPrivateCommand !== ['unknown', true]) {
    throw new RuntimeException('PHP QQ bot must identify invalid private-chat command syntax');
}
$inviteCommand = parseCommand('/sapi 邀请码 user@example.com');
if ($inviteCommand !== ['invite', true]
    || !isValidInviteEmail('user@example.com')
    || isValidInviteEmail('not-an-email')
    || isValidInviteEmail(' user@example.com')
    || isValidInviteEmail(str_repeat('a', 41) . '@example.com')) {
    throw new RuntimeException('PHP QQ bot must accept a valid email invite command and reject invalid email inputs');
}
if (parseCommand('/sapi 邀请码') !== ['invite_usage', true]
    || parseCommand('/sapi 邀请码 user@example.com extra') !== ['invite_usage', true]) {
    throw new RuntimeException('PHP QQ bot must show the invite syntax for missing or extra arguments');
}

$statusCommand = parseGroupGPTStatusCommand('<@!bot-id> /gptstatus');
if (!$statusCommand['matched'] || $statusCommand['command'] !== 'status') {
    throw new RuntimeException('PHP QQ bot must recognize the group status command after a mention');
}
$removedAutoCommand = parseGroupGPTStatusCommand('<@!bot-id> /gptstatus auto on');
if (!$removedAutoCommand['matched'] || $removedAutoCommand['command'] !== 'unknown') {
    throw new RuntimeException('PHP QQ bot must not retain automatic status push commands');
}

$statusHtml = '<html><div data-testid="heads-up"><div><svg class="text-icon-partial-outage"></svg>Some services are experiencing issues</div><div class="text-slate-900 dark:text-slate-50 p-4">One service is degraded</div></div>'
    . '<div data-testid="system-status"><div><svg class="text-icon-degraded"></svg></div><h3 class="font-medium">ChatGPT</h3></div></html>';
$parsedStatus = parseGptStatusHtml($statusHtml);
if ($parsedStatus['indicator'] !== 'partial_outage'
    || $parsedStatus['description'] !== 'Some services are experiencing issues — One service is degraded'
    || $parsedStatus['components'] !== [['name' => 'ChatGPT', 'status' => 'degraded_performance']]) {
    throw new RuntimeException('PHP QQ bot must parse status information from the status page HTML');
}

$statusSettings = ['gptstatus_image_url' => 'https://bot.example.com/qqbot/gptstatus-image.php'];
$stateFile = sys_get_temp_dir() . '/starryapi-qqbot-test-' . bin2hex(random_bytes(8)) . '/state.json';
$statusSettings['gptstatus_state_file'] = $stateFile;
$freshStatus = [
    'fingerprint' => str_repeat('b', 64),
    'summary' => $parsedStatus,
    'fetched_at' => time(),
];
$statusState = recordGptStatusSnapshot($statusSettings, $freshStatus);
if (($statusState['status']['summary']['indicator'] ?? '') !== 'partial_outage') {
    throw new RuntimeException('PHP QQ bot must persist the fetched status for its image endpoint');
}
$statusState = [
    'image_token' => $statusState['image_token'],
    'fingerprint' => $freshStatus['fingerprint'],
    'status' => $freshStatus,
];
$statusImageURL = buildGptStatusImageUrl($statusSettings, $statusState);
$statusImageURLParts = parse_url($statusImageURL);
$statusImageQuery = [];
parse_str((string) ($statusImageURLParts['query'] ?? ''), $statusImageQuery);
if (!is_array($statusImageURLParts)
    || !isValidGptStatusImageRequest($statusSettings, $statusState, $statusImageQuery, (string) ($statusImageURLParts['path'] ?? ''))) {
    throw new RuntimeException('PHP QQ bot must create valid signed status-image URLs');
}
$statusImageQuery['sig'] = str_repeat('0', 64);
if (isValidGptStatusImageRequest($statusSettings, $statusState, $statusImageQuery, '/qqbot/gptstatus-image.php')) {
    throw new RuntimeException('PHP QQ bot must reject tampered status-image signatures');
}
@unlink($stateFile);
@unlink($stateFile . '.lock');
@rmdir(dirname($stateFile));

fwrite(STDOUT, "PHP QQ bot signature, private-chat and GPT status vectors passed\n");
