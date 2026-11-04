package common

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type GroupRatioChange struct {
	Name     string  `json:"name"`
	OldRatio float64 `json:"old_ratio"`
	NewRatio float64 `json:"new_ratio"`
}

func NotifyGroupRatioChanges(previous, current map[string]float64) {
	endpoint := strings.TrimSpace(GetEnvOrDefaultString("QQ_BOT_RATIO_NOTIFY_URL", ""))
	secret := strings.TrimSpace(GetEnvOrDefaultString("STARRY_API_BOT_SECRET", ""))
	if endpoint == "" {
		return
	}
	changes := groupRatioChanges(previous, current)
	if len(changes) == 0 {
		return
	}
	if len(secret) < 32 {
		SysError("QQ bot group-ratio notification is configured without a valid STARRY_API_BOT_SECRET")
		return
	}

	go func() {
		if err := sendGroupRatioNotification(context.Background(), endpoint, secret, changes, http.DefaultClient); err != nil {
			SysError("QQ bot group-ratio notification failed: " + err.Error())
		}
	}()
}

func groupRatioChanges(previous, current map[string]float64) []GroupRatioChange {
	changes := make([]GroupRatioChange, 0)
	for name, oldRatio := range previous {
		newRatio, exists := current[name]
		if exists && oldRatio != newRatio {
			changes = append(changes, GroupRatioChange{Name: name, OldRatio: oldRatio, NewRatio: newRatio})
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Name < changes[j].Name })
	return changes
}

func sendGroupRatioNotification(ctx context.Context, endpoint, secret string, changes []GroupRatioChange, client *http.Client) error {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackNotificationHost(parsed.Hostname()))) {
		return errors.New("invalid QQ_BOT_RATIO_NOTIFY_URL")
	}
	if len(secret) < 32 {
		return errors.New("invalid STARRY_API_BOT_SECRET")
	}
	if len(changes) == 0 {
		return nil
	}
	path := parsed.EscapedPath()
	if path == "" {
		path = "/"
	}
	requestClient := *client
	requestClient.Timeout = 10 * time.Second
	requestClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	var failures []error
	for start := 0; start < len(changes); start += 100 {
		end := min(start+100, len(changes))
		body, err := Marshal(struct {
			Groups []GroupRatioChange `json:"groups"`
		}{Groups: changes[start:end]})
		if err != nil {
			failures = append(failures, errors.New("could not encode group-ratio changes"))
			continue
		}
		timestamp := strconv.FormatInt(time.Now().Unix(), 10)
		nonceBytes := make([]byte, 16)
		if _, err := rand.Read(nonceBytes); err != nil {
			failures = append(failures, errors.New("could not create group-ratio notification signature"))
			continue
		}
		nonce := hex.EncodeToString(nonceBytes)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), strings.NewReader(string(body)))
		if err != nil {
			failures = append(failures, errors.New("could not create group-ratio notification request"))
			continue
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-QQ-Bot-Timestamp", timestamp)
		request.Header.Set("X-QQ-Bot-Nonce", nonce)
		request.Header.Set("X-QQ-Bot-Signature", signGroupRatioNotification(secret, http.MethodPost, path, timestamp, nonce, body))
		response, err := requestClient.Do(request)
		if err != nil {
			failures = append(failures, errors.New("group-ratio notification request failed"))
			continue
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			failures = append(failures, fmt.Errorf("group-ratio notification returned HTTP %d", response.StatusCode))
		}
	}
	return errors.Join(failures...)
}

func signGroupRatioNotification(secret, method, path, timestamp, nonce string, body []byte) string {
	digest := sha256.Sum256(body)
	canonical := fmt.Sprintf("%s\n%s\n%s\n%s\n%s", method, path, timestamp, nonce, hex.EncodeToString(digest[:]))
	return GenerateHMACWithKey([]byte(secret), canonical)
}

func isLoopbackNotificationHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	parsed := net.ParseIP(host)
	return parsed != nil && parsed.IsLoopback()
}
