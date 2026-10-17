package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

var jailbreakScanSlots = make(chan struct{}, 32)

var offlineJailbreakPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bignore\s+(?:(?:all|any|the)\s+)?(?:previous|prior|preceding|above)\s+(?:(?:system|developer)\s+)?(?:instructions?|rules?|prompts?)\b`),
	regexp.MustCompile(`(?i)\b(?:disregard|forget)\s+(?:(?:all|any)\s+)?(?:previous|prior|preceding|above)\s+(?:(?:system|developer)\s+)?(?:instructions?|rules?|prompts?)\b`),
	regexp.MustCompile(`(?i)\b(?:reveal|print|repeat|show|expose)\s+(?:the\s+)?(?:(?:hidden|system|developer)\s+)*(?:prompt|instructions?)\b`),
	regexp.MustCompile(`(?i)\b(?:enable|activate|enter)\s+(?:the\s+)?(?:developer|unrestricted|dan)\s+mode\b`),
	regexp.MustCompile(`忽略(?:掉)?(?:(?:此前|之前|先前|上文|以上|前面)(?:的)?(?:所有|全部)?|所有(?:此前|之前|先前|上文|以上|前面)?(?:的)?)(?:系统|开发者)?(?:指令|规则|提示词|提示)`),
	regexp.MustCompile(`无视(?:(?:此前|之前|先前|上文|以上|前面)(?:的)?(?:所有|全部)?|所有(?:此前|之前|先前|上文|以上|前面)?(?:的)?)(?:系统|开发者)?(?:指令|规则|提示词|提示)`),
	regexp.MustCompile(`(?:输出|泄露|打印|复述|展示|给出)(?:全部|完整|隐藏的?)?(?:系统|开发者)(?:提示词|指令|消息)`),
	regexp.MustCompile(`(?i)(?:开启|启用|进入)(?:开发者|无限制|DAN|越狱)模式`),
}

// Each group selection rechecks the whitelist. Only a completed scan is reused
// within this request; exemptions never authorize a later group outside the list.
func CheckJailbreakRequest(c *gin.Context) *types.NewAPIError {
	policy := model.CurrentRequestPolicy()
	if !policy.Jailbreak.Enabled {
		return nil
	}
	group := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	if group == "auto" {
		if selected := common.GetContextKeyString(c, constant.ContextKeyAutoGroup); selected != "" {
			group = selected
		}
	}
	if slices.Contains(policy.Jailbreak.AllowedGroups, group) {
		return nil
	}
	if c.Request.Method == http.MethodGet && !strings.HasSuffix(c.Request.URL.Path, "/responses") {
		if strings.HasSuffix(c.Request.URL.Path, "/realtime") {
			return jailbreakUnavailable()
		}
		return nil
	}
	if checked, _ := c.Get("jailbreak_checked_policy"); checked == policy {
		return nil
	}
	var body map[string]any
	mediaType, _, mediaErr := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if mediaErr == nil && (mediaType == "multipart/form-data" || mediaType == "application/x-www-form-urlencoded") {
		if err := common.UnmarshalBodyReusable(c, &body); err != nil {
			return jailbreakUnavailable()
		}
	} else {
		// Decode JSON regardless of Content-Type: relay accepts missing headers,
		// whereas UnmarshalBodyReusable silently skips unsupported MIME types.
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return jailbreakUnavailable()
		}
		if err = common.DecodeJson(storage, &body); err != nil {
			return jailbreakUnavailable()
		}
		if _, err = storage.Seek(0, io.SeekStart); err != nil {
			return jailbreakUnavailable()
		}
		c.Request.Body = io.NopCloser(storage)
	}
	text, textErr := jailbreakPromptText(body)
	if textErr != nil || body == nil {
		return jailbreakUnavailable()
	}
	text = prioritizeLatestJailbreakUserPrompt(body, text)
	if text == "" {
		c.Set("jailbreak_checked_policy", policy)
		return nil
	}
	if policy.Jailbreak.Mode != model.JailbreakModeOnline {
		if policy.Jailbreak.Mode != model.JailbreakModeOffline && policy.Jailbreak.Mode != model.JailbreakModeHybrid {
			return jailbreakUnavailable()
		}
		if matchesOfflineJailbreak(text) {
			return interceptJailbreakRequest(c, policy.Jailbreak, group)
		}
		if policy.Jailbreak.Mode == model.JailbreakModeOffline {
			c.Set("jailbreak_checked_policy", policy)
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	select {
	case jailbreakScanSlots <- struct{}{}:
		defer func() { <-jailbreakScanSlots }()
	default:
		return jailbreakUnavailable()
	}
	channel, err := model.CacheGetChannel(policy.Jailbreak.ChannelID)
	if err != nil || channel.Status != common.ChannelStatusEnabled || channel.Type != constant.ChannelTypeOpenAI {
		return jailbreakUnavailable()
	}
	key, _, keyErr := channel.GetNextEnabledKey()
	if keyErr != nil {
		return jailbreakUnavailable()
	}
	settings := channel.GetSetting()
	client, err := GetHttpClientWithProxySettings(settings.Proxy, settings)
	if err != nil {
		return jailbreakUnavailable()
	}
	input := []rune(text)
	if len(input) > 256_000 {
		return jailbreakUnavailable()
	}
	for start := 0; start < len(input); {
		end := min(start+8000, len(input))
		blocked, err := scanJailbreakChunk(ctx, client, channel, key, policy.Jailbreak.Model, string(input[start:end]))
		if err != nil {
			logger.LogWarn(c, "jailbreak detector unavailable channel=%d", channel.Id)
			return jailbreakUnavailable()
		}
		if blocked {
			return interceptJailbreakRequest(c, policy.Jailbreak, group)
		}
		if end == len(input) {
			break
		}
		start = end - 256
	}
	c.Set("jailbreak_checked_policy", policy)
	return nil
}

func matchesOfflineJailbreak(text string) bool {
	for _, pattern := range offlineJailbreakPatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

func interceptJailbreakRequest(c *gin.Context, policy model.JailbreakPolicy, group string) *types.NewAPIError {
	count, banned, err := model.RecordJailbreakInterception(c.GetInt("id"), policy.BanThreshold)
	if err != nil {
		return jailbreakUnavailable()
	}
	logger.LogWarn(c, "jailbreak intercepted user=%d group=%q count=%d banned=%t", c.GetInt("id"), group, count, banned)
	RequestPolicy(c).AddEvent(PolicyEvent{ErrorCode: "jailbreak_detected", ErrorSource: "local", Decision: PolicyDecision{Action: "stop", Reason: "local_rejection", Source: "global"}, Health: "unchanged"})
	return types.NewErrorWithStatusCode(errors.New(policy.Replies[min(max(count, 1), len(policy.Replies))-1]), types.ErrorCode("jailbreak_detected"), http.StatusForbidden, types.ErrOptionWithSkipRetry())
}

func jailbreakUnavailable() *types.NewAPIError {
	return types.NewErrorWithStatusCode(errors.New("破甲检测暂不可用，请稍后重试。"), types.ErrorCode("jailbreak_detection_unavailable"), http.StatusServiceUnavailable, types.ErrOptionWithSkipRetry())
}

func jailbreakPromptText(body map[string]any) (string, error) {
	var text strings.Builder
	characters := 0
	// Providers and passthrough channels may use additional prompt fields.
	// Inspect their text too rather than trusting a fixed field-name allowlist.
	if err := appendJailbreakText(body, &text, &characters, 0, ""); err != nil {
		return "", err
	}
	return text.String(), nil
}

func prioritizeLatestJailbreakUserPrompt(body map[string]any, fullText string) string {
	latest := latestJailbreakUserPrompt(body)
	if latest == "" || fullText == "" {
		return fullText
	}
	index := strings.LastIndex(fullText, latest)
	if index < 0 {
		return fullText
	}
	remainder := fullText[:index] + fullText[index+len(latest):]
	if strings.TrimSpace(remainder) == "" {
		return fullText
	}
	prioritized := latest + "\n" + remainder
	if utf8.RuneCountInString(prioritized) > 256_000 {
		return fullText
	}
	return prioritized
}

func latestJailbreakUserPrompt(body map[string]any) string {
	for _, key := range []string{"messages", "Messages"} {
		if prompt := latestJailbreakPromptFromMessages(body[key], "content", key+"[].content"); prompt != "" {
			return prompt
		}
	}
	if prompt := latestJailbreakPromptFromMessages(body["input"], "content", "input[].content"); prompt != "" {
		return prompt
	}
	for _, key := range []string{"contents", "content"} {
		if prompt := latestJailbreakPromptFromMessages(body[key], "parts", key+"[].parts"); prompt != "" {
			return prompt
		}
	}
	if instances, ok := body["instances"].([]any); ok {
		for index := len(instances) - 1; index >= 0; index-- {
			if instance, ok := instances[index].(map[string]any); ok {
				if prompt := strings.TrimSpace(jailbreakStringValue(instance["prompt"])); prompt != "" {
					return prompt
				}
			}
		}
	}
	return strings.TrimSpace(jailbreakStringValue(body["prompt"]))
}

func jailbreakStringValue(value any) string {
	text, _ := value.(string)
	return text
}

func latestJailbreakPromptFromMessages(value any, contentKey, contentPath string) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		return latestJailbreakPromptFromMessages([]any{typed}, contentKey, contentPath)
	case []any:
		for index := len(typed) - 1; index >= 0; index-- {
			var content any
			switch item := typed[index].(type) {
			case string:
				return strings.TrimSpace(item)
			case map[string]any:
				role := strings.ToLower(strings.TrimSpace(jailbreakStringValue(item["role"])))
				if role != "" && role != "user" {
					continue
				}
				if value, exists := item[contentKey]; exists {
					content = value
				} else if value, exists := item["text"]; exists {
					content = value
				}
			default:
				continue
			}
			var candidate strings.Builder
			characters := 0
			if err := appendJailbreakText(content, &candidate, &characters, 0, contentPath); err == nil && strings.TrimSpace(candidate.String()) != "" {
				return strings.TrimSpace(candidate.String())
			}
		}
	}
	return ""
}

func appendJailbreakText(value any, text *strings.Builder, characters *int, depth int, path string) error {
	if depth > 64 {
		return errors.New("prompt nesting limit exceeded")
	}
	switch item := value.(type) {
	case string:
		if strings.TrimSpace(item) != "" {
			*characters += utf8.RuneCountInString(item) + 1
			if *characters > 256_000 {
				return errors.New("prompt inspection limit exceeded")
			}
			if text.Len() > 0 {
				text.WriteByte('\n')
			}
			text.WriteString(item)
		}
	case []any:
		for _, child := range item {
			if err := appendJailbreakText(child, text, characters, depth+1, path+"[]"); err != nil {
				return err
			}
		}
	case map[string]any:
		// Only protocol-defined media blocks omit binary payloads. Identical
		// field names inside tool arguments and provider extensions remain text.
		contentBlock := slices.Contains([]string{"messages[].content[]", "input[].content[]", "input[]", "system[]"}, path)
		geminiPart := slices.Contains([]string{"contents[].parts[]", "systemInstruction.parts[]", "system_instruction.parts[]"}, path)
		for _, key := range slices.Sorted(maps.Keys(item)) {
			if contentBlock && ((item["type"] == "image_url" && key == "image_url") ||
				(item["type"] == "input_image" && key == "image_url") ||
				(item["type"] == "image" && key == "source") ||
				(item["type"] == "input_audio" && key == "input_audio") ||
				(item["type"] == "input_file" && key == "file_data")) {
				continue
			}
			if geminiPart && slices.Contains([]string{"inlineData", "inline_data", "fileData", "file_data"}, key) {
				continue
			}
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if err := appendJailbreakText(item[key], text, characters, depth+1, childPath); err != nil {
				return err
			}
		}
	}
	return nil
}

func scanJailbreakChunk(ctx context.Context, client *http.Client, channel *model.Channel, key, detectorModel, text string) (bool, error) {
	encoded, err := common.Marshal(map[string]any{
		"model": detectorModel, "messages": []map[string]string{{"role": "user", "content": text}}, "stream": false, "temperature": 0, "max_tokens": 64, "seed": 42,
	})
	if err != nil {
		return false, err
	}
	base := strings.TrimRight(channel.GetBaseURL(), "/")
	parsed, err := url.Parse(base)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false, errors.New("invalid detector URL")
	}
	if !strings.HasSuffix(base, "/v1") {
		base += "/v1"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat/completions", bytes.NewReader(encoded))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	guardClient := *client
	guardClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	response, err := guardClient.Do(req)
	if err != nil {
		return false, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return false, fmt.Errorf("detector HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 64*1024+1))
	if err != nil || len(data) > 64*1024 {
		return false, errors.New("invalid detector response")
	}
	var result struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content any `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := common.Unmarshal(data, &result); err != nil || len(result.Choices) != 1 {
		return false, errors.New("invalid detector response")
	}
	if result.Choices[0].FinishReason != "" && result.Choices[0].FinishReason != "stop" {
		return false, errors.New("incomplete detector response")
	}
	content, err := extractJailbreakDetectorContent(result.Choices[0].Message.Content)
	if err != nil {
		return false, err
	}
	return parseJailbreakGuard(content)
}

func extractJailbreakDetectorContent(content any) (string, error) {
	switch value := content.(type) {
	case string:
		if strings.TrimSpace(value) != "" {
			return value, nil
		}
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			if block, ok := item.(map[string]any); ok {
				if text, ok := block["text"].(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, text)
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n"), nil
		}
	}
	return "", errors.New("invalid detector response content")
}

// Qwen3Guard's Safety/Categories contract matches sub2api's detector. Only
// jailbreak classifications contribute strikes; unrelated categories do not.
func parseJailbreakGuard(content string) (bool, error) {
	var safety, categories string
	hasSafety, hasCategories := false, false
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "safety":
			if hasSafety {
				return false, errors.New("duplicate safety")
			}
			safety = strings.ToLower(strings.TrimSpace(value))
			hasSafety = true
		case "categories":
			if hasCategories {
				return false, errors.New("duplicate categories")
			}
			categories = strings.ToLower(strings.TrimSpace(value))
			hasCategories = true
		default:
			// Qwen3Guard-compatible detectors may add auxiliary output fields.
		}
	}
	if !hasSafety || !hasCategories || !slices.Contains([]string{"safe", "unsafe", "controversial"}, safety) || categories == "" {
		return false, errors.New("invalid guard classification")
	}
	blocked := false
	for category := range strings.SplitSeq(categories, ",") {
		category = strings.Join(strings.Fields(strings.NewReplacer("_", " ", "-", " ", "&", " and ", "/", " ", "–", " ", "—", " ").Replace(category)), " ")
		if category == "none" || category == "n/a" {
			continue
		}
		if category == "jailbreak" || category == "prompt injection" {
			if safety == "safe" {
				return false, errors.New("conflicting guard classification")
			}
			blocked = true
		}
	}
	return blocked, nil
}
