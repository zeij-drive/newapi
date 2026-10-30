package main

import "testing"

import (
	"context"
	"errors"

	"github.com/stretchr/testify/require"
	"github.com/tencent-connect/botgo/dto"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		name    string
		content string
		command string
		matched bool
	}{
		{name: "help", content: "/sapi", command: "help", matched: true},
		{name: "help with whitespace", content: "  /SAPI help  ", command: "help", matched: true},
		{name: "invite requires email", content: "/sapi 邀请码", command: "invite_usage", matched: true},
		{name: "invite with email", content: "/sapi 邀请码 person@example.com", command: "invite", matched: true},
		{name: "invite is case insensitive for command", content: "/SAPI 邀请码 person@example.com", command: "invite", matched: true},
		{name: "unknown command", content: "/sapi status", command: "invalid", matched: true},
		{name: "different command", content: "/help", matched: false},
		{name: "empty", content: "", matched: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, matched := parseCommand(test.content)
			if matched != test.matched || command != test.command {
				t.Fatalf("parseCommand(%q) = (%q, %v), want (%q, %v)", test.content, command, matched, test.command, test.matched)
			}
		})
	}
}

func TestParseGroupCommandStripsQQMention(t *testing.T) {
	command, matched := parseGroupCommand("<@!bot-id> /sapi 邀请码 person@example.com")
	require.True(t, matched)
	require.Equal(t, "invite", command)
}

type testMessageSender struct {
	openID    string
	groupID   string
	messageID string
	content   string
	imageURL  string
	caption   string
}

func (sender *testMessageSender) SendReply(_ context.Context, openID, messageID, content string) error {
	sender.openID = openID
	sender.messageID = messageID
	sender.content = content
	return nil
}

func (sender *testMessageSender) SendGroupReply(_ context.Context, groupID, messageID, content string) error {
	sender.groupID = groupID
	sender.messageID = messageID
	sender.content = content
	return nil
}

func (sender *testMessageSender) SendGroupStatus(_ context.Context, groupID, messageID, imageURL, caption string) error {
	sender.groupID = groupID
	sender.messageID = messageID
	sender.imageURL = imageURL
	sender.caption = caption
	return nil
}

type testInviteEmailSender struct {
	openID string
	email  string
	err    error
	calls  int
}

type testInviteClaimer = testInviteEmailSender

func (sender *testInviteEmailSender) SendInvite(_ context.Context, openID, email string) error {
	sender.openID = openID
	sender.email = email
	sender.calls++
	return sender.err
}

func TestC2CMessageHandlerProvidesHelpAndEmailsInvite(t *testing.T) {
	sender := &testMessageSender{}
	inviteSender := &testInviteEmailSender{}
	handler := NewBot(sender, inviteSender).C2CMessageHandler()

	require.NoError(t, handler(nil, &dto.WSC2CMessageData{
		ID:      "help-message",
		Content: "/sapi",
		Author:  &dto.User{ID: "qq-open-id"},
	}))
	require.Equal(t, helpMessage, sender.content)
	require.Equal(t, 0, inviteSender.calls)

	message := &dto.WSC2CMessageData{
		ID:      "invite-message",
		Content: "/sapi 邀请码 person@example.com",
		Author:  &dto.User{ID: "qq-open-id"},
	}
	require.NoError(t, handler(nil, message))
	require.Equal(t, "qq-open-id", inviteSender.openID)
	require.Equal(t, "person@example.com", inviteSender.email)
	require.Equal(t, 1, inviteSender.calls)
	require.Equal(t, "qq-open-id", sender.openID)
	require.Equal(t, "invite-message", sender.messageID)
	require.Equal(t, inviteSentMessage, sender.content)
	require.NotContains(t, sender.content, "SAPI-")

	// Repeating the command asks the server to resend this OpenID's existing invite.
	require.NoError(t, handler(nil, message))
	require.Equal(t, 2, inviteSender.calls)
	require.Equal(t, inviteSentMessage, sender.content)
}

func TestC2CMessageHandlerValidatesEmailBeforeSendingInvite(t *testing.T) {
	sender := &testMessageSender{}
	inviteSender := &testInviteEmailSender{}
	handler := NewBot(sender, inviteSender).C2CMessageHandler()

	require.NoError(t, handler(nil, &dto.WSC2CMessageData{
		ID:      "missing-email-message",
		Content: "/sapi 邀请码",
		Author:  &dto.User{ID: "qq-open-id"},
	}))
	require.Equal(t, inviteUsageMessage, sender.content)
	require.Equal(t, 0, inviteSender.calls)

	require.NoError(t, handler(nil, &dto.WSC2CMessageData{
		ID:      "invalid-email-message",
		Content: "/sapi 邀请码 display name <person@example.com>",
		Author:  &dto.User{ID: "qq-open-id"},
	}))
	require.Equal(t, inviteUsageMessage, sender.content)
	require.Equal(t, 0, inviteSender.calls)
}

func TestC2CMessageHandlerReportsInviteEmailFailure(t *testing.T) {
	sender := &testMessageSender{}
	inviteSender := &testInviteEmailSender{err: errors.New("email delivery failed")}
	handler := NewBot(sender, inviteSender).C2CMessageHandler()

	require.NoError(t, handler(nil, &dto.WSC2CMessageData{
		ID:      "invite-message",
		Content: "/sapi 邀请码 person@example.com",
		Author:  &dto.User{ID: "qq-open-id"},
	}))
	require.Equal(t, 1, inviteSender.calls)
	require.Equal(t, inviteSendFailedMessage, sender.content)
}

func TestC2CMessageHandlerReportsInviteEmailRateLimit(t *testing.T) {
	sender := &testMessageSender{}
	inviteSender := &testInviteEmailSender{err: errInviteEmailRateLimited}
	handler := NewBot(sender, inviteSender).C2CMessageHandler()

	require.NoError(t, handler(nil, &dto.WSC2CMessageData{
		ID:      "invite-message",
		Content: "/sapi 邀请码 person@example.com",
		Author:  &dto.User{ID: "qq-open-id"},
	}))
	require.Equal(t, inviteRateLimitedMessage, sender.content)
}

func TestC2CMessageHandlerReportsUnknownSlashCommand(t *testing.T) {
	sender := &testMessageSender{}
	handler := NewBot(sender, &testInviteEmailSender{}).C2CMessageHandler()

	require.NoError(t, handler(nil, &dto.WSC2CMessageData{
		ID:      "unknown-message",
		Content: "/sapi unknown",
		Author:  &dto.User{ID: "qq-open-id"},
	}))
	require.Equal(t, commandNotFoundMessage, sender.content)
}

func TestGroupATMessageHandlerRepliesWithoutClaimingInvite(t *testing.T) {
	sender := &testMessageSender{}
	inviteSender := &testInviteEmailSender{}
	handler := NewBot(sender, inviteSender).GroupATMessageHandler()

	require.NoError(t, handler(nil, &dto.WSGroupATMessageData{
		ID:      "group-message",
		GroupID: "group-open-id",
		Content: "/sapi",
		Author:  &dto.User{ID: "qq-open-id"},
	}))
	require.Equal(t, "group-open-id", sender.groupID)
	require.Equal(t, "group-message", sender.messageID)
	require.Equal(t, helpMessage, sender.content)
	require.Equal(t, 0, inviteSender.calls)

	require.NoError(t, handler(nil, &dto.WSGroupATMessageData{
		ID:      "group-invite-message",
		GroupID: "group-open-id",
		Content: "<@!bot-id> /sapi 邀请码 person@example.com",
		Author:  &dto.User{ID: "qq-open-id"},
	}))
	require.Equal(t, groupInviteMessage, sender.content)
	require.Equal(t, 0, inviteSender.calls)
}

func TestGroupATMessageHandlerReportsUnknownCommand(t *testing.T) {
	sender := &testMessageSender{}
	handler := NewBot(sender, &testInviteEmailSender{}).GroupATMessageHandler()

	require.NoError(t, handler(nil, &dto.WSGroupATMessageData{
		ID:      "unknown-group-message",
		GroupID: "group-open-id",
		Content: "<@!bot-id> /sapi unknown",
		Author:  &dto.User{ID: "qq-open-id"},
	}))
	require.Equal(t, commandNotFoundMessage, sender.content)
}

func TestGroupIDFromMessageSupportsGroupOpenIDPayload(t *testing.T) {
	payload := &dto.WSPayload{RawMessage: []byte(`{"d":{"group_openid":"group-open-id"}}`)}
	require.Equal(t, "group-open-id", groupIDFromMessage(payload, &dto.WSGroupATMessageData{}))
}
