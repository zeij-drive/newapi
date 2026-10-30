package controller

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

func isRegistrationInviteError(err error) bool {
	return errors.Is(err, model.ErrRegistrationInviteRequired) ||
		errors.Is(err, model.ErrRegistrationInviteInvalid) ||
		errors.Is(err, model.ErrRegistrationInviteExpired) ||
		errors.Is(err, model.ErrRegistrationInviteUsed) ||
		errors.Is(err, model.ErrRegistrationInviteDisabled) ||
		errors.Is(err, model.ErrRegistrationInviteNotConfigured)
}

func writeRegistrationInviteError(c *gin.Context, err error) {
	if errors.Is(err, model.ErrRegistrationInviteNotConfigured) {
		common.ApiError(c, err)
		return
	}
	key := "user.registration_invite_invalid"
	switch {
	case errors.Is(err, model.ErrRegistrationInviteRequired):
		key = "user.registration_invite_required"
	case errors.Is(err, model.ErrRegistrationInviteExpired):
		key = "user.registration_invite_expired"
	case errors.Is(err, model.ErrRegistrationInviteUsed):
		key = "user.registration_invite_used"
	case errors.Is(err, model.ErrRegistrationInviteDisabled):
		key = "user.registration_invite_disabled"
	}
	common.ApiErrorI18n(c, key)
}

type registrationInviteCreateRequest struct {
	Count     int   `json:"count"`
	ExpiresAt int64 `json:"expires_at"`
}

func CreateRegistrationInvites(c *gin.Context) {
	var request registrationInviteCreateRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if request.Count == 0 {
		request.Count = 1
	}
	if request.Count < 1 || request.Count > 1000 || request.ExpiresAt < 0 {
		common.ApiErrorMsg(c, "邀请码数量或过期时间无效")
		return
	}
	codes, err := model.CreateRegistrationInvites(request.Count, c.GetInt("id"), request.ExpiresAt)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "registration_invite.create", map[string]any{"count": len(codes), "expires_at": request.ExpiresAt})
	common.ApiSuccess(c, codes)
}

func ListRegistrationInvites(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("p"))
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	invites, total, err := model.ListRegistrationInvites(page, pageSize)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo := common.GetPageQuery(c)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(invites)
	common.ApiSuccess(c, pageInfo)
}

func UpdateRegistrationInvite(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的邀请码 ID")
		return
	}
	var request struct {
		Status int `json:"status"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if err := model.SetRegistrationInviteStatus(id, request.Status); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "registration_invite.status_update", map[string]any{"id": id, "status": request.Status})
	common.ApiSuccess(c, nil)
}

type qqBotInviteRequest struct {
	OpenID string `json:"open_id"`
	Email  string `json:"email"`
}

const (
	qqBotTimestampHeader       = "X-QQ-Bot-Timestamp"
	qqBotNonceHeader           = "X-QQ-Bot-Nonce"
	qqBotSignatureHeader       = "X-QQ-Bot-Signature"
	qqBotRequestPath           = "/api/bot/registration-invite"
	qqBotTimestampSkew         = 5 * time.Minute
	qqBotNonceTTL              = qqBotTimestampSkew * 2
	qqBotNonceCacheLimit       = 8192
	qqBotInviteEmailCacheLimit = 8192
)

const qqBotTimestampSkewSeconds int64 = int64(qqBotTimestampSkew / time.Second)

const qqBotInviteEmailCooldown = time.Minute

var (
	errQQBotRequestInvalid    = errors.New("invalid bot request")
	errQQBotReplay            = errors.New("duplicate bot request")
	errQQBotReplayUnavailable = errors.New("bot replay protection unavailable")
)

var qqBotNonceCache = struct {
	sync.Mutex
	values map[string]time.Time
}{values: make(map[string]time.Time)}

var qqBotInviteEmailCache = struct {
	sync.Mutex
	values map[string]qqBotInviteEmailReservation
}{values: make(map[string]qqBotInviteEmailReservation)}

type qqBotInviteEmailReservation struct {
	nonce     string
	expiresAt time.Time
}

var (
	errQQBotInviteEmailRateLimited      = errors.New("QQ bot invite email rate limit exceeded")
	errQQBotInviteEmailLimitUnavailable = errors.New("QQ bot invite email rate limit unavailable")
)

var qqBotInviteEmailSender = sendQQBotRegistrationInviteEmail

func sendQQBotRegistrationInviteEmail(recipient, code string) error {
	subject := fmt.Sprintf("%s 注册邀请码", common.SystemName)
	content := fmt.Sprintf("<p>您好，您的 %s 注册邀请码为：</p><p><strong>%s</strong></p><p>请勿将此邀请码转发给不信任的人。</p>", html.EscapeString(common.SystemName), html.EscapeString(code))
	return common.SendEmail(subject, recipient, content)
}

func qqBotSignature(secret, method, path, timestamp, nonce string, body []byte) []byte {
	bodyDigest := sha256.Sum256(body)
	canonical := fmt.Sprintf("%s\n%s\n%s\n%s\n%s", method, path, timestamp, nonce, hex.EncodeToString(bodyDigest[:]))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(canonical))
	return mac.Sum(nil)
}

func validateQQBotRequest(secret string, c *gin.Context, body []byte) error {
	if len(secret) < 32 || c.Request.Method != http.MethodPost || c.Request.URL.Path != qqBotRequestPath {
		return errQQBotRequestInvalid
	}
	timestamp := strings.TrimSpace(c.GetHeader(qqBotTimestampHeader))
	nonce := strings.TrimSpace(c.GetHeader(qqBotNonceHeader))
	signature := strings.TrimSpace(c.GetHeader(qqBotSignatureHeader))
	if len(timestamp) < 1 || len(timestamp) > 12 || len(nonce) != 32 || len(signature) != sha256.Size*2 {
		return errQQBotRequestInvalid
	}
	issuedAt, err := strconv.ParseInt(timestamp, 10, 64)
	now := time.Now().Unix()
	if err != nil || issuedAt < 1 || strconv.FormatInt(issuedAt, 10) != timestamp || issuedAt < now-qqBotTimestampSkewSeconds || issuedAt > now+qqBotTimestampSkewSeconds {
		return errQQBotRequestInvalid
	}
	if nonce != strings.ToLower(nonce) {
		return errQQBotRequestInvalid
	}
	if _, err := hex.DecodeString(nonce); err != nil {
		return errQQBotRequestInvalid
	}
	provided, err := hex.DecodeString(signature)
	if err != nil || len(provided) != sha256.Size {
		return errQQBotRequestInvalid
	}
	expected := qqBotSignature(secret, c.Request.Method, qqBotRequestPath, timestamp, nonce, body)
	if !hmac.Equal(provided, expected) {
		return errQQBotRequestInvalid
	}
	return reserveQQBotNonce(nonce)
}

func reserveQQBotNonce(nonce string) error {
	if common.RedisEnabled {
		if common.RDB == nil {
			return errQQBotReplayUnavailable
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		ok, err := common.RDB.SetNX(ctx, "newapi:qqbot:nonce:"+nonce, "1", qqBotNonceTTL).Result()
		if err != nil {
			return errQQBotReplayUnavailable
		}
		if !ok {
			return errQQBotReplay
		}
		return nil
	}

	now := time.Now()
	qqBotNonceCache.Lock()
	defer qqBotNonceCache.Unlock()
	for value, expiresAt := range qqBotNonceCache.values {
		if !expiresAt.After(now) {
			delete(qqBotNonceCache.values, value)
		}
	}
	if _, exists := qqBotNonceCache.values[nonce]; exists {
		return errQQBotReplay
	}
	if len(qqBotNonceCache.values) >= qqBotNonceCacheLimit {
		return errQQBotReplayUnavailable
	}
	qqBotNonceCache.values[nonce] = now.Add(qqBotNonceTTL)
	return nil
}

func qqBotInviteEmailRateLimitKey(secret, openID string) string {
	hash := common.GenerateHMACWithKey([]byte("qqbot-registration-invite-email-v1:"+secret), strings.TrimSpace(openID))
	return "newapi:qqbot:invite-email:" + hash
}

func reserveQQBotInviteEmailSend(secret, openID, nonce string) error {
	openID = strings.TrimSpace(openID)
	if openID == "" || len(openID) > 128 || nonce == "" {
		return errQQBotRequestInvalid
	}
	key := qqBotInviteEmailRateLimitKey(secret, openID)
	if common.RedisEnabled {
		if common.RDB == nil {
			return errQQBotInviteEmailLimitUnavailable
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		reserved, err := common.RDB.SetNX(ctx, key, nonce, qqBotInviteEmailCooldown).Result()
		if err != nil {
			return errQQBotInviteEmailLimitUnavailable
		}
		if !reserved {
			return errQQBotInviteEmailRateLimited
		}
		return nil
	}

	now := time.Now()
	qqBotInviteEmailCache.Lock()
	defer qqBotInviteEmailCache.Unlock()
	for entryKey, entry := range qqBotInviteEmailCache.values {
		if !entry.expiresAt.After(now) {
			delete(qqBotInviteEmailCache.values, entryKey)
		}
	}
	if _, exists := qqBotInviteEmailCache.values[key]; exists {
		return errQQBotInviteEmailRateLimited
	}
	if len(qqBotInviteEmailCache.values) >= qqBotInviteEmailCacheLimit {
		return errQQBotInviteEmailLimitUnavailable
	}
	qqBotInviteEmailCache.values[key] = qqBotInviteEmailReservation{nonce: nonce, expiresAt: now.Add(qqBotInviteEmailCooldown)}
	return nil
}

func releaseQQBotInviteEmailSend(secret, openID, nonce string) {
	key := qqBotInviteEmailRateLimitKey(secret, openID)
	if common.RedisEnabled {
		if common.RDB == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		const compareAndDelete = `if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) else return 0 end`
		_, _ = common.RDB.Eval(ctx, compareAndDelete, []string{key}, nonce).Result()
		return
	}
	qqBotInviteEmailCache.Lock()
	defer qqBotInviteEmailCache.Unlock()
	if entry, exists := qqBotInviteEmailCache.values[key]; exists && entry.nonce == nonce {
		delete(qqBotInviteEmailCache.values, key)
	}
}

// ClaimRegistrationInviteForQQ is an internal endpoint for the standalone QQ
// bot. It requires a short-lived HMAC request signature and a single-use nonce;
// the OpenID is therefore bound to the authenticated request body.
func ClaimRegistrationInviteForQQ(c *gin.Context) {
	configured := strings.TrimSpace(common.GetEnvOrDefaultString("STARRY_API_BOT_SECRET", ""))
	if len(configured) < 32 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "未授权"})
		return
	}
	if c.Request.Body == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "无效的请求体"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"success": false, "message": "请求体过大"})
		return
	}
	if err := validateQQBotRequest(configured, c, body); err != nil {
		if errors.Is(err, errQQBotReplayUnavailable) {
			c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "服务暂不可用"})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "未授权"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	var request qqBotInviteRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	email, err := service.ValidateAccountEmail(request.Email)
	if err != nil {
		common.ApiErrorMsg(c, "邮箱格式无效或不符合站点策略")
		return
	}
	nonce := c.GetHeader(qqBotNonceHeader)
	if err := reserveQQBotInviteEmailSend(configured, request.OpenID, nonce); err != nil {
		if errors.Is(err, errQQBotInviteEmailRateLimited) {
			c.Header("Retry-After", strconv.Itoa(int(qqBotInviteEmailCooldown.Seconds())))
			c.JSON(http.StatusTooManyRequests, gin.H{"success": false, "message": "请求过于频繁，请稍后重试"})
			return
		}
		c.JSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "服务暂不可用"})
		return
	}
	code, err := model.ClaimRegistrationInviteForQQ(request.OpenID)
	if err != nil {
		releaseQQBotInviteEmailSend(configured, request.OpenID, nonce)
		if errors.Is(err, model.ErrRegistrationInviteInvalid) {
			common.ApiErrorMsg(c, "邀请码生成失败，请稍后重试")
			return
		}
		common.ApiError(c, err)
		return
	}
	if err := qqBotInviteEmailSender(email, code); err != nil {
		releaseQQBotInviteEmailSend(configured, request.OpenID, nonce)
		common.ApiErrorMsg(c, "邀请码邮件发送失败，请稍后重试")
		return
	}
	common.ApiSuccess(c, gin.H{"sent": true})
}
