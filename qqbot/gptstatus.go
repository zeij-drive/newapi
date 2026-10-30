package main

import (
	"bytes"
	"compress/zlib"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	gptStatusPageURL = "https://status.openai.com/"
	gptStatusMaxBody = 2 << 20
)

type gptStatusCommand string

const (
	gptStatusQuery   gptStatusCommand = "query"
	gptStatusInvalid gptStatusCommand = "invalid"
	gptStatusUsage   gptStatusCommand = "usage"
)

type gptStatusComponent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Description string `json:"description"`
}

type gptStatusSnapshot struct {
	Indicator   string
	Description string
	Components  []gptStatusComponent
}

type gptStatusFetcher interface {
	Fetch(context.Context) (gptStatusSnapshot, error)
}

type gptStatusClient struct {
	httpClient *http.Client
	pageURL    string
}

func newGPTStatusClient(client *http.Client) *gptStatusClient {
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	return &gptStatusClient{httpClient: client, pageURL: gptStatusPageURL}
}

func (client *gptStatusClient) Fetch(ctx context.Context) (gptStatusSnapshot, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.pageURL, nil)
	if err != nil {
		return gptStatusSnapshot{}, errors.New("could not create OpenAI status request")
	}
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	request.Header.Set("User-Agent", "StarryAPI-QQBot/1.0")
	response, err := client.httpClient.Do(request)
	if err != nil {
		return gptStatusSnapshot{}, errors.New("OpenAI status request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return gptStatusSnapshot{}, fmt.Errorf("OpenAI status returned HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, gptStatusMaxBody+1))
	if err != nil || len(body) > gptStatusMaxBody {
		return gptStatusSnapshot{}, errors.New("OpenAI status response is invalid")
	}
	return parseGPTStatusHTML(string(body))
}

var (
	statusClassPattern = regexp.MustCompile(`(?i)text-icon-([a-z-]+)`)
	h3Pattern          = regexp.MustCompile(`(?is)<h3\b[^>]*>(.*?)</h3>`)
	tagPattern         = regexp.MustCompile(`(?s)<[^>]*>`)
	statusLabelPattern = regexp.MustCompile(`(?is)<svg\b[^>]*class="[^"]*text-icon-[^"]*"[^>]*>.*?</svg>\s*([^<]+)`)
)

func parseGPTStatusHTML(source string) (gptStatusSnapshot, error) {
	if strings.TrimSpace(source) == "" {
		return gptStatusSnapshot{}, errors.New("OpenAI status page is empty")
	}
	decoded := html.UnescapeString(source)
	headsUpStart := strings.Index(decoded, `data-testid="heads-up"`)
	if headsUpStart < 0 {
		return gptStatusSnapshot{}, errors.New("OpenAI status page has no status banner")
	}
	systemStatusStart := strings.Index(decoded[headsUpStart:], `data-testid="system-status"`)
	if systemStatusStart < 0 {
		return gptStatusSnapshot{}, errors.New("OpenAI status page has no system status")
	}
	systemStatusStart += headsUpStart
	headsUp := decoded[headsUpStart:systemStatusStart]
	headsUpText := cleanGPTStatusHTMLText(headsUp)
	indicator := ""
	if matches := statusClassPattern.FindStringSubmatch(headsUp); len(matches) == 2 {
		indicator = normalizeGPTStatusIndicator(matches[1])
	}
	if indicator == "" {
		indicator = inferGPTStatusIndicator(headsUpText)
	}
	if indicator == "" || headsUpText == "" {
		return gptStatusSnapshot{}, errors.New("OpenAI status page has no status details")
	}
	description := firstGPTStatusSentence(headsUpText)
	if matches := statusLabelPattern.FindStringSubmatch(headsUp); len(matches) == 2 {
		if label := cleanGPTStatusHTMLText(matches[1]); label != "" {
			description = label
		}
	}
	systemStatus := decoded[systemStatusStart:]
	if footerStart := strings.Index(systemStatus, `data-testid="status-page-footer"`); footerStart >= 0 {
		systemStatus = systemStatus[:footerStart]
	}
	components := parseGPTStatusComponents(systemStatus)
	return newGPTStatusSnapshot(indicator, description, components), nil
}

func parseGPTStatusComponents(source string) []gptStatusComponent {
	matches := h3Pattern.FindAllStringIndex(source, -1)
	components := make([]gptStatusComponent, 0, len(matches))
	seenNames := make(map[string]struct{}, len(matches))
	for index, match := range matches {
		name := cleanGPTStatusHTMLText(source[match[0]:match[1]])
		if name == "" || len(name) > 160 {
			continue
		}
		nameKey := strings.ToLower(name)
		if _, exists := seenNames[nameKey]; exists {
			continue
		}
		segmentStart := 0
		if index > 0 {
			segmentStart = matches[index-1][1]
		}
		statusMatches := statusClassPattern.FindAllStringSubmatch(source[segmentStart:match[0]], -1)
		if len(statusMatches) == 0 {
			continue
		}
		status := normalizeGPTStatusIndicator(statusMatches[len(statusMatches)-1][1])
		if status == "" {
			continue
		}
		seenNames[nameKey] = struct{}{}
		components = append(components, gptStatusComponent{
			ID:     componentID(name),
			Name:   name,
			Status: status,
		})
	}
	return components
}

func componentID(name string) string {
	digest := sha256.Sum256([]byte(name))
	return hex.EncodeToString(digest[:8])
}

func cleanGPTStatusHTMLText(source string) string {
	text := tagPattern.ReplaceAllString(source, " ")
	return strings.Join(strings.Fields(html.UnescapeString(text)), " ")
}

func firstGPTStatusSentence(text string) string {
	if index := strings.IndexAny(text, ".。!"); index >= 0 {
		return strings.TrimSpace(text[:index+1])
	}
	return text
}

func normalizeGPTStatusIndicator(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "operational":
		return "operational"
	case "degraded", "degraded-performance":
		return "minor"
	case "partial-outage":
		return "major"
	case "full-outage":
		return "critical"
	case "maintenance", "under-maintenance":
		return "maintenance"
	default:
		return ""
	}
}

func inferGPTStatusIndicator(text string) string {
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "fully operational"), strings.Contains(lower, "no issues"):
		return "operational"
	case strings.Contains(lower, "full outage"):
		return "critical"
	case strings.Contains(lower, "maintenance"):
		return "maintenance"
	case strings.Contains(lower, "partial outage"):
		return "major"
	case strings.Contains(lower, "degraded"):
		return "minor"
	default:
		return ""
	}
}

func newGPTStatusSnapshot(indicator, description string, components []gptStatusComponent) gptStatusSnapshot {
	indicator = strings.TrimSpace(indicator)
	description = strings.TrimSpace(description)
	components = slices.Clone(components)
	slices.SortFunc(components, func(left, right gptStatusComponent) int {
		leftKey := left.ID + "\x00" + left.Name
		rightKey := right.ID + "\x00" + right.Name
		return strings.Compare(leftKey, rightKey)
	})
	return gptStatusSnapshot{
		Indicator:   indicator,
		Description: description,
		Components:  components,
	}
}

func (snapshot gptStatusSnapshot) Text() string {
	description := snapshot.Description
	if description == "" {
		description = "状态未知"
	}
	return "GPT 状态：" + description + "\n查看详情：https://status.openai.com/"
}

type gptStatusImagePayload struct {
	Indicator   string               `json:"indicator"`
	Description string               `json:"description"`
	Components  []gptStatusComponent `json:"components"`
}

func makeGPTStatusImageURL(publicURL, webhookPath, secret string, snapshot gptStatusSnapshot) (string, error) {
	base, err := url.Parse(strings.TrimSpace(publicURL))
	if err != nil || base.Host == "" || (base.Scheme != "https" && !(base.Scheme == "http" && isLoopbackHost(base.Hostname()))) || base.User != nil || (base.Path != "" && base.Path != "/") || base.RawQuery != "" || base.Fragment != "" {
		return "", errors.New("QQ_BOT_PUBLIC_URL must be an HTTPS origin, except loopback development")
	}
	if len(secret) < 32 {
		return "", errors.New("GPT status image signing secret is not configured")
	}
	imagePath := "/" + strings.Trim(webhookPath, "/") + "/gptstatus/image"
	if imagePath == "//gptstatus/image" {
		imagePath = "/gptstatus/image"
	}
	base.Path = imagePath
	payload, err := json.Marshal(gptStatusImagePayload{
		Indicator:   snapshot.Indicator,
		Description: snapshot.Description,
		Components:  snapshot.Components,
	})
	if err != nil {
		return "", errors.New("could not encode GPT status image")
	}
	var compressed strings.Builder
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(payload); err != nil {
		_ = writer.Close()
		return "", errors.New("could not compress GPT status image")
	}
	if err := writer.Close(); err != nil {
		return "", errors.New("could not compress GPT status image")
	}
	encoded := base64.RawURLEncoding.EncodeToString([]byte(compressed.String()))
	expires := time.Now().Add(15 * time.Minute).Unix()
	canonical := imagePath + "\n" + fmt.Sprintf("%d", expires) + "\n" + encoded
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	query := base.Query()
	query.Set("data", encoded)
	query.Set("exp", fmt.Sprintf("%d", expires))
	query.Set("sig", hex.EncodeToString(mac.Sum(nil)))
	base.RawQuery = query.Encode()
	return base.String(), nil
}

func decodeGPTStatusImageRequest(request *http.Request, secret string) (gptStatusSnapshot, error) {
	if len(secret) < 32 || len(request.URL.RawQuery) > 32<<10 {
		return gptStatusSnapshot{}, errors.New("invalid GPT status image request")
	}
	query := request.URL.Query()
	encoded, expiresText, signature := query.Get("data"), query.Get("exp"), query.Get("sig")
	if encoded == "" || len(encoded) > 24<<10 || expiresText == "" || len(signature) != sha256.Size*2 {
		return gptStatusSnapshot{}, errors.New("invalid GPT status image request")
	}
	expires, err := strconv.ParseInt(expiresText, 10, 64)
	if err != nil || expires < time.Now().Unix() || expires > time.Now().Add(16*time.Minute).Unix() {
		return gptStatusSnapshot{}, errors.New("expired GPT status image request")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(request.URL.Path + "\n" + expiresText + "\n" + encoded))
	provided, err := hex.DecodeString(signature)
	if err != nil || !hmac.Equal(provided, mac.Sum(nil)) {
		return gptStatusSnapshot{}, errors.New("invalid GPT status image signature")
	}
	compressed, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return gptStatusSnapshot{}, errors.New("invalid GPT status image payload")
	}
	reader, err := zlib.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return gptStatusSnapshot{}, errors.New("invalid GPT status image payload")
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, 16<<10))
	if err != nil || len(data) > 16<<10 {
		return gptStatusSnapshot{}, errors.New("invalid GPT status image payload")
	}
	var payload gptStatusImagePayload
	if err := json.Unmarshal(data, &payload); err != nil || len(payload.Components) > 64 || len(payload.Indicator) > 64 || len(payload.Description) > 512 {
		return gptStatusSnapshot{}, errors.New("invalid GPT status image payload")
	}
	return newGPTStatusSnapshot(payload.Indicator, payload.Description, payload.Components), nil
}

func renderGPTStatusPNG(snapshot gptStatusSnapshot) ([]byte, error) {
	const width, height = 800, 420
	canvas := image.NewRGBA(image.Rect(0, 0, width, height))
	fillRect(canvas, 0, 0, width, height, color.RGBA{R: 12, G: 16, B: 26, A: 255})
	fillRect(canvas, 24, 24, width-48, height-48, color.RGBA{R: 24, G: 31, B: 47, A: 255})
	accent := color.RGBA{R: 57, G: 196, B: 126, A: 255}
	switch strings.ToLower(snapshot.Indicator) {
	case "minor", "maintenance":
		accent = color.RGBA{R: 242, G: 190, B: 68, A: 255}
	case "major", "critical":
		accent = color.RGBA{R: 238, G: 86, B: 86, A: 255}
	}
	fillRect(canvas, 24, 24, width-48, 10, accent)
	white := color.RGBA{R: 246, G: 248, B: 252, A: 255}
	muted := color.RGBA{R: 163, G: 176, B: 198, A: 255}
	drawBitmapText(canvas, 54, 58, "OPENAI GPT STATUS", white, 4, 37)
	status := asciiUpper(snapshot.Description)
	if status == "" {
		status = "STATUS UNKNOWN"
	}
	drawBitmapText(canvas, 54, 112, status, accent, 3, 40)
	drawBitmapText(canvas, 54, 154, "STATUS.OPENAI.COM", muted, 2, 48)
	fills := 0
	for _, component := range snapshot.Components {
		if !strings.Contains(strings.ToLower(component.Name), "gpt") && !strings.Contains(strings.ToLower(component.Name), "api") {
			continue
		}
		line := asciiUpper(component.Name + " - " + component.Status)
		drawBitmapText(canvas, 54, 208+fills*30, line, white, 2, 53)
		fills++
		if fills >= 5 {
			break
		}
	}
	if fills == 0 {
		drawBitmapText(canvas, 54, 208, "OVERALL STATUS", muted, 2, 53)
		drawBitmapText(canvas, 54, 238, asciiUpper(snapshot.Indicator), white, 2, 53)
	}
	fillRect(canvas, 54, height-72, width-108, 2, color.RGBA{R: 55, G: 68, B: 90, A: 255})
	drawBitmapText(canvas, 54, height-58, "LIVE STATUS FROM OPENAI", muted, 2, 53)
	var output bytes.Buffer
	if err := png.Encode(&output, canvas); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func fillRect(canvas *image.RGBA, x, y, width, height int, fill color.RGBA) {
	for row := max(y, 0); row < min(y+height, canvas.Bounds().Dy()); row++ {
		for column := max(x, 0); column < min(x+width, canvas.Bounds().Dx()); column++ {
			canvas.SetRGBA(column, row, fill)
		}
	}
}

var statusFont = map[rune][7]string{
	' ': {"00000", "00000", "00000", "00000", "00000", "00000", "00000"},
	'-': {"00000", "00000", "00000", "11111", "00000", "00000", "00000"},
	'.': {"00000", "00000", "00000", "00000", "00000", "01100", "01100"},
	':': {"00000", "01100", "01100", "00000", "01100", "01100", "00000"},
	'/': {"00001", "00010", "00010", "00100", "01000", "01000", "10000"},
	'(': {"00010", "00100", "01000", "01000", "01000", "00100", "00010"},
	')': {"01000", "00100", "00010", "00010", "00010", "00100", "01000"},
	'0': {"01110", "10001", "10011", "10101", "11001", "10001", "01110"},
	'1': {"00100", "01100", "00100", "00100", "00100", "00100", "01110"},
	'2': {"01110", "10001", "00001", "00010", "00100", "01000", "11111"},
	'3': {"11110", "00001", "00001", "01110", "00001", "00001", "11110"},
	'4': {"00010", "00110", "01010", "10010", "11111", "00010", "00010"},
	'5': {"11111", "10000", "10000", "11110", "00001", "00001", "11110"},
	'6': {"01110", "10000", "10000", "11110", "10001", "10001", "01110"},
	'7': {"11111", "00001", "00010", "00100", "01000", "01000", "01000"},
	'8': {"01110", "10001", "10001", "01110", "10001", "10001", "01110"},
	'9': {"01110", "10001", "10001", "01111", "00001", "00001", "01110"},
	'A': {"01110", "10001", "10001", "11111", "10001", "10001", "10001"},
	'B': {"11110", "10001", "10001", "11110", "10001", "10001", "11110"},
	'C': {"01111", "10000", "10000", "10000", "10000", "10000", "01111"},
	'D': {"11110", "10001", "10001", "10001", "10001", "10001", "11110"},
	'E': {"11111", "10000", "10000", "11110", "10000", "10000", "11111"},
	'F': {"11111", "10000", "10000", "11110", "10000", "10000", "10000"},
	'G': {"01111", "10000", "10000", "10111", "10001", "10001", "01111"},
	'H': {"10001", "10001", "10001", "11111", "10001", "10001", "10001"},
	'I': {"01110", "00100", "00100", "00100", "00100", "00100", "01110"},
	'J': {"00111", "00010", "00010", "00010", "10010", "10010", "01100"},
	'K': {"10001", "10010", "10100", "11000", "10100", "10010", "10001"},
	'L': {"10000", "10000", "10000", "10000", "10000", "10000", "11111"},
	'M': {"10001", "11011", "10101", "10101", "10001", "10001", "10001"},
	'N': {"10001", "11001", "10101", "10011", "10001", "10001", "10001"},
	'O': {"01110", "10001", "10001", "10001", "10001", "10001", "01110"},
	'P': {"11110", "10001", "10001", "11110", "10000", "10000", "10000"},
	'Q': {"01110", "10001", "10001", "10001", "10101", "10010", "01101"},
	'R': {"11110", "10001", "10001", "11110", "10100", "10010", "10001"},
	'S': {"01111", "10000", "10000", "01110", "00001", "00001", "11110"},
	'T': {"11111", "00100", "00100", "00100", "00100", "00100", "00100"},
	'U': {"10001", "10001", "10001", "10001", "10001", "10001", "01110"},
	'V': {"10001", "10001", "10001", "10001", "10001", "01010", "00100"},
	'W': {"10001", "10001", "10001", "10101", "10101", "10101", "01010"},
	'X': {"10001", "10001", "01010", "00100", "01010", "10001", "10001"},
	'Y': {"10001", "10001", "01010", "00100", "00100", "00100", "00100"},
	'Z': {"11111", "00001", "00010", "00100", "01000", "10000", "11111"},
	'?': {"01110", "10001", "00001", "00010", "00100", "00000", "00100"},
}

func asciiUpper(value string) string {
	var result strings.Builder
	for _, char := range value {
		switch char {
		case '’', '‘':
			result.WriteByte('\'')
			continue
		case '–', '—':
			result.WriteByte('-')
			continue
		}
		if char > 126 || char < 32 {
			result.WriteByte('?')
			continue
		}
		result.WriteRune(rune(strings.ToUpper(string(char))[0]))
	}
	return result.String()
}

func drawBitmapText(canvas *image.RGBA, x, y int, text string, fill color.RGBA, scale, maxChars int) {
	text = asciiUpper(text)
	if len(text) > maxChars {
		text = text[:maxChars-3] + "..."
	}
	for index, char := range text {
		glyph, exists := statusFont[char]
		if !exists {
			glyph = statusFont['?']
		}
		for row, line := range glyph {
			for column, pixel := range line {
				if pixel != '1' {
					continue
				}
				fillRect(canvas, x+(index*6+column)*scale, y+row*scale, scale, scale, fill)
			}
		}
	}
}

func parseGPTStatusCommand(content string) (gptStatusCommand, bool) {
	content, ok := stripGroupMention(content)
	if !ok {
		return "", false
	}
	fields := strings.Fields(content)
	if len(fields) == 0 || !strings.EqualFold(fields[0], "/gptstatus") {
		return "", false
	}
	if len(fields) == 1 {
		return gptStatusQuery, true
	}
	if len(fields) == 2 && strings.EqualFold(fields[1], "help") {
		return gptStatusUsage, true
	}
	return gptStatusInvalid, true
}

func stripGroupMention(content string) (string, bool) {
	content = strings.TrimSpace(content)
	for strings.HasPrefix(content, "<@") {
		_, remainder, found := strings.Cut(content, ">")
		if !found {
			return "", false
		}
		content = strings.TrimSpace(remainder)
	}
	return content, true
}
