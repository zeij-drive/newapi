package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type managedQQBot struct {
	cancel context.CancelFunc
	done   <-chan error
}

func startManagedQQBot() (*managedQQBot, error) {
	enabled, err := qqBotAutoStartEnabled()
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, nil
	}

	binary, err := resolveQQBotBinary()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, binary)
	command.Env = os.Environ()
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if workDir := strings.TrimSpace(os.Getenv("QQ_BOT_WORKDIR")); workDir != "" {
		command.Dir = workDir
	}
	if err := command.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start QQ Bot %q: %w", binary, err)
	}
	done := make(chan error, 1)
	go func() {
		done <- command.Wait()
	}()
	return &managedQQBot{cancel: cancel, done: done}, nil
}

func (bot *managedQQBot) stop(timeout time.Duration) {
	if bot == nil {
		return
	}
	bot.cancel()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-bot.done:
	case <-timer.C:
	}
}

func qqBotAutoStartEnabled() (bool, error) {
	value, configured := os.LookupEnv("QQ_BOT_AUTO_START")
	if configured && strings.TrimSpace(value) != "" {
		enabled, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return false, errors.New("QQ_BOT_AUTO_START must be a boolean")
		}
		return enabled, nil
	}
	return strings.TrimSpace(os.Getenv("QQ_BOT_APP_ID")) != "", nil
}

func resolveQQBotBinary() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("QQ_BOT_BINARY")); configured != "" {
		return configured, nil
	}

	candidates := []string{"qqbot"}
	if runtime.GOOS == "windows" {
		candidates = append([]string{"qqbot.exe"}, candidates...)
	}
	if workingDirectory, err := os.Getwd(); err == nil {
		if runtime.GOOS == "windows" {
			candidates = append(candidates, filepath.Join(workingDirectory, "qqbot", "qqbot.exe"))
		}
		candidates = append(candidates, filepath.Join(workingDirectory, "qqbot", "qqbot"))
	}
	if executable, err := os.Executable(); err == nil {
		directory := filepath.Dir(executable)
		if runtime.GOOS == "windows" {
			candidates = append(candidates, filepath.Join(directory, "qqbot.exe"))
		}
		candidates = append(candidates, filepath.Join(directory, "qqbot"))
	}
	if runtime.GOOS != "windows" {
		candidates = append(candidates, "/qqbot")
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", errors.New("QQ Bot binary not found; set QQ_BOT_BINARY or build qqbot alongside NewAPI")
}
