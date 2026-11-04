package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tencent-connect/botgo/token"
)

func TestRatioNotificationStorePersistsSubscriptionsAndReplayNonces(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ratio-groups.json")
	store, err := newRatioNotificationStore(path)
	require.NoError(t, err)
	require.NoError(t, store.setEnabled("group-a", true))
	require.NoError(t, store.setEnabled("group-b", true))
	require.NoError(t, store.setEnabled("group-a", false))
	require.True(t, store.reserveNonce(strings.Repeat("a", 32), time.Now()))
	require.False(t, store.reserveNonce(strings.Repeat("a", 32), time.Now()))

	reloaded, err := newRatioNotificationStore(path)
	require.NoError(t, err)
	require.Equal(t, []string{"group-b"}, reloaded.enabledGroups())
	require.False(t, reloaded.reserveNonce(strings.Repeat("a", 32), time.Now()))
	require.FileExists(t, path)
	require.FileExists(t, path+".nonces")
}

func TestRatioNotificationStoreRollsBackSubscriptionWhenPersistenceFails(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	require.NoError(t, os.WriteFile(parentFile, []byte("file"), 0600))
	store := &ratioNotificationStore{
		path:   filepath.Join(parentFile, "groups.json"),
		groups: make(map[string]struct{}),
		nonces: make(map[string]time.Time),
	}
	require.Error(t, store.setEnabled("group-a", true))
	require.Empty(t, store.enabledGroups())
}

func TestRatioNotificationTextUsesDirectionAndCompactMultipliers(t *testing.T) {
	content := ratioNotificationText([]ratioChange{
		{Name: "GPT破甲", OldRatio: 0.78, NewRatio: 1},
		{Name: "GPT备用分组", OldRatio: 1, NewRatio: 0.01},
	})
	require.Equal(t, "@everyone 分组倍率变更通知\nGPT破甲分组涨价，0.78x->1x\nGPT备用分组降价，1x->0.01x", content)
	require.Error(t, validateRatioChanges([]ratioChange{{Name: "x\n@everyone", OldRatio: 1, NewRatio: 2}}))
	require.Error(t, validateRatioChanges([]ratioChange{{Name: "same", OldRatio: 1, NewRatio: 1}}))
	_, err := decodeRatioNotificationPayload([]byte(`{"groups":[{"name":"partial","new_ratio":1}]}`))
	require.Error(t, err)
}

type ratioAnnouncementTestSender struct {
	mu       sync.Mutex
	groupIDs []string
	content  string
	failFor  string
}

func (sender *ratioAnnouncementTestSender) SendReply(context.Context, string, string, string) error {
	return nil
}

func (sender *ratioAnnouncementTestSender) SendGroupReply(context.Context, string, string, string) error {
	return nil
}

func (sender *ratioAnnouncementTestSender) SendGroupAnnouncement(_ context.Context, groupID, content string) error {
	sender.mu.Lock()
	defer sender.mu.Unlock()
	sender.groupIDs = append(sender.groupIDs, groupID)
	sender.content = content
	if groupID == sender.failFor {
		return errors.New("send failed")
	}
	return nil
}

func TestNotifyRatioChangesContinuesAfterAGroupSendFailure(t *testing.T) {
	store, err := newRatioNotificationStore(filepath.Join(t.TempDir(), "groups.json"))
	require.NoError(t, err)
	require.NoError(t, store.setEnabled("group-a", true))
	require.NoError(t, store.setEnabled("group-b", true))
	require.NoError(t, store.setEnabled("group-c", true))
	sender := &ratioAnnouncementTestSender{failFor: "group-b"}
	bot := NewBot(sender, nil)
	bot.ConfigureRatioNotifications(store, nil)

	err = bot.NotifyRatioChanges(t.Context(), []ratioChange{{Name: "vip", OldRatio: 1, NewRatio: 2}})
	require.ErrorContains(t, err, "group group-b")
	require.Equal(t, []string{"group-a", "group-b", "group-c"}, sender.groupIDs)
	require.Contains(t, sender.content, "@everyone")
}

func TestGroupMultiplierNoticeCommandRequiresConfiguredAdmin(t *testing.T) {
	store, err := newRatioNotificationStore(filepath.Join(t.TempDir(), "groups.json"))
	require.NoError(t, err)
	bot := NewBot(&testMessageSender{}, nil)
	bot.ConfigureRatioNotifications(store, []string{"qq-admin"})

	require.Contains(t, bot.ratioNotificationReply("group-a", "member", "/sapi 倍率通知 开启"), "只有配置的群管理账号")
	require.Empty(t, store.enabledGroups())
	require.Equal(t, ratioNotificationEnabledMessage, bot.ratioNotificationReply("group-a", "qq-admin", "/sapi 倍率通知 开启"))
	require.Equal(t, []string{"group-a"}, store.enabledGroups())
	require.Equal(t, ratioNotificationDisabledMessage, bot.ratioNotificationReply("group-a", "qq-admin", "/sapi 倍率通知 关闭"))
	require.Empty(t, store.enabledGroups())
}

type ratioNotificationHandlerTestDouble struct {
	mu      sync.Mutex
	nonces  map[string]bool
	changes chan []ratioChange
}

func (handler *ratioNotificationHandlerTestDouble) ReserveRatioNotificationNonce(nonce string) bool {
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.nonces[nonce] {
		return false
	}
	handler.nonces[nonce] = true
	return true
}

func (handler *ratioNotificationHandlerTestDouble) NotifyRatioChanges(_ context.Context, changes []ratioChange) error {
	handler.changes <- changes
	return nil
}

func TestRatioNotificationWebhookVerifiesSignatureAndRejectsReplay(t *testing.T) {
	const secret = "test-secret-with-at-least-32-characters"
	credentials := &token.QQBotCredentials{AppID: "bot-app", AppSecret: "bot-secret"}
	dispatcher := &ratioNotificationHandlerTestDouble{nonces: make(map[string]bool), changes: make(chan []ratioChange, 1)}
	handler := newWebhookHandlerWithGPTStatus(credentials, "/qqbot", secret, dispatcher)
	body := `{"groups":[{"name":"GPT破甲","old_ratio":0.78,"new_ratio":1}]}`
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := strings.Repeat("b", 32)
	request := httptest.NewRequest(http.MethodPost, "/qqbot/ratio-notification", strings.NewReader(body))
	request.Header.Set("X-QQ-Bot-Timestamp", timestamp)
	request.Header.Set("X-QQ-Bot-Nonce", nonce)
	request.Header.Set("X-QQ-Bot-Signature", ratioNotificationSignature(secret, http.MethodPost, request.URL.EscapedPath(), timestamp, nonce, []byte(body)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusAccepted, response.Code)
	select {
	case changes := <-dispatcher.changes:
		require.Equal(t, []ratioChange{{Name: "GPT破甲", OldRatio: 0.78, NewRatio: 1}}, changes)
	case <-time.After(time.Second):
		t.Fatal("ratio notification was not dispatched")
	}

	replayed := httptest.NewRequest(http.MethodPost, "/qqbot/ratio-notification", strings.NewReader(body))
	replayed.Header = request.Header.Clone()
	replayedResponse := httptest.NewRecorder()
	handler.ServeHTTP(replayedResponse, replayed)
	require.Equal(t, http.StatusConflict, replayedResponse.Code)

	invalidSignature := httptest.NewRequest(http.MethodPost, "/qqbot/ratio-notification", strings.NewReader(body))
	invalidSignature.Header = request.Header.Clone()
	invalidSignature.Header.Set("X-QQ-Bot-Nonce", strings.Repeat("c", 32))
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalidSignature)
	require.Equal(t, http.StatusUnauthorized, invalidResponse.Code)
}
