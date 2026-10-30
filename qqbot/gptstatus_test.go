package main

import (
	"context"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tencent-connect/botgo/dto"
)

const gptStatusHTMLFixture = `<!doctype html>
<html><body>
<div data-testid="heads-up" class="operational">
  <div><svg class="text-icon-operational"><path></path></svg>We’re fully operational</div>
  <div>We’re not aware of any issues affecting our systems.</div>
</div>
<div data-testid="system-status">
  <div class="h-7 flex flex-grow items-center"><svg class="text-icon-operational"><path></path></svg><h3>ChatGPT</h3></div>
  <div class="h-7 flex flex-grow items-center"><svg class="text-icon-degraded"><path></path></svg><h3>API</h3></div>
</div>
<div data-testid="status-page-footer"></div>
</body></html>`

type staticGPTStatusFetcher struct {
	snapshot gptStatusSnapshot
	err      error
}

func (fetcher *staticGPTStatusFetcher) Fetch(context.Context) (gptStatusSnapshot, error) {
	return fetcher.snapshot, fetcher.err
}

func TestParseGPTStatusCommand(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    gptStatusCommand
		matched bool
	}{
		{name: "query after group mention", content: "<@!bot-id> /gptstatus", want: gptStatusQuery, matched: true},
		{name: "help", content: "/gptstatus help", want: gptStatusUsage, matched: true},
		{name: "auto on removed", content: "/gptstatus auto on", want: gptStatusInvalid, matched: true},
		{name: "auto off removed", content: "/gptstatus auto off", want: gptStatusInvalid, matched: true},
		{name: "different command", content: "/status", matched: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command, matched := parseGPTStatusCommand(test.content)
			require.Equal(t, test.matched, matched)
			require.Equal(t, test.want, command)
		})
	}
}

func TestParseGPTStatusHTML(t *testing.T) {
	snapshot, err := parseGPTStatusHTML(gptStatusHTMLFixture)
	require.NoError(t, err)
	require.Equal(t, "operational", snapshot.Indicator)
	require.Equal(t, "We’re fully operational", snapshot.Description)
	require.Len(t, snapshot.Components, 2)
	components := make(map[string]string, len(snapshot.Components))
	for _, component := range snapshot.Components {
		components[component.Name] = component.Status
	}
	require.Equal(t, map[string]string{"API": "minor", "ChatGPT": "operational"}, components)
}

func TestParseGPTStatusHTMLDeduplicatesResponsiveComponents(t *testing.T) {
	body := strings.Replace(gptStatusHTMLFixture,
		`<div class="h-7 flex flex-grow items-center"><svg class="text-icon-operational"><path></path></svg><h3>ChatGPT</h3></div>`,
		`<div class="h-7 flex flex-grow items-center"><svg class="text-icon-operational"><path></path></svg><h3>ChatGPT</h3></div>`+"\n"+`<div class="h-7 flex flex-grow items-center"><svg class="text-icon-degraded"><path></path></svg><h3>ChatGPT</h3></div>`, 1)

	snapshot, err := parseGPTStatusHTML(body)
	require.NoError(t, err)
	require.Len(t, snapshot.Components, 2)
	components := make(map[string]string, len(snapshot.Components))
	for _, component := range snapshot.Components {
		components[component.Name] = component.Status
	}
	require.Equal(t, map[string]string{"API": "minor", "ChatGPT": "operational"}, components)
}

func TestParseGPTStatusHTMLMapsBannerAndRejectsMissingData(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		indicator string
		wantError bool
	}{
		{name: "degraded from icon", body: strings.ReplaceAll(gptStatusHTMLFixture, "text-icon-operational", "text-icon-degraded"), indicator: "minor"},
		{name: "partial outage", body: strings.Replace(gptStatusHTMLFixture, "text-icon-operational", "text-icon-partial-outage", 1), indicator: "major"},
		{name: "full outage", body: strings.Replace(gptStatusHTMLFixture, "text-icon-operational", "text-icon-full-outage", 1), indicator: "critical"},
		{name: "maintenance", body: strings.Replace(gptStatusHTMLFixture, "text-icon-operational", "text-icon-maintenance", 1), indicator: "maintenance"},
		{name: "missing banner", body: `<html><div data-testid="system-status"></div></html>`, wantError: true},
		{name: "missing status section", body: `<html><div data-testid="heads-up">We’re fully operational</div></html>`, wantError: true},
		{name: "unrecognized banner", body: `<div data-testid="heads-up"><svg class="text-icon-unknown"></svg>Status unavailable</div><div data-testid="system-status"></div>`, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot, err := parseGPTStatusHTML(test.body)
			if test.wantError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.indicator, snapshot.Indicator)
		})
	}
}

func TestGPTStatusClientFetchesHTMLSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodGet, request.Method)
		require.Contains(t, request.Header.Get("Accept"), "text/html")
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = writer.Write([]byte(gptStatusHTMLFixture))
	}))
	defer server.Close()

	client := newGPTStatusClient(server.Client())
	client.pageURL = server.URL
	snapshot, err := client.Fetch(context.Background())
	require.NoError(t, err)
	require.Equal(t, "operational", snapshot.Indicator)
	require.Equal(t, "We’re fully operational", snapshot.Description)
}

func TestGPTStatusClientRejectsMalformedPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(`<html><body>not a status page</body></html>`))
	}))
	defer server.Close()

	client := newGPTStatusClient(server.Client())
	client.pageURL = server.URL
	_, err := client.Fetch(context.Background())
	require.Error(t, err)
}

func TestGPTStatusImageTextPreservesCommonPunctuation(t *testing.T) {
	require.Equal(t, "WE'RE FULLY OPERATIONAL", asciiUpper("We’re fully operational"))
}

func TestGroupGPTStatusQuerySendsImageAndRemovedAutoCommandIsUnknown(t *testing.T) {
	snapshot := newGPTStatusSnapshot("operational", "We’re fully operational", []gptStatusComponent{
		{ID: "chat", Name: "ChatGPT", Status: "operational"},
	})
	sender := &testMessageSender{}
	bot := NewBot(sender, &testInviteClaimer{})
	bot.statusFetcher = &staticGPTStatusFetcher{snapshot: snapshot}
	secret := strings.Repeat("s", 32)
	bot.ConfigureGPTStatus("http://127.0.0.1:8080", "/qqbot", secret)
	handler := bot.GroupATMessageHandler()

	require.NoError(t, handler(nil, &dto.WSGroupATMessageData{
		ID:      "query-message",
		GroupID: "group-1",
		Content: "/gptstatus",
		Author:  &dto.User{ID: "qq-user"},
	}))
	require.Equal(t, "group-1", sender.groupID)
	require.Equal(t, "query-message", sender.messageID)
	require.NotEmpty(t, sender.imageURL)
	require.Contains(t, sender.caption, "We’re fully operational")

	require.NoError(t, handler(nil, &dto.WSGroupATMessageData{
		ID:      "removed-auto-message",
		GroupID: "group-1",
		Content: "/gptstatus auto on",
		Author:  &dto.User{ID: "qq-user"},
	}))
	require.Equal(t, commandNotFoundMessage, sender.content)
}

func TestGPTStatusSignedImageEndpoint(t *testing.T) {
	secret := strings.Repeat("s", 32)
	snapshot := newGPTStatusSnapshot("operational", "All systems operational", []gptStatusComponent{
		{ID: "chat", Name: "ChatGPT", Status: "operational"},
	})
	imageURL, err := makeGPTStatusImageURL("http://127.0.0.1:8080", "/qqbot", secret, snapshot)
	require.NoError(t, err)
	parsed, err := url.Parse(imageURL)
	require.NoError(t, err)
	request := httptest.NewRequest(http.MethodGet, parsed.RequestURI(), nil)
	response := httptest.NewRecorder()

	handler := newWebhookHandlerWithGPTStatus(nil, "/qqbot", secret)
	handler.ServeHTTP(response, request)
	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, "image/png", response.Header().Get("Content-Type"))
	_, err = png.Decode(response.Body)
	require.NoError(t, err)

	tamperedQuery := parsed.Query()
	tamperedQuery.Set("exp", "1")
	requestURL := parsed.Path + "?" + tamperedQuery.Encode()
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, requestURL, nil))
	require.Equal(t, http.StatusNotFound, response.Code)
}
