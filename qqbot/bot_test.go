package main

import "testing"

import (
	"context"
	"errors"
	"strings"

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
		{name: "invite", content: "/sapi 邀请码", command: "invite", matched: true},
		{name: "invite is case insensitive for command", content: "/SAPI 邀请码", command: "invite", matched: true},
		{name: "unknown command shows help", content: "/sapi status", command: "help", matched: true},
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

type testMessageSender struct {
	openID    string
	messageID string
	content   string
}

func (sender *testMessageSender) SendReply(_ context.Context, openID, messageID, content string) error {
	sender.openID = openID
	sender.messageID = messageID
	sender.content = content
	return nil
}

type testInviteClaimer struct {
	openID string
	code   string
	err    error
	calls  int
}

func (claimer *testInviteClaimer) Claim(_ context.Context, openID string) (string, error) {
	claimer.openID = openID
	claimer.calls++
	return claimer.code, claimer.err
}

func TestC2CMessageHandlerProvidesHelpAndClaimsForQQOpenID(t *testing.T) {
	sender := &testMessageSender{}
	claimer := &testInviteClaimer{code: "SAPI-TEST-CODE"}
	handler := NewBot(sender, claimer).C2CMessageHandler()

	require.NoError(t, handler(nil, &dto.WSC2CMessageData{
		ID:      "help-message",
		Content: "/sapi",
		Author:  &dto.User{ID: "qq-open-id"},
	}))
	require.Equal(t, helpMessage, sender.content)
	require.Equal(t, 0, claimer.calls)

	message := &dto.WSC2CMessageData{
		ID:      "invite-message",
		Content: "/sapi 邀请码",
		Author:  &dto.User{ID: "qq-open-id"},
	}
	require.NoError(t, handler(nil, message))
	require.Equal(t, "qq-open-id", claimer.openID)
	require.Equal(t, 1, claimer.calls)
	require.Equal(t, "qq-open-id", sender.openID)
	require.Equal(t, "invite-message", sender.messageID)
	require.Contains(t, sender.content, "SAPI-TEST-CODE")

	// Repeating the command asks the server for this OpenID's existing claim.
	require.NoError(t, handler(nil, message))
	require.Equal(t, 2, claimer.calls)
	require.Contains(t, sender.content, "SAPI-TEST-CODE")
}

func TestC2CMessageHandlerReportsUnavailableInvite(t *testing.T) {
	sender := &testMessageSender{}
	claimer := &testInviteClaimer{err: errors.New("out of invites")}
	handler := NewBot(sender, claimer).C2CMessageHandler()

	require.NoError(t, handler(nil, &dto.WSC2CMessageData{
		ID:      "invite-message",
		Content: "/sapi 邀请码",
		Author:  &dto.User{ID: "qq-open-id"},
	}))
	require.Equal(t, 1, claimer.calls)
	require.NotEmpty(t, sender.content)
	require.True(t, strings.Contains(sender.content, "联系管理员"))
}
