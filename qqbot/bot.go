package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tencent-connect/botgo/dto"
	"github.com/tencent-connect/botgo/event"
)

const helpMessage = "StarryAPI bot\n/sapi - 显示帮助\n/sapi 邀请码 - 领取注册邀请码（每个 QQ 账号一个）"

type messageSender interface {
	SendReply(ctx context.Context, openID, messageID, content string) error
}

type inviteClaimer interface {
	Claim(ctx context.Context, openID string) (string, error)
}

type Bot struct {
	sender  messageSender
	claimer inviteClaimer
}

func NewBot(sender messageSender, claimer inviteClaimer) *Bot {
	return &Bot{sender: sender, claimer: claimer}
}

func (bot *Bot) C2CMessageHandler() event.C2CMessageEventHandler {
	return func(_ *dto.WSPayload, message *dto.WSC2CMessageData) error {
		if message == nil || message.Author == nil {
			return nil
		}
		command, matched := parseCommand(message.Content)
		if !matched {
			return nil
		}
		if message.ID == "" || message.Author.ID == "" {
			return errors.New("QQ message is missing its reply or user identifier")
		}

		content := helpMessage
		if command == "invite" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			code, err := bot.claimer.Claim(ctx, message.Author.ID)
			cancel()
			if err != nil {
				content = "邀请码生成失败，请稍后重试。"
			} else {
				content = "StarryAPI 注册邀请码：\n" + code + "\n该 QQ 账号重复领取时会返回同一个邀请码。"
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return bot.sender.SendReply(ctx, message.Author.ID, message.ID, content)
	}
}

func parseCommand(content string) (string, bool) {
	if len(content) > 256 {
		return "", false
	}
	fields := strings.Fields(strings.TrimSpace(content))
	if len(fields) == 0 || !strings.EqualFold(fields[0], "/sapi") {
		return "", false
	}
	if len(fields) == 1 || strings.EqualFold(fields[1], "help") {
		return "help", true
	}
	if strings.EqualFold(fields[1], "邀请码") {
		return "invite", true
	}
	return "help", true
}
