package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const registrationInvitePath = "/api/bot/registration-invite"

type config struct {
	appID       string
	appSecret   string
	apiBaseURL  string
	apiSecret   string
	listenAddr  string
	webhookPath string
	tlsCertFile string
	tlsKeyFile  string
}

type newAPIInviteClient struct {
	baseURL string
	secret  string
	client  *http.Client
}

func newNewAPIInviteClient(baseURL, secret string) (*newAPIInviteClient, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	secret = strings.TrimSpace(secret)
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("invalid NewAPI base URL")
	}
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && isLoopbackHost(parsed.Hostname())) {
		return nil, errors.New("NewAPI base URL must use HTTPS, except for loopback development")
	}
	if secret == "" {
		return nil, errors.New("NewAPI bot secret is required")
	}
	return &newAPIInviteClient{
		baseURL: baseURL,
		secret:  secret,
		client:  &http.Client{Timeout: 8 * time.Second},
	}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (client *newAPIInviteClient) Claim(ctx context.Context, openID string) (string, error) {
	payload, err := json.Marshal(struct {
		OpenID string `json:"open_id"`
	}{OpenID: openID})
	if err != nil {
		return "", errors.New("could not create invite request")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.baseURL+registrationInvitePath, bytes.NewReader(payload))
	if err != nil {
		return "", errors.New("could not create invite request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-QQ-Bot-Secret", client.secret)
	response, err := client.client.Do(request)
	if err != nil {
		return "", errors.New("invite service request failed")
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("invite service returned HTTP %d", response.StatusCode)
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&result); err != nil {
		return "", errors.New("invite service returned invalid data")
	}
	if !result.Success || result.Data.Code == "" || len(result.Data.Code) > 128 {
		return "", errors.New("invite service did not issue an invite code")
	}
	return result.Data.Code, nil
}
