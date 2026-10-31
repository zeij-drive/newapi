/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package controller

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	sub2APIRatioSyncOptionKey  = "Sub2APIRatioSyncSecret"
	sub2APIRatioSyncTaskType   = model.SystemTaskTypeSub2APIRatioSync
	sub2APIRatioSyncInterval   = 60
	sub2APIRatioSyncMaxLogs    = 100
	sub2APIRatioSyncMaxBackups = 20
)

var sub2APIRatioSyncMu sync.Mutex

type sub2APIRatioSyncMapping struct {
	SourceID  int     `json:"source_id"`
	Target    string  `json:"target"`
	Mode      string  `json:"mode"`
	Value     float64 `json:"value"`
	Enabled   bool    `json:"enabled"`
	AllowPeak bool    `json:"allow_peak"`
}

type sub2APIRatioSyncSettings struct {
	UpstreamURL  string                    `json:"upstream_url"`
	Interval     int                       `json:"interval_seconds"`
	Enabled      bool                      `json:"enabled"`
	Mappings     []sub2APIRatioSyncMapping `json:"mappings"`
	MappingScope string                    `json:"mapping_scope"`
}

type sub2APIRatioSyncGroup struct {
	ID            int      `json:"id"`
	Name          string   `json:"name"`
	CanonicalName string   `json:"canonical_name"`
	Default       float64  `json:"default"`
	Custom        *float64 `json:"custom"`
	Effective     float64  `json:"effective"`
	Peak          bool     `json:"peak"`
}

type sub2APIRatioSyncBaseline struct {
	Desired    float64 `json:"desired"`
	SourceRate float64 `json:"source_rate"`
}

type sub2APIRatioSyncLog struct {
	ID      int64          `json:"id"`
	At      int64          `json:"created_at"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

type sub2APIRatioSyncBackup struct {
	ID     int64              `json:"id"`
	At     int64              `json:"created_at"`
	Before map[string]float64 `json:"before"`
	After  map[string]float64 `json:"after"`
}

type sub2APIRatioSyncResult struct {
	At             int64            `json:"at"`
	Mode           string           `json:"mode"`
	Changed        int              `json:"changed"`
	Rows           []map[string]any `json:"rows"`
	Warnings       []map[string]any `json:"warnings"`
	UpstreamChange []map[string]any `json:"upstream_changes"`
	BackupID       int64            `json:"backup_id,omitempty"`
}

type sub2APIRatioSyncRuntime struct {
	Groups     []sub2APIRatioSyncGroup             `json:"groups"`
	Ratios     map[string]float64                  `json:"ratios"`
	Baseline   map[string]sub2APIRatioSyncBaseline `json:"baseline"`
	Pending    []string                            `json:"pending"`
	Warnings   []map[string]any                    `json:"warnings"`
	Failures   int                                 `json:"failures"`
	LastRun    int64                               `json:"last_run"`
	NextRun    int64                               `json:"next_run"`
	LastResult map[string]any                      `json:"last_result"`
	Logs       []sub2APIRatioSyncLog               `json:"logs"`
	Backups    []sub2APIRatioSyncBackup            `json:"backups"`
}

type sub2APIRatioSyncPair struct {
	TokenHash string `json:"token_hash"`
	Site      string `json:"site"`
	ExpiresAt int64  `json:"expires_at"`
	UsedAt    int64  `json:"used_at"`
}

type sub2APIRatioSyncSecrets struct {
	AccessToken  string                 `json:"access_token"`
	RefreshToken string                 `json:"refresh_token"`
	ExpiresAt    int64                  `json:"expires_at"`
	Pairs        []sub2APIRatioSyncPair `json:"pairs"`
}

type sub2APIRatioSyncState struct {
	Settings sub2APIRatioSyncSettings `json:"settings"`
	Secrets  sub2APIRatioSyncSecrets  `json:"secrets"`
	Runtime  sub2APIRatioSyncRuntime  `json:"runtime"`
}

type sub2APIRatioSyncPublicState struct {
	Settings sub2APIRatioSyncSettings `json:"settings"`
	Runtime  sub2APIRatioSyncRuntime  `json:"runtime"`
	Auth     struct {
		Upstream bool `json:"upstream"`
		Refresh  bool `json:"refresh"`
	} `json:"auth"`
	ServerTime int64 `json:"server_time"`
}

type sub2APIRatioSyncError struct {
	Message   string
	Retryable bool
	Status    int
}

func (err *sub2APIRatioSyncError) Error() string { return err.Message }

func newSub2APIRatioSyncError(message string, status int, retryable bool) *sub2APIRatioSyncError {
	return &sub2APIRatioSyncError{Message: message, Status: status, Retryable: retryable}
}

func sub2APIRatioSyncRetryDelay(interval, failures int) int {
	if interval <= 0 {
		interval = sub2APIRatioSyncInterval
	}
	attempts := min(max(failures-1, 0), 5)
	delay := interval * (1 << attempts)
	return min(delay, max(interval, 1800))
}

func initialSub2APIRatioSyncState() sub2APIRatioSyncState {
	return sub2APIRatioSyncState{
		Settings: sub2APIRatioSyncSettings{Interval: sub2APIRatioSyncInterval, Mappings: []sub2APIRatioSyncMapping{}},
		Secrets:  sub2APIRatioSyncSecrets{Pairs: []sub2APIRatioSyncPair{}},
		Runtime: sub2APIRatioSyncRuntime{
			Groups: []sub2APIRatioSyncGroup{}, Ratios: map[string]float64{},
			Baseline: map[string]sub2APIRatioSyncBaseline{}, Pending: []string{},
			Warnings: []map[string]any{}, Logs: []sub2APIRatioSyncLog{}, Backups: []sub2APIRatioSyncBackup{},
		},
	}
}

func loadSub2APIRatioSyncState() (sub2APIRatioSyncState, error) {
	state := initialSub2APIRatioSyncState()
	if model.DB == nil {
		return state, errors.New("database is unavailable")
	}
	var option model.Option
	err := model.DB.Where(&model.Option{Key: sub2APIRatioSyncOptionKey}).First(&option).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return state, nil
	}
	if err != nil {
		return state, errors.New("load Sub2API ratio sync state failed")
	}
	plaintext, err := common.DecryptWithCryptoSecret(option.Value)
	if err != nil {
		return state, errors.New("cannot decrypt Sub2API ratio sync settings")
	}
	if err := common.UnmarshalJsonStr(plaintext, &state); err != nil {
		return state, errors.New("Sub2API ratio sync settings are invalid")
	}
	if state.Settings.Interval < 60 || state.Settings.Interval > 86400 {
		state.Settings.Interval = sub2APIRatioSyncInterval
	}
	if state.Settings.Mappings == nil {
		state.Settings.Mappings = []sub2APIRatioSyncMapping{}
	}
	if state.Secrets.Pairs == nil {
		state.Secrets.Pairs = []sub2APIRatioSyncPair{}
	}
	if state.Runtime.Groups == nil {
		state.Runtime.Groups = []sub2APIRatioSyncGroup{}
	}
	if state.Runtime.Ratios == nil {
		state.Runtime.Ratios = map[string]float64{}
	}
	if state.Runtime.Baseline == nil {
		state.Runtime.Baseline = map[string]sub2APIRatioSyncBaseline{}
	}
	if state.Runtime.Pending == nil {
		state.Runtime.Pending = []string{}
	}
	if state.Runtime.Warnings == nil {
		state.Runtime.Warnings = []map[string]any{}
	}
	state.Runtime.Failures = min(max(state.Runtime.Failures, 0), 6)
	if state.Runtime.Logs == nil {
		state.Runtime.Logs = []sub2APIRatioSyncLog{}
	}
	if state.Runtime.Backups == nil {
		state.Runtime.Backups = []sub2APIRatioSyncBackup{}
	}
	if state.Settings.UpstreamURL != "" {
		state.Settings.UpstreamURL, err = normalizeSub2APIBaseURL(state.Settings.UpstreamURL)
		if err != nil {
			return state, errors.New("saved Sub2API URL is invalid")
		}
	}
	return state, nil
}

func saveSub2APIRatioSyncState(state sub2APIRatioSyncState) error {
	encoded, err := common.Marshal(state)
	if err != nil {
		return errors.New("encode Sub2API ratio sync state failed")
	}
	encrypted, err := common.EncryptWithCryptoSecret(string(encoded))
	if err != nil {
		return errors.New("encrypt Sub2API ratio sync state failed")
	}
	option := model.Option{Key: sub2APIRatioSyncOptionKey, Value: encrypted}
	err = model.DB.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.Assignments(map[string]any{"value": encrypted}),
	}).Create(&option).Error
	if err != nil {
		return errors.New("save Sub2API ratio sync state failed")
	}
	return nil
}

func publicSub2APIRatioSyncState(state sub2APIRatioSyncState) sub2APIRatioSyncPublicState {
	state.Runtime.Baseline = nil
	state.Runtime.Pending = []string{}
	view := sub2APIRatioSyncPublicState{Settings: state.Settings, Runtime: state.Runtime, ServerTime: time.Now().Unix()}
	view.Auth.Upstream = state.Secrets.AccessToken != "" || state.Secrets.RefreshToken != ""
	view.Auth.Refresh = state.Secrets.RefreshToken != ""
	return view
}

func normalizeSub2APIBaseURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.ContainsAny(raw, "\\\r\n ") {
		return "", newSub2APIRatioSyncError("请输入完整的 http(s) 站点根地址。", http.StatusBadRequest, false)
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if !slices.Contains([]string{"http", "https"}, parsed.Scheme) {
		return "", newSub2APIRatioSyncError("请输入完整的 http(s) 站点根地址。", http.StatusBadRequest, false)
	}
	if parsed.Scheme == "http" && !isSub2APIRatioSyncLoopbackHost(parsed.Hostname()) {
		return "", newSub2APIRatioSyncError("远程 Sub2API 地址必须使用 HTTPS；仅允许环回地址使用 HTTP。", http.StatusBadRequest, false)
	}
	if parsed.Path == "/" {
		parsed.Path = ""
	}
	if strings.EqualFold(parsed.Hostname(), "f-api.site") || strings.EqualFold(parsed.Hostname(), "www.f-api.site") {
		standardPort := parsed.Port() == "" || parsed.Scheme == "https" && parsed.Port() == "443" || parsed.Scheme == "http" && parsed.Port() == "80"
		if parsed.Path == "" && standardPort {
			return "https://www.f-api.site", nil
		}
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func isSub2APIRatioSyncLoopbackHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func sub2APIRatioSyncReceiverURL(requestHost string, requestTLS bool, serverAddress string) (string, error) {
	host := requestHost
	scheme := "http"
	if requestTLS {
		scheme = "https"
	}
	if strings.TrimSpace(serverAddress) != "" {
		configured, err := url.Parse(strings.TrimSpace(serverAddress))
		if err != nil || configured.Host == "" || configured.User != nil || configured.RawQuery != "" || configured.Fragment != "" {
			return "", errors.New("本站服务地址无效")
		}
		host = configured.Host
		scheme = strings.ToLower(configured.Scheme)
	}
	hostURL, err := url.Parse(scheme + "://" + host)
	if err != nil || hostURL.Host != host || hostURL.Hostname() == "" {
		return "", errors.New("无法确定本站域名，请先配置服务地址")
	}
	if scheme != "https" && !(scheme == "http" && isSub2APIRatioSyncLoopbackHost(hostURL.Hostname())) {
		return "", errors.New("登录助手回调地址必须使用 HTTPS；仅允许环回地址使用 HTTP")
	}
	return scheme + "://" + host + "/api/sub2api-ratio-sync/pair", nil
}

func validateSub2APIRatioSyncMappings(mappings []sub2APIRatioSyncMapping) error {
	if len(mappings) > 500 {
		return errors.New("映射不能超过 500 条")
	}
	targets := make(map[string]struct{}, len(mappings))
	for i := range mappings {
		mapping := &mappings[i]
		mapping.Target = strings.TrimSpace(mapping.Target)
		if mapping.SourceID < 1 || mapping.Target == "" || len(mapping.Target) > 128 ||
			!slices.Contains([]string{"effective", "default", "fixed"}, mapping.Mode) ||
			math.IsNaN(mapping.Value) || math.IsInf(mapping.Value, 0) || mapping.Value < 0 {
			return errors.New("映射包含无效分组或倍率")
		}
		if _, ok := targets[mapping.Target]; ok {
			return errors.New("下游分组不能重复映射")
		}
		targets[mapping.Target] = struct{}{}
	}
	return nil
}

func cleanSub2APIToken(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(raw, "Bearer "), "bearer "))
	if len(raw) > 32768 || strings.ContainsAny(raw, "\r\n") {
		return "", errors.New("上游令牌格式不正确")
	}
	return raw, nil
}

type sub2APIRatioSyncAPIResponse struct {
	Code    int             `json:"code"`
	Data    json.RawMessage `json:"data"`
	Message string          `json:"message"`
}

func requestSub2API(ctx context.Context, baseURL, path, token string, method string, payload any) (sub2APIRatioSyncAPIResponse, int, error) {
	var response sub2APIRatioSyncAPIResponse
	var body io.Reader
	if payload != nil {
		encoded, err := common.Marshal(payload)
		if err != nil {
			return response, 0, newSub2APIRatioSyncError("构造上游请求失败", http.StatusBadRequest, false)
		}
		body = strings.NewReader(string(encoded))
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(baseURL, "/")+path, body)
	if err != nil {
		return response, 0, newSub2APIRatioSyncError("上游地址无效", http.StatusBadRequest, false)
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if baseURL == "https://www.f-api.site" {
		request.Header.Set("X-User-UI-Request", "1")
		request.Header.Set("Accept-Language", "zh-CN")
	}
	client := &http.Client{
		Timeout: 15 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Do(request)
	if err != nil {
		return response, 0, newSub2APIRatioSyncError("无法连接上游站点，请检查地址、DNS 和出站网络。", http.StatusBadGateway, true)
	}
	defer resp.Body.Close()
	content, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024+1))
	if err != nil || len(content) > 2*1024*1024 {
		return response, resp.StatusCode, newSub2APIRatioSyncError("上游响应读取失败或超过 2 MiB。", http.StatusBadGateway, false)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		retryable := resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		message := fmt.Sprintf("上游返回 HTTP %d", resp.StatusCode)
		if resp.StatusCode == http.StatusUnauthorized {
			message = "上游登录已失效，请重新登录或导入令牌。"
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			message = "上游发生重定向，请填写最终站点地址。"
		}
		return response, resp.StatusCode, newSub2APIRatioSyncError(message, http.StatusBadGateway, retryable)
	}
	if err := common.Unmarshal(content, &response); err != nil {
		return response, resp.StatusCode, newSub2APIRatioSyncError("上游没有返回有效 JSON。", http.StatusBadGateway, false)
	}
	return response, resp.StatusCode, nil
}

func sub2APIRatioSyncPath(state sub2APIRatioSyncState, path string) string {
	if state.Settings.UpstreamURL == "https://www.f-api.site" && (path == "/api/v1/groups/available" || path == "/api/v1/groups/rates") {
		return path + "?scope=personal"
	}
	return path
}

func refreshSub2APIToken(ctx context.Context, state *sub2APIRatioSyncState) error {
	if state.Secrets.RefreshToken == "" {
		return newSub2APIRatioSyncError("上游令牌已失效，请重新登录或导入令牌。", http.StatusUnauthorized, false)
	}
	response, _, err := requestSub2API(ctx, state.Settings.UpstreamURL, "/api/v1/auth/refresh", "", http.MethodPost, map[string]string{"refresh_token": state.Secrets.RefreshToken})
	if err != nil {
		return err
	}
	if response.Code != 0 {
		return newSub2APIRatioSyncError("刷新上游会话失败，请重新登录。", http.StatusUnauthorized, false)
	}
	var tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
	}
	if err := common.Unmarshal(response.Data, &tokens); err != nil || tokens.AccessToken == "" {
		return newSub2APIRatioSyncError("刷新上游会话失败，请重新登录。", http.StatusUnauthorized, false)
	}
	state.Secrets.AccessToken = tokens.AccessToken
	if tokens.RefreshToken != "" {
		state.Secrets.RefreshToken = tokens.RefreshToken
	}
	state.Secrets.ExpiresAt = 0
	if tokens.ExpiresIn > 0 {
		state.Secrets.ExpiresAt = time.Now().Unix() + tokens.ExpiresIn
	}
	return saveSub2APIRatioSyncState(*state)
}

func upstreamSub2APIRatioSyncRequest(ctx context.Context, state *sub2APIRatioSyncState, path string) (json.RawMessage, error) {
	if state.Settings.UpstreamURL == "" || state.Secrets.AccessToken == "" && state.Secrets.RefreshToken == "" {
		return nil, newSub2APIRatioSyncError("请先配置并登录上游 Sub2API。", http.StatusBadRequest, false)
	}
	if state.Secrets.AccessToken == "" || state.Secrets.ExpiresAt > 0 && state.Secrets.ExpiresAt < time.Now().Unix()+30 {
		if err := refreshSub2APIToken(ctx, state); err != nil {
			return nil, err
		}
	}
	requestPath := sub2APIRatioSyncPath(*state, path)
	response, status, err := requestSub2API(ctx, state.Settings.UpstreamURL, requestPath, state.Secrets.AccessToken, http.MethodGet, nil)
	if err != nil {
		var syncErr *sub2APIRatioSyncError
		if errors.As(err, &syncErr) && status == http.StatusUnauthorized && state.Secrets.RefreshToken != "" {
			if refreshErr := refreshSub2APIToken(ctx, state); refreshErr != nil {
				return nil, refreshErr
			}
			response, _, err = requestSub2API(ctx, state.Settings.UpstreamURL, requestPath, state.Secrets.AccessToken, http.MethodGet, nil)
		}
	}
	if err != nil {
		return nil, err
	}
	if response.Code != 0 {
		return nil, newSub2APIRatioSyncError("上游拒绝了请求，请检查账号会话和站点设置。", http.StatusBadGateway, false)
	}
	return response.Data, nil
}

func fetchSub2APIRatioSyncGroups(ctx context.Context, state *sub2APIRatioSyncState) ([]sub2APIRatioSyncGroup, error) {
	groupsData, err := upstreamSub2APIRatioSyncRequest(ctx, state, "/api/v1/groups/available")
	if err != nil {
		return nil, err
	}
	ratesData, err := upstreamSub2APIRatioSyncRequest(ctx, state, "/api/v1/groups/rates")
	if err != nil {
		return nil, err
	}
	var upstreamGroups []struct {
		ID      int     `json:"id"`
		Name    string  `json:"name"`
		Display string  `json:"display_name"`
		Default float64 `json:"rate_multiplier"`
		Peak    bool    `json:"peak_rate_enabled"`
	}
	var rates map[string]float64
	if err := common.Unmarshal(groupsData, &upstreamGroups); err != nil || upstreamGroups == nil {
		return nil, newSub2APIRatioSyncError("上游分组响应格式不正确。", http.StatusBadGateway, false)
	}
	if len(ratesData) != 0 && string(ratesData) != "null" {
		if err := common.Unmarshal(ratesData, &rates); err != nil {
			return nil, newSub2APIRatioSyncError("上游专属倍率响应格式不正确。", http.StatusBadGateway, false)
		}
	}
	out := make([]sub2APIRatioSyncGroup, 0, len(upstreamGroups))
	seen := make(map[int]struct{}, len(upstreamGroups))
	for _, group := range upstreamGroups {
		if group.ID < 1 || group.Name == "" || math.IsNaN(group.Default) || math.IsInf(group.Default, 0) || group.Default < 0 {
			return nil, newSub2APIRatioSyncError("上游返回无效分组或倍率。", http.StatusBadGateway, false)
		}
		if _, exists := seen[group.ID]; exists {
			return nil, newSub2APIRatioSyncError("上游分组 ID 重复。", http.StatusBadGateway, false)
		}
		seen[group.ID] = struct{}{}
		custom, exists := rates[fmt.Sprint(group.ID)]
		var customPtr *float64
		if exists {
			if math.IsNaN(custom) || math.IsInf(custom, 0) || custom < 0 {
				return nil, newSub2APIRatioSyncError("上游专属倍率无效。", http.StatusBadGateway, false)
			}
			customPtr = &custom
		}
		name := group.Name
		if strings.TrimSpace(group.Display) != "" {
			name = group.Display
		}
		out = append(out, sub2APIRatioSyncGroup{ID: group.ID, Name: name, CanonicalName: group.Name, Default: group.Default, Custom: customPtr, Effective: group.Default, Peak: group.Peak})
		if customPtr != nil {
			out[len(out)-1].Effective = custom
		}
	}
	return out, nil
}

func sub2APISameRatios(left, right map[string]float64) bool {
	if len(left) != len(right) {
		return false
	}
	for key, value := range left {
		other, ok := right[key]
		if !ok || value != other {
			return false
		}
	}
	return true
}

func computeSub2APIRatioSyncPlan(state sub2APIRatioSyncState, groups []sub2APIRatioSyncGroup, current map[string]float64) (map[string]float64, []map[string]any, []map[string]any, error) {
	if state.Settings.MappingScope != state.Settings.UpstreamURL {
		return nil, nil, nil, errors.New("站点已变更，请重新核对并保存分组映射")
	}
	byID := make(map[int]sub2APIRatioSyncGroup, len(groups))
	for _, group := range groups {
		byID[group.ID] = group
	}
	updated := make(map[string]float64, len(current))
	for name, rate := range current {
		if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
			return nil, nil, nil, errors.New("当前分组倍率包含无效值，请先修复配置")
		}
		updated[name] = rate
	}
	rows := make([]map[string]any, 0, len(state.Settings.Mappings))
	warnings := make([]map[string]any, 0)
	for _, mapping := range state.Settings.Mappings {
		if !mapping.Enabled {
			continue
		}
		group, upstreamExists := byID[mapping.SourceID]
		_, downstreamExists := current[mapping.Target]
		if !upstreamExists || !downstreamExists {
			missing := make([]string, 0, 2)
			if !upstreamExists {
				missing = append(missing, "upstream")
			}
			if !downstreamExists {
				missing = append(missing, "downstream")
			}
			warnings = append(warnings, map[string]any{"source_id": mapping.SourceID, "target": mapping.Target, "missing": missing, "message": "分组不存在或不可用，已跳过映射。"})
			continue
		}
		if group.Peak && !mapping.AllowPeak && mapping.Mode != "fixed" {
			return nil, nil, nil, errors.New("上游已启用高峰倍率，请为此映射明确允许同步基础倍率")
		}
		sourceRate := mapping.Value
		after := mapping.Value
		switch mapping.Mode {
		case "effective":
			sourceRate = group.Effective
			after = sourceRate * mapping.Value
		case "default":
			sourceRate = group.Default
			after = sourceRate * mapping.Value
		}
		if math.IsNaN(after) || math.IsInf(after, 0) || after < 0 {
			return nil, nil, nil, errors.New("计算后的倍率超出有效范围")
		}
		rounded, parseErr := strconv.ParseFloat(strconv.FormatFloat(after, 'g', 12, 64), 64)
		if parseErr != nil {
			return nil, nil, nil, errors.New("计算后的倍率超出有效范围")
		}
		after = rounded
		before := current[mapping.Target]
		updated[mapping.Target] = after
		rows = append(rows, map[string]any{"source": group.Name, "source_id": group.ID, "source_rate": sourceRate, "target": mapping.Target, "before": before, "after": after, "changed": before != after})
	}
	if len(rows) == 0 && len(warnings) == 0 {
		return nil, nil, nil, errors.New("请先添加并启用分组映射")
	}
	return updated, rows, warnings, nil
}

func appendSub2APIRatioSyncLog(state *sub2APIRatioSyncState, level, message string, details map[string]any) {
	maxID := int64(0)
	if len(state.Runtime.Logs) > 0 {
		maxID = state.Runtime.Logs[0].ID
	}
	state.Runtime.Logs = append([]sub2APIRatioSyncLog{{ID: maxID + 1, At: time.Now().Unix(), Level: level, Message: message, Details: details}}, state.Runtime.Logs...)
	if len(state.Runtime.Logs) > sub2APIRatioSyncMaxLogs {
		state.Runtime.Logs = state.Runtime.Logs[:sub2APIRatioSyncMaxLogs]
	}
}

func executeSub2APIRatioSync(ctx context.Context, apply, monitor bool) (sub2APIRatioSyncResult, error) {
	sub2APIRatioSyncMu.Lock()
	defer sub2APIRatioSyncMu.Unlock()
	state, err := loadSub2APIRatioSyncState()
	if err != nil {
		return sub2APIRatioSyncResult{}, err
	}
	if monitor && !state.Settings.Enabled {
		return sub2APIRatioSyncResult{Mode: "skipped"}, nil
	}
	groups, err := fetchSub2APIRatioSyncGroups(ctx, &state)
	if err != nil {
		return sub2APIRatioSyncResult{}, recordSub2APIRatioSyncFailure(&state, err, monitor || apply)
	}
	current := ratio_setting.GetGroupRatioCopy()
	updated, rows, warnings, err := computeSub2APIRatioSyncPlan(state, groups, current)
	if err != nil {
		return sub2APIRatioSyncResult{}, recordSub2APIRatioSyncFailure(&state, err, monitor || apply)
	}
	initial := state.Runtime.Baseline == nil || len(state.Runtime.Baseline) == 0
	warningsChanged := !reflect.DeepEqual(warnings, state.Runtime.Warnings)
	changes := make([]map[string]any, 0)
	baseline := make(map[string]sub2APIRatioSyncBaseline, len(rows))
	if monitor {
		for key, value := range current {
			updated[key] = value
		}
	}
	for _, row := range rows {
		target := row["target"].(string)
		desired := row["after"].(float64)
		sourceRate := row["source_rate"].(float64)
		previous, exists := state.Runtime.Baseline[target]
		baseline[target] = sub2APIRatioSyncBaseline{Desired: desired, SourceRate: sourceRate}
		if monitor {
			eligible := !exists || previous.Desired != desired || slices.Contains(state.Runtime.Pending, target)
			if exists && previous.SourceRate != sourceRate {
				changes = append(changes, map[string]any{"source": row["source"], "target": target, "before": previous.SourceRate, "after": sourceRate})
			}
			row["changed"] = row["before"] != desired && eligible
			if eligible {
				updated[target] = desired
			}
		}
	}
	changed := 0
	for _, row := range rows {
		if row["changed"] == true {
			changed++
		}
	}
	backupID := int64(0)
	if apply && changed > 0 {
		if ctx.Err() != nil {
			return sub2APIRatioSyncResult{}, ctx.Err()
		}
		if !sub2APISameRatios(current, ratio_setting.GetGroupRatioCopy()) {
			return sub2APIRatioSyncResult{}, recordSub2APIRatioSyncFailure(&state, newSub2APIRatioSyncError("倍率配置刚刚被其他操作修改，请重新预览。", http.StatusConflict, true), true)
		}
		backupID = time.Now().UnixNano()
		state.Runtime.Backups = append([]sub2APIRatioSyncBackup{{ID: backupID, At: time.Now().Unix(), Before: current, After: updated}}, state.Runtime.Backups...)
		if len(state.Runtime.Backups) > sub2APIRatioSyncMaxBackups {
			state.Runtime.Backups = state.Runtime.Backups[:sub2APIRatioSyncMaxBackups]
		}
		if err := saveSub2APIRatioSyncState(state); err != nil {
			return sub2APIRatioSyncResult{}, err
		}
		encoded, marshalErr := common.Marshal(updated)
		if marshalErr != nil {
			return sub2APIRatioSyncResult{}, recordSub2APIRatioSyncFailure(&state, errors.New("编码倍率配置失败"), true)
		}
		if err := model.UpdateOptionsBulk(map[string]string{"GroupRatio": string(encoded)}); err != nil {
			return sub2APIRatioSyncResult{}, recordSub2APIRatioSyncFailure(&state, errors.New("写入倍率配置失败"), true)
		}
		if !sub2APISameRatios(updated, ratio_setting.GetGroupRatioCopy()) {
			return sub2APIRatioSyncResult{}, recordSub2APIRatioSyncFailure(&state, errors.New("倍率写入后核对不一致，请手动检查配置"), false)
		}
	}
	now := time.Now().Unix()
	result := sub2APIRatioSyncResult{At: now, Mode: "preview", Changed: changed, Rows: rows, Warnings: warnings, UpstreamChange: changes}
	if apply {
		result.Mode = "sync"
		result.BackupID = backupID
		state.Runtime.Baseline = baseline
		state.Runtime.Pending = []string{}
	}
	state.Runtime.Groups = groups
	if apply {
		state.Runtime.Ratios = updated
	} else {
		state.Runtime.Ratios = current
	}
	state.Runtime.Warnings = warnings
	if apply || monitor {
		state.Runtime.LastRun = now
		state.Runtime.NextRun = now + int64(state.Settings.Interval)
		state.Runtime.Failures = 0
		state.Runtime.LastResult = map[string]any{"ok": true, "changed": changed, "skipped_count": len(warnings), "at": now}
	}
	if !monitor || initial && len(rows) > 0 || changed > 0 || len(changes) > 0 || warningsChanged {
		message := "同步预览完成"
		if apply && monitor {
			message = "变价检测与自动同步完成"
		} else if apply {
			message = "倍率同步完成"
		}
		appendSub2APIRatioSyncLog(&state, "success", message, map[string]any{"mode": result.Mode, "changed": changed, "warnings": warnings, "backup_id": backupID})
	}
	if err := saveSub2APIRatioSyncState(state); err != nil {
		return sub2APIRatioSyncResult{}, err
	}
	return result, nil
}

func recordSub2APIRatioSyncFailure(state *sub2APIRatioSyncState, err error, updateRuntime bool) error {
	if updateRuntime {
		now := time.Now().Unix()
		state.Runtime.LastRun = now
		state.Runtime.LastResult = map[string]any{"ok": false, "message": err.Error(), "at": now}
		var syncErr *sub2APIRatioSyncError
		if errors.As(err, &syncErr) && syncErr.Retryable && state.Settings.Enabled {
			state.Runtime.Failures = min(state.Runtime.Failures+1, 6)
			state.Runtime.NextRun = now + int64(sub2APIRatioSyncRetryDelay(state.Settings.Interval, state.Runtime.Failures))
		} else if state.Settings.Enabled {
			state.Settings.Enabled = false
			state.Runtime.NextRun = 0
		}
		appendSub2APIRatioSyncLog(state, "error", err.Error(), nil)
		if saveErr := saveSub2APIRatioSyncState(*state); saveErr != nil {
			return errors.Join(err, saveErr)
		}
	}
	return err
}

func GetSub2APIRatioSyncState(c *gin.Context) {
	state, err := loadSub2APIRatioSyncState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	state.Runtime.Ratios = ratio_setting.GetGroupRatioCopy()
	common.ApiSuccess(c, publicSub2APIRatioSyncState(state))
}

func SaveSub2APIRatioSyncSettings(c *gin.Context) {
	var request struct {
		UpstreamURL string `json:"upstream_url"`
		Interval    int    `json:"interval_seconds"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	if request.Interval < 60 || request.Interval > 86400 {
		common.ApiError(c, errors.New("检测间隔须为 60～86400 秒"))
		return
	}
	upstreamURL, err := normalizeSub2APIBaseURL(request.UpstreamURL)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	sub2APIRatioSyncMu.Lock()
	defer sub2APIRatioSyncMu.Unlock()
	state, err := loadSub2APIRatioSyncState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if state.Settings.UpstreamURL != upstreamURL {
		state.Secrets.AccessToken = ""
		state.Secrets.RefreshToken = ""
		state.Secrets.ExpiresAt = 0
		state.Secrets.Pairs = []sub2APIRatioSyncPair{}
		state.Runtime.Groups = []sub2APIRatioSyncGroup{}
		state.Settings.MappingScope = ""
		state.Settings.Enabled = false
		state.Runtime.Baseline = map[string]sub2APIRatioSyncBaseline{}
		state.Runtime.NextRun = 0
	}
	state.Settings.UpstreamURL = upstreamURL
	state.Settings.Interval = request.Interval
	state.Runtime.Failures = 0
	if err := saveSub2APIRatioSyncState(state); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, publicSub2APIRatioSyncState(state))
}

func ImportSub2APIRatioSyncTokens(c *gin.Context) {
	var request struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	access, err := cleanSub2APIToken(request.AccessToken)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	refresh, err := cleanSub2APIToken(request.RefreshToken)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if access == "" && refresh == "" {
		common.ApiError(c, errors.New("至少填写一个上游会话令牌"))
		return
	}
	sub2APIRatioSyncMu.Lock()
	defer sub2APIRatioSyncMu.Unlock()
	state, err := loadSub2APIRatioSyncState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if state.Settings.UpstreamURL == "" {
		common.ApiError(c, errors.New("请先保存上游站点地址"))
		return
	}
	state.Secrets.AccessToken = access
	state.Secrets.RefreshToken = refresh
	state.Secrets.ExpiresAt = 0
	state.Secrets.Pairs = []sub2APIRatioSyncPair{}
	state.Settings.Enabled = false
	state.Runtime.Failures = 0
	state.Runtime.Groups = []sub2APIRatioSyncGroup{}
	state.Runtime.Baseline = map[string]sub2APIRatioSyncBaseline{}
	state.Runtime.NextRun = 0
	appendSub2APIRatioSyncLog(&state, "info", "上游会话凭据已安全保存", nil)
	if err := saveSub2APIRatioSyncState(state); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, publicSub2APIRatioSyncState(state))
}

func ClearSub2APIRatioSyncTokens(c *gin.Context) {
	sub2APIRatioSyncMu.Lock()
	defer sub2APIRatioSyncMu.Unlock()
	state, err := loadSub2APIRatioSyncState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	state.Secrets = sub2APIRatioSyncSecrets{Pairs: []sub2APIRatioSyncPair{}}
	state.Settings.Enabled = false
	state.Runtime.Failures = 0
	state.Runtime.NextRun = 0
	state.Runtime.Baseline = map[string]sub2APIRatioSyncBaseline{}
	if err := saveSub2APIRatioSyncState(state); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, publicSub2APIRatioSyncState(state))
}

func RefreshSub2APIRatioSyncGroups(c *gin.Context) {
	sub2APIRatioSyncMu.Lock()
	defer sub2APIRatioSyncMu.Unlock()
	state, err := loadSub2APIRatioSyncState()
	if err == nil {
		state.Runtime.Groups, err = fetchSub2APIRatioSyncGroups(c.Request.Context(), &state)
	}
	if err == nil {
		state.Runtime.Ratios = ratio_setting.GetGroupRatioCopy()
		err = saveSub2APIRatioSyncState(state)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, publicSub2APIRatioSyncState(state))
}

func SaveSub2APIRatioSyncMappings(c *gin.Context) {
	var request struct {
		Mappings []sub2APIRatioSyncMapping `json:"mappings"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	if err := validateSub2APIRatioSyncMappings(request.Mappings); err != nil {
		common.ApiError(c, err)
		return
	}
	sub2APIRatioSyncMu.Lock()
	defer sub2APIRatioSyncMu.Unlock()
	state, err := loadSub2APIRatioSyncState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if state.Settings.UpstreamURL == "" {
		common.ApiError(c, errors.New("请先保存上游站点地址"))
		return
	}
	state.Settings.Mappings = request.Mappings
	state.Settings.MappingScope = state.Settings.UpstreamURL
	state.Settings.Enabled = false
	state.Runtime.Failures = 0
	state.Runtime.Baseline = map[string]sub2APIRatioSyncBaseline{}
	state.Runtime.Pending = []string{}
	state.Runtime.NextRun = 0
	appendSub2APIRatioSyncLog(&state, "info", "分组映射已保存，自动监测已停止", nil)
	if err := saveSub2APIRatioSyncState(state); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, publicSub2APIRatioSyncState(state))
}

func PreviewSub2APIRatioSync(c *gin.Context) {
	result, err := executeSub2APIRatioSync(c.Request.Context(), false, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func RunSub2APIRatioSync(c *gin.Context) {
	result, err := executeSub2APIRatioSync(c.Request.Context(), true, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func StartSub2APIRatioSync(c *gin.Context) {
	var request struct {
		Interval int `json:"interval_seconds"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	if request.Interval < 60 || request.Interval > 86400 {
		common.ApiError(c, errors.New("检测间隔须为 60～86400 秒"))
		return
	}
	sub2APIRatioSyncMu.Lock()
	state, err := loadSub2APIRatioSyncState()
	if err == nil {
		state.Settings.Interval = request.Interval
		state.Settings.Enabled = true
		state.Runtime.Failures = 0
		state.Runtime.Baseline = map[string]sub2APIRatioSyncBaseline{}
		state.Runtime.NextRun = 0
		err = saveSub2APIRatioSyncState(state)
	}
	sub2APIRatioSyncMu.Unlock()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	result, err := executeSub2APIRatioSync(c.Request.Context(), true, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, result)
}

func StopSub2APIRatioSync(c *gin.Context) {
	sub2APIRatioSyncMu.Lock()
	defer sub2APIRatioSyncMu.Unlock()
	state, err := loadSub2APIRatioSyncState()
	if err == nil {
		state.Settings.Enabled = false
		state.Runtime.Failures = 0
		state.Runtime.Baseline = map[string]sub2APIRatioSyncBaseline{}
		state.Runtime.Pending = []string{}
		state.Runtime.NextRun = 0
		err = saveSub2APIRatioSyncState(state)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, publicSub2APIRatioSyncState(state))
}

func GetSub2APIRatioSyncBackup(c *gin.Context) {
	var id int64
	if _, err := fmt.Sscan(c.Param("id"), &id); err != nil || id <= 0 {
		common.ApiError(c, errors.New("备份编号无效"))
		return
	}
	state, err := loadSub2APIRatioSyncState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	for _, backup := range state.Runtime.Backups {
		if backup.ID != id {
			continue
		}
		encoded, err := common.Marshal(map[string]any{"created_at": backup.At, "before": backup.Before, "after": backup.After})
		if err != nil {
			common.ApiError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=group-ratio-backup-%d.json", backup.ID))
		c.Data(http.StatusOK, "application/json; charset=utf-8", encoded)
		return
	}
	common.ApiError(c, errors.New("备份不存在"))
}

func CreateSub2APIRatioSyncPair(c *gin.Context) {
	sub2APIRatioSyncMu.Lock()
	defer sub2APIRatioSyncMu.Unlock()
	state, err := loadSub2APIRatioSyncState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if state.Settings.UpstreamURL == "" {
		common.ApiError(c, errors.New("请先保存上游站点地址"))
		return
	}
	rawTokenBytes := make([]byte, 64)
	if _, err := rand.Read(rawTokenBytes); err != nil {
		common.ApiError(c, err)
		return
	}
	rawToken := hex.EncodeToString(rawTokenBytes)
	state.Secrets.Pairs = []sub2APIRatioSyncPair{{TokenHash: sub2APIRatioSyncPairHash(rawToken), Site: state.Settings.UpstreamURL, ExpiresAt: time.Now().Add(10 * time.Minute).Unix()}}
	state.Settings.Enabled = false
	state.Runtime.Failures = 0
	if err := saveSub2APIRatioSyncState(state); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"pair": rawToken, "expires_at": state.Secrets.Pairs[0].ExpiresAt, "url": state.Settings.UpstreamURL + "/login#ratesync_pair=" + rawToken})
}

func sub2APIRatioSyncPairHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func CheckSub2APIRatioSyncPair(c *gin.Context) {
	var request struct {
		Pair string `json:"pair"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil || len(request.Pair) != 128 {
		common.ApiError(c, errors.New("登录配对无效"))
		return
	}
	state, err := loadSub2APIRatioSyncState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	hash := sub2APIRatioSyncPairHash(request.Pair)
	for _, pair := range state.Secrets.Pairs {
		if pair.TokenHash == hash {
			common.ApiSuccess(c, gin.H{"done": pair.UsedAt > 0, "expired": pair.ExpiresAt < time.Now().Unix()})
			return
		}
	}
	common.ApiSuccess(c, gin.H{"done": false, "expired": true})
}

func ReceiveSub2APIRatioSyncPair(c *gin.Context) {
	var request struct {
		Pair         string `json:"pair"`
		Site         string `json:"site"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiError(c, err)
		return
	}
	if len(request.Pair) != 128 || len(request.Site) > 2048 {
		common.ApiError(c, newSub2APIRatioSyncError("登录配对无效", http.StatusForbidden, false))
		return
	}
	site, err := normalizeSub2APIBaseURL(request.Site)
	if err != nil {
		common.ApiError(c, newSub2APIRatioSyncError("登录站点不匹配", http.StatusForbidden, false))
		return
	}
	access, err := cleanSub2APIToken(request.AccessToken)
	if err != nil || access == "" {
		common.ApiError(c, newSub2APIRatioSyncError("未取得有效的登录令牌", http.StatusBadRequest, false))
		return
	}
	refresh, err := cleanSub2APIToken(request.RefreshToken)
	if err != nil {
		common.ApiError(c, newSub2APIRatioSyncError("上游刷新令牌格式不正确", http.StatusBadRequest, false))
		return
	}
	sub2APIRatioSyncMu.Lock()
	defer sub2APIRatioSyncMu.Unlock()
	state, err := loadSub2APIRatioSyncState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if site != state.Settings.UpstreamURL {
		common.ApiError(c, newSub2APIRatioSyncError("登录站点不匹配", http.StatusForbidden, false))
		return
	}
	hash := sub2APIRatioSyncPairHash(request.Pair)
	for i := range state.Secrets.Pairs {
		pair := &state.Secrets.Pairs[i]
		if subtle.ConstantTimeCompare([]byte(pair.TokenHash), []byte(hash)) != 1 || pair.ExpiresAt < time.Now().Unix() {
			continue
		}
		if pair.UsedAt > 0 {
			common.ApiSuccess(c, gin.H{"received": true})
			return
		}
		pair.UsedAt = time.Now().Unix()
		state.Secrets.AccessToken = access
		state.Secrets.RefreshToken = refresh
		state.Secrets.ExpiresAt = 0
		state.Runtime.Groups = []sub2APIRatioSyncGroup{}
		state.Settings.Enabled = false
		state.Runtime.Failures = 0
		state.Runtime.Baseline = map[string]sub2APIRatioSyncBaseline{}
		appendSub2APIRatioSyncLog(&state, "success", "浏览器登录已完成，上游会话已加密保存", nil)
		if err := saveSub2APIRatioSyncState(state); err != nil {
			common.ApiError(c, err)
			return
		}
		common.ApiSuccess(c, gin.H{"received": true})
		return
	}
	common.ApiError(c, newSub2APIRatioSyncError("登录配对已过期，请重新打开上游登录", http.StatusForbidden, false))
}

func GetSub2APIRatioSyncLoginHelper(c *gin.Context) {
	state, err := loadSub2APIRatioSyncState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if state.Settings.UpstreamURL == "" {
		common.ApiError(c, errors.New("请先保存上游站点地址"))
		return
	}
	upstream, err := url.Parse(state.Settings.UpstreamURL)
	if err != nil {
		common.ApiError(c, errors.New("上游站点地址无效"))
		return
	}
	webHost := c.Request.Host
	common.OptionMapRWMutex.RLock()
	serverAddress := common.OptionMap["ServerAddress"]
	common.OptionMapRWMutex.RUnlock()
	receiverURL, err := sub2APIRatioSyncReceiverURL(c.Request.Host, c.Request.TLS != nil, serverAddress)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	namespaceHash := sha256.Sum256([]byte(receiverURL))
	namespace := "sub2api-ratio-sync-" + hex.EncodeToString(namespaceHash[:6])
	siteJSON, err := common.Marshal(state.Settings.UpstreamURL)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	receiverJSON, err := common.Marshal(receiverURL)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	origin := upstream.Scheme + "://" + upstream.Host
	script := fmt.Sprintf(`// ==UserScript==
// @name         Sub2API 登录助手
// @namespace    %s
// @version      1.0.0
// @description  仅在倍率同步台发起配对后转交上游会话
// @match        %s/*
// @connect      %s
// @run-at       document-start
// @grant        GM_xmlhttpRequest
// @noframes
// @updateURL    none
// @downloadURL  none
// ==/UserScript==
(function(){"use strict";const SITE=%s,RECEIVER=%s,SLOT=%s;if(location.origin!==new URL(SITE).origin)return;const incoming=/^#ratesync_pair=([a-f0-9]{128})$/.exec(location.hash);if(incoming){sessionStorage.setItem(SLOT,JSON.stringify({pair:incoming[1],until:Date.now()+600000}));history.replaceState(null,"",location.pathname+location.search);}let busy=false,stopped=false;const timer=setInterval(()=>{if(busy||stopped)return;let pair;try{pair=JSON.parse(sessionStorage.getItem(SLOT)||"null")}catch{return}if(!pair||!/^[a-f0-9]{128}$/.test(pair.pair))return;if(Date.now()>pair.until){sessionStorage.removeItem(SLOT);clearInterval(timer);return}const access=localStorage.getItem("auth_token");if(!access)return;const expiry=Number(localStorage.getItem("token_expires_at"));if(expiry&&expiry<=Date.now()+30000)return;busy=true;GM_xmlhttpRequest({method:"POST",url:RECEIVER,anonymous:true,timeout:20000,headers:{"Content-Type":"application/json"},data:JSON.stringify({pair:pair.pair,site:SITE,access_token:access,refresh_token:localStorage.getItem("refresh_token")||""}),onload(response){busy=false;let body;try{body=JSON.parse(response.responseText)}catch{return}if(response.status===200&&body.success){stopped=true;sessionStorage.removeItem(SLOT);clearInterval(timer);window.close();if(!window.closed)alert("会话已交给倍率同步台，请关闭此页并返回管理页面。")}else if(response.status>=400&&response.status<500){stopped=true;sessionStorage.removeItem(SLOT);clearInterval(timer);alert("登录配对无效或已过期，请重新发起。")}},onerror(){busy=false},ontimeout(){busy=false}})},1500)})();`, namespace, origin, webHost, string(siteJSON), string(receiverJSON), namespace)
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, "application/javascript; charset=utf-8", []byte(script))
}

type sub2APIRatioSyncHandler struct{}

func (sub2APIRatioSyncHandler) Type() string    { return sub2APIRatioSyncTaskType }
func (sub2APIRatioSyncHandler) NewPayload() any { return nil }
func (sub2APIRatioSyncHandler) Enabled() bool {
	state, err := loadSub2APIRatioSyncState()
	return err == nil && state.Settings.Enabled && (state.Secrets.AccessToken != "" || state.Secrets.RefreshToken != "") &&
		(state.Runtime.NextRun == 0 || state.Runtime.NextRun <= time.Now().Unix())
}
func (sub2APIRatioSyncHandler) Interval() time.Duration {
	state, err := loadSub2APIRatioSyncState()
	if err != nil || state.Settings.Interval < 60 || state.Settings.Interval > 86400 {
		return time.Duration(sub2APIRatioSyncInterval) * time.Second
	}
	return time.Duration(state.Settings.Interval) * time.Second
}
func (sub2APIRatioSyncHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	result, err := executeSub2APIRatioSync(ctx, true, true)
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, result, nil)
}
