package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"strings"
	"time"

	"github.com/tencent-connect/botgo/dto"
	"github.com/tencent-connect/botgo/event"
)

const helpMessage = "StarryAPI bot\n/sapi - 显示帮助\n/sapi 邀请码 邮箱 - 私聊或群聊@机器人获取邀请码（每个 QQ 账号一个，邮件发送）\n/gptstatus - 群聊查询 GPT 状态图片"
const inviteUsageMessage = "用法：/sapi 邀请码 邮箱"
const inviteSentMessage = "邀请码已发送至邮箱，请查收。"
const inviteSendFailedMessage = "邀请码邮件发送失败，请检查邮箱或稍后重试。"
const inviteRateLimitedMessage = "请求过于频繁，请 1 分钟后重试。"
const commandNotFoundMessage = "指令不存在"
const gptStatusUsageMessage = "用法：/gptstatus（查询并发送 GPT 状态图片）。"

type messageSender interface {
	SendReply(ctx context.Context, openID, messageID, content string) error
}

type groupMessageSender interface {
	SendGroupReply(ctx context.Context, groupID, messageID, content string) error
}

type groupStatusSender interface {
	SendGroupStatus(ctx context.Context, groupID, messageID, imageURL, caption string) error
}

type inviteEmailSender interface {
	SendInvite(ctx context.Context, openID, email string) error
}

type Bot struct {
	sender         messageSender
	inviteSender   inviteEmailSender
	statusFetcher  gptStatusFetcher
	statusImageURL string
	statusSecret   string
	webhookPath    string
}

func NewBot(sender messageSender, inviteSender inviteEmailSender) *Bot {
	client := newGPTStatusClient(nil)
	return &Bot{
		sender:        sender,
		inviteSender:  inviteSender,
		statusFetcher: client,
		webhookPath:   "/qqbot",
	}
}

func (bot *Bot) ConfigureGPTStatus(publicURL, webhookPath, secret string) {
	bot.statusImageURL = strings.TrimSpace(publicURL)
	bot.statusSecret = strings.TrimSpace(secret)
	if webhookPath != "" {
		bot.webhookPath = webhookPath
	}
}

func (bot *Bot) sendGPTStatus(ctx context.Context, groupID, messageID string, snapshot gptStatusSnapshot, caption string) error {
	sender, ok := bot.sender.(groupStatusSender)
	if !ok {
		return errors.New("QQ group status sender is not configured")
	}
	imageURL, err := makeGPTStatusImageURL(bot.statusImageURL, bot.webhookPath, bot.statusSecret, snapshot)
	if err != nil {
		return err
	}
	return sender.SendGroupStatus(ctx, groupID, messageID, imageURL, caption)
}

func (bot *Bot) C2CMessageHandler() event.C2CMessageEventHandler {
	return func(_ *dto.WSPayload, message *dto.WSC2CMessageData) error {
		if message == nil || message.Author == nil {
			return nil
		}
		command, matched := parseCommand(message.Content)
		if !matched {
			if !isSlashCommand(message.Content) {
				return nil
			}
			command = "invalid"
		}
		if message.ID == "" || message.Author.ID == "" {
			return errors.New("QQ message is missing its reply or user identifier")
		}

		content := helpMessage
		switch command {
		case "invite":
			content = bot.inviteEmailReply(message.Author.ID, message.Content)
		case "invite_usage":
			content = inviteUsageMessage
		case "invalid":
			content = commandNotFoundMessage
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return bot.sender.SendReply(ctx, message.Author.ID, message.ID, content)
	}
}

// GroupATMessageHandler handles commands sent by @mentioning the bot in a group.
// Invite codes are delivered by email and are never included in group replies.
func (bot *Bot) GroupATMessageHandler() event.GroupATMessageEventHandler {
	return func(payload *dto.WSPayload, message *dto.WSGroupATMessageData) error {
		if message == nil || message.Author == nil {
			return nil
		}
		if command, matched := parseGPTStatusCommand(message.Content); matched {
			groupID := groupIDFromMessage(payload, message)
			if message.ID == "" || groupID == "" {
				return errors.New("QQ group message is missing its reply or group identifier")
			}
			return bot.handleGPTStatusCommand(groupID, message.ID, command)
		}
		command, matched := parseGroupCommand(message.Content)
		if !matched {
			content, mentionMatched := stripGroupMention(message.Content)
			if !mentionMatched || !isSlashCommand(content) {
				return nil
			}
			groupID := groupIDFromMessage(payload, message)
			if message.ID == "" || groupID == "" {
				return errors.New("QQ group message is missing its reply or group identifier")
			}
			sender, ok := bot.sender.(groupMessageSender)
			if !ok {
				return errors.New("QQ group message sender is not configured")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			return sender.SendGroupReply(ctx, groupID, message.ID, commandNotFoundMessage)
		}
		groupID := groupIDFromMessage(payload, message)
		if message.ID == "" || groupID == "" {
			return errors.New("QQ group message is missing its reply or group identifier")
		}
		sender, ok := bot.sender.(groupMessageSender)
		if !ok {
			return errors.New("QQ group message sender is not configured")
		}
		content := helpMessage
		switch command {
		case "invite":
			commandContent, _ := stripGroupMention(message.Content)
			if message.Author.ID == "" {
				return errors.New("QQ group message is missing its user identifier")
			}
			content = bot.inviteEmailReply(message.Author.ID, commandContent)
		case "invite_usage":
			content = inviteUsageMessage
		case "invalid":
			content = commandNotFoundMessage
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return sender.SendGroupReply(ctx, groupID, message.ID, content)
	}
}

func (bot *Bot) inviteEmailReply(openID, commandContent string) string {
	fields := strings.Fields(commandContent)
	if len(fields) != 3 || !validInviteEmail(fields[2]) {
		return inviteUsageMessage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := bot.inviteSender.SendInvite(ctx, openID, fields[2]); err != nil {
		if errors.Is(err, errInviteEmailRateLimited) {
			return inviteRateLimitedMessage
		}
		return inviteSendFailedMessage
	}
	return inviteSentMessage
}

func (bot *Bot) handleGPTStatusCommand(groupID, messageID string, command gptStatusCommand) error {
	groupSender, ok := bot.sender.(groupMessageSender)
	if !ok {
		return errors.New("QQ group message sender is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch command {
	case gptStatusInvalid:
		return groupSender.SendGroupReply(ctx, groupID, messageID, commandNotFoundMessage)
	case gptStatusUsage:
		return groupSender.SendGroupReply(ctx, groupID, messageID, gptStatusUsageMessage)
	case gptStatusQuery:
		if bot.statusImageURL == "" {
			return groupSender.SendGroupReply(ctx, groupID, messageID, "状态图片功能尚未配置，请管理员设置 QQ_BOT_PUBLIC_URL 后重启机器人。")
		}
		snapshot, err := bot.statusFetcher.Fetch(ctx)
		if err != nil {
			return groupSender.SendGroupReply(ctx, groupID, messageID, "暂时无法获取 OpenAI 状态，请稍后重试。")
		}
		if err := bot.sendGPTStatus(ctx, groupID, messageID, snapshot, snapshot.Text()); err != nil {
			return groupSender.SendGroupReply(ctx, groupID, messageID, "已获取状态，但图片发送失败，请稍后重试。")
		}
		return nil
	default:
		return groupSender.SendGroupReply(ctx, groupID, messageID, gptStatusUsageMessage)
	}
}

func groupIDFromMessage(payload *dto.WSPayload, message *dto.WSGroupATMessageData) string {
	if message != nil && message.GroupID != "" {
		return message.GroupID
	}
	if payload == nil || len(payload.RawMessage) == 0 {
		return ""
	}
	var envelope struct {
		Data struct {
			GroupOpenID string `json:"group_openid"`
			GroupID     string `json:"group_id"`
		} `json:"d"`
	}
	if err := json.Unmarshal(payload.RawMessage, &envelope); err != nil {
		return ""
	}
	if envelope.Data.GroupOpenID != "" {
		return envelope.Data.GroupOpenID
	}
	return envelope.Data.GroupID
}

func parseCommand(content string) (string, bool) {
	if len(content) > 256 {
		return "", false
	}
	fields := strings.Fields(strings.TrimSpace(content))
	if len(fields) == 0 || !strings.EqualFold(fields[0], "/sapi") {
		return "", false
	}
	if len(fields) == 1 || (len(fields) == 2 && strings.EqualFold(fields[1], "help")) {
		return "help", true
	}
	if fields[1] == "邀请码" {
		if len(fields) == 3 {
			return "invite", true
		}
		return "invite_usage", true
	}
	return "invalid", true
}

func validInviteEmail(value string) bool {
	if value == "" || len(value) > 50 || strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	address, err := mail.ParseAddress(value)
	return err == nil && address.Address == value
}

func isSlashCommand(content string) bool {
	return strings.HasPrefix(strings.TrimSpace(content), "/")
}

func parseGroupCommand(content string) (string, bool) {
	var ok bool
	content, ok = stripGroupMention(content)
	if !ok {
		return "", false
	}
	return parseCommand(content)
}
