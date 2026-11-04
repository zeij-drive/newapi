package common

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupRatioChangesOnlyIncludesExistingGroupsWhoseRatiosChanged(t *testing.T) {
	changes := groupRatioChanges(
		map[string]float64{"GPT破甲": 0.78, "removed": 1},
		map[string]float64{"GPT破甲": 1, "added": 0.01},
	)

	require.Equal(t, []GroupRatioChange{{Name: "GPT破甲", OldRatio: 0.78, NewRatio: 1}}, changes)
	require.Empty(t, groupRatioChanges(map[string]float64{"same": 1}, map[string]float64{"same": 1}))
}

func TestSendGroupRatioNotificationSignsAndChunksPayloads(t *testing.T) {
	const secret = "test-secret-with-at-least-32-characters"
	var mu sync.Mutex
	var batchSizes []int
	var invalidSignature bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		if err != nil {
			http.Error(writer, "read failed", http.StatusBadRequest)
			return
		}
		timestamp := request.Header.Get("X-QQ-Bot-Timestamp")
		nonce := request.Header.Get("X-QQ-Bot-Nonce")
		signature := request.Header.Get("X-QQ-Bot-Signature")
		if request.Method != http.MethodPost || request.URL.Path != "/qqbot/ratio-notification" ||
			signature != signGroupRatioNotification(secret, http.MethodPost, request.URL.EscapedPath(), timestamp, nonce, body) {
			mu.Lock()
			invalidSignature = true
			mu.Unlock()
			http.Error(writer, "bad signature", http.StatusUnauthorized)
			return
		}
		var payload struct {
			Groups []GroupRatioChange `json:"groups"`
		}
		if err := Unmarshal(body, &payload); err != nil {
			http.Error(writer, "bad payload", http.StatusBadRequest)
			return
		}
		mu.Lock()
		batchSizes = append(batchSizes, len(payload.Groups))
		mu.Unlock()
		writer.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	changes := make([]GroupRatioChange, 101)
	for index := range changes {
		changes[index] = GroupRatioChange{Name: strconv.Itoa(index), OldRatio: 1, NewRatio: 2}
	}
	require.NoError(t, sendGroupRatioNotification(context.Background(), server.URL+"/qqbot/ratio-notification", secret, changes, server.Client()))
	require.False(t, invalidSignature)
	require.Equal(t, []int{100, 1}, batchSizes)
}

func TestSendGroupRatioNotificationRejectsNonLoopbackHTTP(t *testing.T) {
	err := sendGroupRatioNotification(context.Background(), "http://example.test/ratio-notification", strings.Repeat("s", 32), []GroupRatioChange{{Name: "g", OldRatio: 1, NewRatio: 2}}, http.DefaultClient)
	require.ErrorContains(t, err, "invalid QQ_BOT_RATIO_NOTIFY_URL")
}
