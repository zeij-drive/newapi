<?php
declare(strict_types=1);

require __DIR__ . '/../php/index.php';

$actual = signInviteRequest(
    'test-signature-secret',
    'POST',
    '/api/bot/registration-invite',
    '1700000000',
    '000102030405060708090a0b0c0d0e0f',
    '{"open_id":"qq-user"}'
);
$expected = '377b32cc7e1f64e0843d4fff71fdce4f7a8256e4c2557be7e652c30e8c3b9a88';
if (!hash_equals($expected, $actual)) {
    throw new RuntimeException('PHP QQ bot signature protocol regression');
}

$groupMessage = [
    't' => 'GROUP_AT_MESSAGE_CREATE',
    'd' => [
        'id' => 'group-message-id',
        'content' => '/sapi 邀请码',
        'author' => ['id' => 'qq-user'],
    ],
];
if (!handleC2CMessage([], $groupMessage)) {
    throw new RuntimeException('PHP QQ bot must ignore invite commands in group messages');
}

fwrite(STDOUT, "PHP QQ bot signature and private-chat vectors passed\n");
