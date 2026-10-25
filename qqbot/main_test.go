package main

import (
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tencent-connect/botgo/dto"
	"github.com/tencent-connect/botgo/event"
	"github.com/tencent-connect/botgo/interaction/signature"
	"github.com/tencent-connect/botgo/token"
)

func TestReadConfigDefaultsAndValidation(t *testing.T) {
	values := map[string]string{
		"QQ_BOT_APP_ID":         "bot-app",
		"QQ_BOT_APP_SECRET":     "bot-secret",
		"NEW_API_URL":           "https://api.example.test",
		"STARRY_API_BOT_SECRET": strings.Repeat("x", 32),
	}
	lookup := func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
	settings, err := readConfig(lookup)
	require.NoError(t, err)
	require.Equal(t, ":9000", settings.listenAddr)
	require.Equal(t, "/qqbot", settings.webhookPath)

	values["STARRY_API_BOT_SECRET"] = strings.Repeat("x", 31)
	_, err = readConfig(lookup)
	require.ErrorContains(t, err, "at least 32 characters")
	values["STARRY_API_BOT_SECRET"] = strings.Repeat("x", 32)

	values["QQ_BOT_WEBHOOK_PATH"] = "relative"
	_, err = readConfig(lookup)
	require.Error(t, err)

	delete(values, "QQ_BOT_WEBHOOK_PATH")
	values["QQ_BOT_TLS_CERT_FILE"] = "cert.pem"
	_, err = readConfig(lookup)
	require.Error(t, err)
}

func TestNewAPIInviteClientClaimsWithSignedQQOpenIDRequest(t *testing.T) {
	const sharedSecret = "test-bot-secret-with-at-least-32-characters"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, registrationInvitePath, request.URL.Path)
		var payload struct {
			OpenID string `json:"open_id"`
		}
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(body, &payload))
		require.Equal(t, "qq-open-id", payload.OpenID)
		timestamp := request.Header.Get("X-QQ-Bot-Timestamp")
		nonce := request.Header.Get("X-QQ-Bot-Nonce")
		requestSignature := request.Header.Get("X-QQ-Bot-Signature")
		require.NotEmpty(t, timestamp)
		require.Len(t, nonce, 32)
		_, err = hex.DecodeString(nonce)
		require.NoError(t, err)
		require.Equal(t, strings.ToLower(nonce), nonce)
		require.Equal(t, signNewAPIInviteRequest(sharedSecret, http.MethodPost, registrationInvitePath, timestamp, nonce, body), requestSignature)
		writer.Header().Set("Content-Type", "application/json")
		_, err = io.WriteString(writer, `{"success":true,"data":{"code":"SAPI-TEST-CODE"}}`)
		require.NoError(t, err)
	}))
	defer server.Close()

	client, err := newNewAPIInviteClient(server.URL, sharedSecret)
	require.NoError(t, err)
	code, err := client.Claim(t.Context(), "qq-open-id")
	require.NoError(t, err)
	require.Equal(t, "SAPI-TEST-CODE", code)
}

func TestNewAPIInviteSignatureMatchesThePHPProtocolVector(t *testing.T) {
	actual := signNewAPIInviteRequest(
		"test-signature-secret",
		http.MethodPost,
		registrationInvitePath,
		"1700000000",
		"000102030405060708090a0b0c0d0e0f",
		[]byte(`{"open_id":"qq-user"}`),
	)
	require.Equal(t, "377b32cc7e1f64e0843d4fff71fdce4f7a8256e4c2557be7e652c30e8c3b9a88", actual)
}

func TestNewAPIInviteClientRejectsInsecureRemoteURLs(t *testing.T) {
	_, err := newNewAPIInviteClient("http://api.example.test", strings.Repeat("s", 32))
	require.Error(t, err)

	client, err := newNewAPIInviteClient("http://127.0.0.1:8080", strings.Repeat("s", 32))
	require.NoError(t, err)
	require.NotNil(t, client)
}

func TestNewWebhookHandlerRejectsInvalidSignaturesAndOversizedBodies(t *testing.T) {
	credentials := &token.QQBotCredentials{AppID: "bot-app", AppSecret: "bot-secret"}
	handler := newWebhookHandler(credentials, "/qqbot")

	invalidSignature := httptest.NewRequest(http.MethodPost, "/qqbot", strings.NewReader("{}"))
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalidSignature)
	require.Equal(t, http.StatusUnauthorized, invalidResponse.Code)

	tooLarge := httptest.NewRequest(http.MethodPost, "/qqbot", strings.NewReader(strings.Repeat("x", maxWebhookBodyBytes+1)))
	tooLarge.Header.Set(signature.HeaderTimestamp, "1")
	tooLarge.Header.Set(signature.HeaderSig, strings.Repeat("0", 128))
	tooLargeResponse := httptest.NewRecorder()
	handler.ServeHTTP(tooLargeResponse, tooLarge)
	require.Equal(t, http.StatusRequestEntityTooLarge, tooLargeResponse.Code)
}

func TestNewWebhookHandlerHealthCheck(t *testing.T) {
	handler := newWebhookHandler(&token.QQBotCredentials{AppSecret: "unused"}, "/qqbot")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	require.Equal(t, http.StatusNoContent, response.Code)
}

func TestNewWebhookHandlerAcknowledgesSignedHeartbeat(t *testing.T) {
	credentials := &token.QQBotCredentials{AppID: "bot-app", AppSecret: "bot-secret"}
	handler := newWebhookHandler(credentials, "/qqbot")
	body := `{"op":1,"d":3}`
	request := httptest.NewRequest(http.MethodPost, "/qqbot", strings.NewReader(body))
	request.Header.Set(signature.HeaderTimestamp, strings.TrimSpace(time.Now().Format("20060102150405")))
	signatureValue, err := signature.Generate(credentials.AppSecret, request.Header, []byte(body))
	require.NoError(t, err)
	request.Header.Set(signature.HeaderSig, signatureValue)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"op":11,"d":3}`, response.Body.String())
}

func TestNewWebhookHandlerRespondsToSignedCallbackValidation(t *testing.T) {
	credentials := &token.QQBotCredentials{AppID: "bot-app", AppSecret: "bot-secret"}
	handler := newWebhookHandler(credentials, "/qqbot")
	challenge := `{"op":13,"d":{"plain_token":"qq-validation-token","event_ts":"20261018120000"}}`
	request := httptest.NewRequest(http.MethodPost, "/qqbot", strings.NewReader(challenge))
	request.Header.Set(signature.HeaderTimestamp, strings.TrimSpace(time.Now().Format("20060102150405")))
	signatureValue, err := signature.Generate(credentials.AppSecret, request.Header, []byte(challenge))
	require.NoError(t, err)
	request.Header.Set(signature.HeaderSig, signatureValue)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	var validationResponse dto.WHValidationRsp
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &validationResponse))
	require.Equal(t, "qq-validation-token", validationResponse.PlainToken)
	require.NotEmpty(t, validationResponse.Signature)

	callbackHeaders := make(http.Header)
	callbackHeaders.Set(signature.HeaderTimestamp, "20261018120000")
	callbackHeaders.Set(signature.HeaderSig, validationResponse.Signature)
	valid, err := signature.Verify(credentials.AppSecret, callbackHeaders, []byte("qq-validation-token"))
	require.NoError(t, err)
	require.True(t, valid)
}

func TestNewWebhookHandlerDispatchesSignedC2CInviteCommand(t *testing.T) {
	credentials := &token.QQBotCredentials{AppID: "bot-app", AppSecret: "bot-secret"}
	sender := &testMessageSender{}
	claimer := &testInviteClaimer{code: "SAPI-WEBHOOK-CODE"}
	event.RegisterHandlers(NewBot(sender, claimer).C2CMessageHandler())
	handler := newWebhookHandler(credentials, "/qqbot")
	body := `{"op":0,"s":1,"t":"C2C_MESSAGE_CREATE","id":"event-id","d":{"id":"message-id","content":"/sapi 邀请码","author":{"id":"qq-open-id"}}}`
	request := httptest.NewRequest(http.MethodPost, "/qqbot", strings.NewReader(body))
	request.Header.Set(signature.HeaderTimestamp, strings.TrimSpace(time.Now().Format("20060102150405")))
	signatureValue, err := signature.Generate(credentials.AppSecret, request.Header, []byte(body))
	require.NoError(t, err)
	request.Header.Set(signature.HeaderSig, signatureValue)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"op":12,"d":0}`, response.Body.String())
	require.Equal(t, "qq-open-id", claimer.openID)
	require.Equal(t, 1, claimer.calls)
	require.Equal(t, "message-id", sender.messageID)
	require.Contains(t, sender.content, "SAPI-WEBHOOK-CODE")
}

func TestNewWebhookHandlerDoesNotClaimInviteFromGroupMessage(t *testing.T) {
	credentials := &token.QQBotCredentials{AppID: "bot-app", AppSecret: "bot-secret"}
	sender := &testMessageSender{}
	claimer := &testInviteClaimer{code: "SAPI-GROUP-CODE"}
	event.RegisterHandlers(NewBot(sender, claimer).C2CMessageHandler())
	handler := newWebhookHandler(credentials, "/qqbot")
	body := `{"op":0,"s":1,"t":"GROUP_AT_MESSAGE_CREATE","id":"group-event-id","d":{"id":"group-message-id","content":"/sapi 邀请码","group_id":"group-id","author":{"id":"qq-open-id"}}}`
	request := httptest.NewRequest(http.MethodPost, "/qqbot", strings.NewReader(body))
	request.Header.Set(signature.HeaderTimestamp, strings.TrimSpace(time.Now().Format("20060102150405")))
	signatureValue, err := signature.Generate(credentials.AppSecret, request.Header, []byte(body))
	require.NoError(t, err)
	request.Header.Set(signature.HeaderSig, signatureValue)
	response := httptest.NewRecorder()

	handler.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Equal(t, 0, claimer.calls)
	require.Empty(t, sender.content)
}
