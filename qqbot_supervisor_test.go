package main

import (
	"testing"
)

func TestQQBotAutoStartEnabledDefaultsToConfiguredBot(t *testing.T) {
	t.Setenv("QQ_BOT_AUTO_START", "")
	t.Setenv("QQ_BOT_APP_ID", "")
	enabled, err := qqBotAutoStartEnabled()
	if err != nil || enabled {
		t.Fatalf("expected disabled without QQ bot configuration, enabled=%v err=%v", enabled, err)
	}

	t.Setenv("QQ_BOT_APP_ID", "bot-app")
	enabled, err = qqBotAutoStartEnabled()
	if err != nil || !enabled {
		t.Fatalf("expected enabled with QQ bot configuration, enabled=%v err=%v", enabled, err)
	}

	t.Setenv("QQ_BOT_AUTO_START", "false")
	enabled, err = qqBotAutoStartEnabled()
	if err != nil || enabled {
		t.Fatalf("expected explicit false to disable, enabled=%v err=%v", enabled, err)
	}
}

func TestQQBotAutoStartRejectsInvalidBoolean(t *testing.T) {
	t.Setenv("QQ_BOT_AUTO_START", "sometimes")
	if _, err := qqBotAutoStartEnabled(); err == nil {
		t.Fatal("expected invalid QQ_BOT_AUTO_START to fail")
	}
}
