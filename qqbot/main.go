package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/tencent-connect/botgo"
	"github.com/tencent-connect/botgo/dto"
	"github.com/tencent-connect/botgo/event"
	"github.com/tencent-connect/botgo/interaction/signature"
	"github.com/tencent-connect/botgo/interaction/webhook"
	"github.com/tencent-connect/botgo/openapi"
	"github.com/tencent-connect/botgo/token"
)

const maxWebhookBodyBytes = 1 << 20

func main() {
	botgo.SetLogger(discardSDKLogger{})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("QQ bot stopped: %v", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	settings, err := readConfig(os.LookupEnv)
	if err != nil {
		return err
	}
	inviteClient, err := newNewAPIInviteClient(settings.apiBaseURL, settings.apiSecret)
	if err != nil {
		return err
	}
	credentials := &token.QQBotCredentials{AppID: settings.appID, AppSecret: settings.appSecret}
	tokenSource := token.NewQQBotTokenSource(credentials)
	if err := token.StartRefreshAccessToken(ctx, tokenSource); err != nil {
		return errors.New("QQ platform authentication failed")
	}
	api := botgo.NewOpenAPI(credentials.AppID, tokenSource).WithTimeout(8 * time.Second).SetDebug(false)
	bot := NewBot(qqC2CSender{api: api}, inviteClient)
	event.RegisterHandlers(bot.C2CMessageHandler())
	server := &http.Server{
		Addr:              settings.listenAddr,
		Handler:           newWebhookHandler(credentials, settings.webhookPath),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      12 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	serverResult := make(chan error, 1)
	go func() {
		if settings.tlsCertFile != "" {
			serverResult <- server.ListenAndServeTLS(settings.tlsCertFile, settings.tlsKeyFile)
			return
		}
		serverResult <- server.ListenAndServe()
	}()
	scheme := "http"
	if settings.tlsCertFile != "" {
		scheme = "https"
	}
	log.Printf("StarryAPI QQ bot webhook listening on %s://%s%s", scheme, settings.listenAddr, settings.webhookPath)
	select {
	case err := <-serverResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return errors.New("QQ webhook server stopped unexpectedly")
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return errors.New("QQ webhook server shutdown failed")
		}
		return nil
	}
}

func readConfig(lookup func(string) (string, bool)) (config, error) {
	value := func(key string) string {
		value, _ := lookup(key)
		return strings.TrimSpace(value)
	}

	settings := config{
		appID:       value("QQ_BOT_APP_ID"),
		appSecret:   value("QQ_BOT_APP_SECRET"),
		apiBaseURL:  value("NEW_API_URL"),
		apiSecret:   value("STARRY_API_BOT_SECRET"),
		listenAddr:  value("QQ_BOT_LISTEN_ADDR"),
		webhookPath: value("QQ_BOT_WEBHOOK_PATH"),
		tlsCertFile: value("QQ_BOT_TLS_CERT_FILE"),
		tlsKeyFile:  value("QQ_BOT_TLS_KEY_FILE"),
	}
	if settings.listenAddr == "" {
		settings.listenAddr = ":9000"
	}
	if settings.webhookPath == "" {
		settings.webhookPath = "/qqbot"
	}
	if settings.appID == "" || settings.appSecret == "" {
		return config{}, errors.New("QQ_BOT_APP_ID and QQ_BOT_APP_SECRET are required")
	}
	if settings.apiBaseURL == "" || settings.apiSecret == "" {
		return config{}, errors.New("NEW_API_URL and STARRY_API_BOT_SECRET are required")
	}
	if len(settings.apiSecret) < 32 {
		return config{}, errors.New("STARRY_API_BOT_SECRET must be at least 32 characters")
	}
	if !strings.HasPrefix(settings.webhookPath, "/") || strings.ContainsAny(settings.webhookPath, "..?#") {
		return config{}, errors.New("QQ_BOT_WEBHOOK_PATH must be a safe absolute URL path")
	}
	if (settings.tlsCertFile == "") != (settings.tlsKeyFile == "") {
		return config{}, errors.New("QQ_BOT_TLS_CERT_FILE and QQ_BOT_TLS_KEY_FILE must be configured together")
	}
	return settings, nil
}

func newWebhookHandler(credentials *token.QQBotCredentials, webhookPath string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST "+webhookPath, func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxWebhookBodyBytes))
		if err != nil {
			status := http.StatusBadRequest
			var maxBytesError *http.MaxBytesError
			if errors.As(err, &maxBytesError) {
				status = http.StatusRequestEntityTooLarge
			}
			http.Error(writer, "invalid callback body", status)
			return
		}
		if len(body) == 0 {
			http.Error(writer, "invalid callback body", http.StatusBadRequest)
			return
		}
		valid, err := signature.Verify(credentials.AppSecret, request.Header, body)
		if err != nil || !valid {
			http.Error(writer, "invalid callback signature", http.StatusUnauthorized)
			return
		}
		var payload callbackPayload
		if err := json.Unmarshal(body, &payload); err != nil {
			http.Error(writer, "invalid callback payload", http.StatusBadRequest)
			return
		}
		var response []byte
		switch payload.OPCode {
		case dto.HTTPCallbackValidation:
			var challenge dto.WHValidationReq
			if err := json.Unmarshal(payload.Data, &challenge); err != nil || challenge.PlainToken == "" || challenge.EventTs == "" {
				http.Error(writer, "invalid callback challenge", http.StatusBadRequest)
				return
			}
			response = webhook.GenValidationACK(&challenge, request.Header, credentials.AppSecret)
			if len(response) == 0 {
				http.Error(writer, "could not sign callback challenge", http.StatusInternalServerError)
				return
			}
		case dto.WSHeartbeat:
			var sequence uint32
			if err := json.Unmarshal(payload.Data, &sequence); err != nil {
				http.Error(writer, "invalid heartbeat payload", http.StatusBadRequest)
				return
			}
			response = []byte(webhook.GenHeartbeatACK(sequence))
		case dto.WSDispatchEvent:
			eventPayload := &dto.WSPayload{
				WSPayloadBase: dto.WSPayloadBase{
					OPCode:  payload.OPCode,
					Seq:     payload.Seq,
					Type:    payload.Type,
					EventID: payload.EventID,
				},
				RawMessage: body,
				Session:    &dto.Session{AppID: credentials.AppID},
			}
			response = []byte(webhook.GenDispatchACK(event.ParseAndHandle(eventPayload) == nil))
		default:
			http.Error(writer, "unsupported callback operation", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(response)
	})
	return mux
}

type callbackPayload struct {
	dto.WSPayloadBase
	Data json.RawMessage `json:"d"`
}

type qqC2CSender struct {
	api openapi.OpenAPI
}

func (sender qqC2CSender) SendReply(ctx context.Context, openID, messageID, content string) error {
	_, err := sender.api.PostC2CMessage(ctx, openID, dto.MessageToCreate{
		Content:   content,
		Timestamp: time.Now().UnixMilli(),
		MsgID:     messageID,
		MessageReference: &dto.MessageReference{
			MessageID:             messageID,
			IgnoreGetMessageError: true,
		},
	})
	return err
}

type discardSDKLogger struct{}

func (discardSDKLogger) Debug(...any)          {}
func (discardSDKLogger) Info(...any)           {}
func (discardSDKLogger) Warn(...any)           {}
func (discardSDKLogger) Error(...any)          {}
func (discardSDKLogger) Debugf(string, ...any) {}
func (discardSDKLogger) Infof(string, ...any)  {}
func (discardSDKLogger) Warnf(string, ...any)  {}
func (discardSDKLogger) Errorf(string, ...any) {}
func (discardSDKLogger) Sync() error           { return nil }
