package controller

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const testQQBotSecret = "test-qq-bot-secret-with-at-least-32-characters"

func makeSignedQQBotInviteRequest(t *testing.T, secret, timestamp, nonce, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, qqBotRequestPath, bytes.NewBufferString(body))
	c.Request.Header.Set(qqBotTimestampHeader, timestamp)
	c.Request.Header.Set(qqBotNonceHeader, nonce)
	c.Request.Header.Set(qqBotSignatureHeader, fmt.Sprintf("%x", qqBotSignature(secret, http.MethodPost, qqBotRequestPath, timestamp, nonce, []byte(body))))
	return c, recorder
}

func setupQQBotInviteControllerTest(t *testing.T) {
	t.Helper()
	previousDB, previousRedis, previousRDB := model.DB, common.RedisEnabled, common.RDB
	previousConfigSecret := common.ConfigEncryptionSecret
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.RegistrationInvite{}))
	model.DB = db
	common.RedisEnabled = false
	common.ConfigEncryptionSecret = "qq-bot-controller-test-config-secret"
	qqBotNonceCache.Lock()
	qqBotNonceCache.values = make(map[string]time.Time)
	qqBotNonceCache.Unlock()
	qqBotInviteEmailCache.Lock()
	qqBotInviteEmailCache.values = make(map[string]qqBotInviteEmailReservation)
	qqBotInviteEmailCache.Unlock()
	t.Cleanup(func() {
		model.DB = previousDB
		common.RedisEnabled = previousRedis
		common.RDB = previousRDB
		common.ConfigEncryptionSecret = previousConfigSecret
	})
}

func withQQBotInviteEmailSender(t *testing.T, sender func(recipient, code string) error) {
	t.Helper()
	previous := qqBotInviteEmailSender
	qqBotInviteEmailSender = sender
	t.Cleanup(func() { qqBotInviteEmailSender = previous })
}

func TestClaimRegistrationInviteForQQRequiresSignedFreshSingleUseRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupQQBotInviteControllerTest(t)
	t.Setenv("STARRY_API_BOT_SECRET", testQQBotSecret)
	now := strconv.FormatInt(time.Now().Unix(), 10)
	body := `{"open_id":"qq-user-1","email":"user@example.com"}`
	nonce := "0123456789abcdef0123456789abcdef"
	withQQBotInviteEmailSender(t, func(string, string) error { return nil })

	context, recorder := makeSignedQQBotInviteRequest(t, testQQBotSecret, now, nonce, body)
	ClaimRegistrationInviteForQQ(context)
	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"sent":true`)
	assert.NotContains(t, recorder.Body.String(), "SAPI-")

	replayContext, replayRecorder := makeSignedQQBotInviteRequest(t, testQQBotSecret, now, nonce, body)
	ClaimRegistrationInviteForQQ(replayContext)
	assert.Equal(t, http.StatusUnauthorized, replayRecorder.Code)

	legacyContext, legacyRecorder := makeSignedQQBotInviteRequest(t, testQQBotSecret, now, "6123456789abcdef0123456789abcdef", body)
	legacyContext.Request.Header.Del(qqBotSignatureHeader)
	legacyContext.Request.Header.Set("X-QQ-Bot-Secret", testQQBotSecret)
	ClaimRegistrationInviteForQQ(legacyContext)
	assert.Equal(t, http.StatusUnauthorized, legacyRecorder.Code)

	alteredBodyContext, alteredBodyRecorder := makeSignedQQBotInviteRequest(t, testQQBotSecret, now, "1123456789abcdef0123456789abcdef", body)
	alteredBodyContext.Request.Body = io.NopCloser(bytes.NewBufferString(`{"open_id":"qq-user-2"}`))
	ClaimRegistrationInviteForQQ(alteredBodyContext)
	assert.Equal(t, http.StatusUnauthorized, alteredBodyRecorder.Code)

	expiredTimestamp := strconv.FormatInt(time.Now().Add(-qqBotTimestampSkew-time.Second).Unix(), 10)
	expiredContext, expiredRecorder := makeSignedQQBotInviteRequest(t, testQQBotSecret, expiredTimestamp, "2123456789abcdef0123456789abcdef", body)
	ClaimRegistrationInviteForQQ(expiredContext)
	assert.Equal(t, http.StatusUnauthorized, expiredRecorder.Code)

	common.RedisEnabled = true
	common.RDB = nil
	unavailableContext, unavailableRecorder := makeSignedQQBotInviteRequest(t, testQQBotSecret, now, "5123456789abcdef0123456789abcdef", body)
	ClaimRegistrationInviteForQQ(unavailableContext)
	assert.Equal(t, http.StatusServiceUnavailable, unavailableRecorder.Code)
}

func TestClaimRegistrationInviteForQQRejectsInvalidEmailBeforeClaim(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupQQBotInviteControllerTest(t)
	t.Setenv("STARRY_API_BOT_SECRET", testQQBotSecret)
	withQQBotInviteEmailSender(t, func(string, string) error {
		t.Fatal("invalid email must not trigger delivery")
		return nil
	})
	now := strconv.FormatInt(time.Now().Unix(), 10)
	body := `{"open_id":"qq-user-1","email":"not-an-email"}`
	context, recorder := makeSignedQQBotInviteRequest(t, testQQBotSecret, now, "6123456789abcdef0123456789abcdef", body)
	ClaimRegistrationInviteForQQ(context)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "邮箱格式无效")

	var count int64
	require.NoError(t, model.DB.Model(&model.RegistrationInvite{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestClaimRegistrationInviteForQQUsesConfiguredEmailPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupQQBotInviteControllerTest(t)
	t.Setenv("STARRY_API_BOT_SECRET", testQQBotSecret)
	previousRestrictionEnabled, previousWhitelist := common.EmailDomainRestrictionEnabled, common.EmailDomainWhitelist
	common.EmailDomainRestrictionEnabled = true
	common.EmailDomainWhitelist = []string{"allowed.example"}
	t.Cleanup(func() {
		common.EmailDomainRestrictionEnabled = previousRestrictionEnabled
		common.EmailDomainWhitelist = previousWhitelist
	})
	withQQBotInviteEmailSender(t, func(string, string) error {
		t.Fatal("email outside the configured domain policy must not trigger delivery")
		return nil
	})
	now := strconv.FormatInt(time.Now().Unix(), 10)
	body := `{"open_id":"qq-user-1","email":"user@blocked.example"}`
	context, recorder := makeSignedQQBotInviteRequest(t, testQQBotSecret, now, "9123456789abcdef0123456789abcdef", body)
	ClaimRegistrationInviteForQQ(context)
	assert.Contains(t, recorder.Body.String(), "邮箱格式无效或不符合站点策略")

	var count int64
	require.NoError(t, model.DB.Model(&model.RegistrationInvite{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestClaimRegistrationInviteForQQEmailDeliveryRetryDoesNotDiscloseCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupQQBotInviteControllerTest(t)
	t.Setenv("STARRY_API_BOT_SECRET", testQQBotSecret)
	var deliveredCodes []string
	withQQBotInviteEmailSender(t, func(_ string, code string) error {
		deliveredCodes = append(deliveredCodes, code)
		if len(deliveredCodes) == 1 {
			return errors.New("SMTP unavailable")
		}
		return nil
	})
	body := `{"open_id":"qq-user-1","email":"user@example.com"}`
	firstContext, firstRecorder := makeSignedQQBotInviteRequest(t, testQQBotSecret, strconv.FormatInt(time.Now().Unix(), 10), "7123456789abcdef0123456789abcdef", body)
	ClaimRegistrationInviteForQQ(firstContext)
	assert.Contains(t, firstRecorder.Body.String(), "邀请码邮件发送失败")
	assert.NotContains(t, firstRecorder.Body.String(), "SAPI-")

	secondContext, secondRecorder := makeSignedQQBotInviteRequest(t, testQQBotSecret, strconv.FormatInt(time.Now().Unix(), 10), "8123456789abcdef0123456789abcdef", body)
	ClaimRegistrationInviteForQQ(secondContext)
	assert.Contains(t, secondRecorder.Body.String(), `"sent":true`)
	assert.NotContains(t, secondRecorder.Body.String(), "SAPI-")
	require.Len(t, deliveredCodes, 2)
	assert.Equal(t, deliveredCodes[0], deliveredCodes[1])
}

func TestClaimRegistrationInviteForQQLimitsRepeatEmailsPerQQOpenID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupQQBotInviteControllerTest(t)
	t.Setenv("STARRY_API_BOT_SECRET", testQQBotSecret)
	var deliveries int
	withQQBotInviteEmailSender(t, func(string, string) error {
		deliveries++
		return nil
	})
	firstBody := `{"open_id":"qq-user-1","email":"first@example.com"}`
	firstContext, firstRecorder := makeSignedQQBotInviteRequest(t, testQQBotSecret, strconv.FormatInt(time.Now().Unix(), 10), "9123456789abcdef0123456789abcdef", firstBody)
	ClaimRegistrationInviteForQQ(firstContext)
	require.Equal(t, http.StatusOK, firstRecorder.Code)

	secondBody := `{"open_id":"qq-user-1","email":"second@example.com"}`
	secondContext, secondRecorder := makeSignedQQBotInviteRequest(t, testQQBotSecret, strconv.FormatInt(time.Now().Unix(), 10), "a123456789abcdef0123456789abcdef", secondBody)
	ClaimRegistrationInviteForQQ(secondContext)
	assert.Equal(t, http.StatusTooManyRequests, secondRecorder.Code)
	assert.Equal(t, "60", secondRecorder.Header().Get("Retry-After"))
	assert.NotContains(t, secondRecorder.Body.String(), "SAPI-")
	assert.Equal(t, 1, deliveries)
}

func TestQQBotNonceReservationIsAtomicAndFailsClosedWithRedis(t *testing.T) {
	previousRedis, previousRDB := common.RedisEnabled, common.RDB
	redisServer := miniredis.RunT(t)
	common.RDB = redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RDB = previousRDB
		common.RedisEnabled = previousRedis
	})

	require.NoError(t, reserveQQBotNonce("3123456789abcdef0123456789abcdef"))
	assert.ErrorIs(t, reserveQQBotNonce("3123456789abcdef0123456789abcdef"), errQQBotReplay)
	assert.Greater(t, redisServer.TTL("newapi:qqbot:nonce:3123456789abcdef0123456789abcdef"), time.Duration(0))

	redisServer.Close()
	assert.ErrorIs(t, reserveQQBotNonce("4123456789abcdef0123456789abcdef"), errQQBotReplayUnavailable)
}

func TestQQBotInviteEmailCooldownIsAtomicAndReleasableWithRedis(t *testing.T) {
	previousRedis, previousRDB := common.RedisEnabled, common.RDB
	redisServer := miniredis.RunT(t)
	common.RDB = redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	common.RedisEnabled = true
	t.Cleanup(func() {
		_ = common.RDB.Close()
		common.RDB = previousRDB
		common.RedisEnabled = previousRedis
	})

	key := qqBotInviteEmailRateLimitKey(testQQBotSecret, "qq-user-1")
	require.NoError(t, reserveQQBotInviteEmailSend(testQQBotSecret, "qq-user-1", "nonce-1"))
	assert.ErrorIs(t, reserveQQBotInviteEmailSend(testQQBotSecret, "qq-user-1", "nonce-2"), errQQBotInviteEmailRateLimited)
	releaseQQBotInviteEmailSend(testQQBotSecret, "qq-user-1", "nonce-1")
	require.NoError(t, reserveQQBotInviteEmailSend(testQQBotSecret, "qq-user-1", "nonce-3"))
	assert.Greater(t, redisServer.TTL(key), time.Duration(0))
}
