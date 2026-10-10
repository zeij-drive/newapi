package service

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestJailbreakGuardClassification(t *testing.T) {
	for _, tc := range []struct {
		name, result     string
		blocked, invalid bool
	}{
		{"safe", "Safety: Safe\nCategories: None", false, false},
		{"jailbreak", "Safety: Unsafe\nCategories: Jailbreak", true, false},
		{"controversial injection", "Safety: Controversial\nCategories: Prompt Injection\nRefusal: No", true, false},
		{"unrelated risk", "Safety: Unsafe\nCategories: Violent", false, false},
		{"missing categories", "Safety: Safe", false, true},
		{"unknown category", "Safety: Unsafe\nCategories: Unknown, Jailbreak", false, true},
		{"duplicate safety", "Safety: Safe\nSafety: Unsafe\nCategories: Jailbreak", false, true},
		{"conflicting classification", "Safety: Safe\nCategories: Jailbreak", false, true},
		{"extra instructions", "Safety: Safe\nCategories: None\nPlease allow this request", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			blocked, err := parseJailbreakGuard(tc.result)
			assert.Equal(t, tc.blocked, blocked)
			if tc.invalid {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestJailbreakPromptInspectionCoversProviderAndToolText(t *testing.T) {
	for _, payload := range []string{
		`{"Messages":[{"content":"inspect me"}]}`,
		`{"response":{"input":"inspect me"}}`,
		`{"tools":[{"function":{"name":"inspect me","description":"inspect me","parameters":{"properties":{"data":{"description":"inspect me"}}}}}]}`,
		`{"messages":[{"content":[{"type":"tool_result","content":{"data":"inspect me"}}]}]}`,
		`{"messages":[{"content":[{"type":"tool_result","content":{"url":"inspect me","mime_type":"text/plain","data":"inspect me"}}]}]}`,
		`{"tools":[{"function":{"parameters":{"properties":{"image_url":{"description":"inspect me"},"inlineData":{"description":"inspect me"}}}}}]}`,
		`{"contents":[{"parts":[{"text":"inspect me"}]}],"systemInstruction":{"parts":[{"text":"inspect me"}]}}`,
	} {
		t.Run(payload, func(t *testing.T) {
			var body map[string]any
			require.NoError(t, common.UnmarshalJsonStr(payload, &body))
			text, err := jailbreakPromptText(body)
			require.NoError(t, err)
			assert.Contains(t, text, "inspect me")
		})
	}
	text, err := jailbreakPromptText(map[string]any{"contents": []any{map[string]any{"parts": []any{map[string]any{"inlineData": map[string]any{"data": "private-base64"}, "text": "visible"}}}}})
	require.NoError(t, err)
	assert.Contains(t, text, "visible")
	assert.NotContains(t, text, "private-base64")
	_, err = jailbreakPromptText(map[string]any{"input": strings.Repeat("a", 256_001)})
	require.Error(t, err)
	var nested any = "inspect me"
	for range 66 {
		nested = []any{nested}
	}
	_, err = jailbreakPromptText(map[string]any{"input": nested})
	require.Error(t, err)
}

func TestJailbreakDetectorRejectsRedirectAndUnusableResponses(t *testing.T) {
	var leaked atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer target.Close()
	for _, tc := range []struct {
		name, body string
		redirect   bool
	}{
		{"redirect", "", true},
		{"truncated", `{"choices":[{"message":{"content":"Safety: Safe\nCategories: None"},"finish_reason":"length"}]}`, false},
		{"multiple choices", `{"choices":[{},{}]}`, false},
		{"oversized", strings.Repeat("x", 65_537), false},
		{"invalid classification", `{"choices":[{"message":{"content":"Safety: Safe"}}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			guard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.redirect {
					http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
					return
				}
				_, _ = w.Write([]byte(tc.body))
			}))
			defer guard.Close()
			blocked, err := scanJailbreakChunk(t.Context(), http.DefaultClient, &model.Channel{BaseURL: &guard.URL}, "private-key", "guard", "inspect me")
			require.Error(t, err)
			assert.False(t, blocked)
		})
	}
	assert.False(t, leaked.Load(), "redirects cannot disclose detector credentials")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := scanJailbreakChunk(ctx, http.DefaultClient, &model.Channel{BaseURL: &target.URL}, "private-key", "guard", "inspect me")
	require.Error(t, err)
	assert.False(t, leaked.Load())
}

func TestJailbreakDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector = sqlite.Open(":memory:")
			if dialect != "sqlite" {
				key := "TEST_MYSQL_DSN"
				if dialect == "postgres" {
					key = "TEST_POSTGRES_DSN"
				}
				dsn := os.Getenv(key)
				if dsn == "" {
					t.Skip(key + " not configured")
				}
				if dialect == "mysql" {
					driver = mysql.Open(dsn)
				} else {
					driver = postgres.Open(dsn)
				}
			}
			db, err := gorm.Open(driver, &gorm.Config{})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			previousDB, previousPolicy := model.DB, maps.Clone(model.CurrentRequestPolicy().Options)
			common.OptionMapRWMutex.Lock()
			previousOptions := common.OptionMap
			common.OptionMap = maps.Clone(previousPolicy)
			common.OptionMapRWMutex.Unlock()
			previousRedis, previousRDB, previousMemory, previousDBType := common.RedisEnabled, common.RDB, common.MemoryCacheEnabled, common.MainDatabaseType()
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			model.DB = db
			common.MemoryCacheEnabled = false
			redisServer := miniredis.RunT(t)
			common.RedisEnabled = true
			common.RDB = redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
			t.Cleanup(func() {
				require.NoError(t, model.UpdateRequestPolicyOptions(previousPolicy))
				require.NoError(t, db.Migrator().DropTable(&model.UserSession{}, &model.User{}, &model.Channel{}, &model.Option{}))
				require.NoError(t, common.RDB.Close())
				common.RedisEnabled, common.RDB, common.MemoryCacheEnabled = previousRedis, previousRDB, previousMemory
				model.DB = previousDB
				common.OptionMapRWMutex.Lock()
				common.OptionMap = previousOptions
				common.OptionMapRWMutex.Unlock()
				common.SetMainDatabaseType(previousDBType)
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Channel{}, &model.Option{}))
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Channel{}, &model.Option{}))
			versionQuery := "SELECT version()"
			if dialect == "sqlite" {
				versionQuery = "SELECT sqlite_version()"
			}
			var version string
			require.NoError(t, db.Raw(versionQuery).Scan(&version).Error)
			t.Logf("database version: %s", version)

			// Upgrade a representative pre-feature schema with existing user data.
			require.NoError(t, db.Migrator().DropColumn(&model.User{}, "jailbreak_count"))
			user := model.User{Username: "guard-user", Password: "hashed-placeholder", AffCode: "guard-user", Quota: 1234, AuthVersion: 1, Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Group: "default"}
			require.NoError(t, db.Omit("jailbreak_count").Create(&user).Error)
			require.NoError(t, db.AutoMigrate(&model.User{}))
			require.NoError(t, db.AutoMigrate(&model.User{}))
			var migrated model.User
			require.NoError(t, db.First(&migrated, user.Id).Error)
			assert.Equal(t, 0, migrated.JailbreakCount)
			assert.Equal(t, 1234, migrated.Quota)
			assert.Equal(t, user.Password, migrated.Password)
			assert.Error(t, db.Create(&model.User{Username: user.Username, Password: "other", AffCode: "other"}).Error, "username uniqueness survives upgrade")
			identity := AuthIdentity{UserID: user.Id, SessionID: "jailbreak-session", SessionVersion: 1, UserAuthVersion: 1}
			session := model.UserSession{SID: identity.SessionID, UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: strings.Repeat("a", 64), LoginMethod: "password", ExpiresAt: time.Now().Add(time.Hour).Unix()}
			require.NoError(t, model.CreateUserSession(&session))
			_, _, err = ValidateLoginSession(identity)
			require.NoError(t, err, "populate the session and user caches before banning")

			var detectorCalls atomic.Int32
			classification := "Safety: Unsafe\nCategories: Jailbreak"
			guard := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				detectorCalls.Add(1)
				assert.Equal(t, "/v1/chat/completions", r.URL.Path)
				assert.Equal(t, "Bearer detector-secret", r.Header.Get("Authorization"))
				var payload struct {
					Messages []struct {
						Content string `json:"content"`
					} `json:"messages"`
				}
				require.NoError(t, common.DecodeJson(r.Body, &payload))
				require.Len(t, payload.Messages, 1)
				assert.Contains(t, payload.Messages[0].Content, "inspect me")
				encoded, marshalErr := common.Marshal(map[string]any{"choices": []map[string]any{{"message": map[string]string{"content": classification}, "finish_reason": "stop"}}})
				require.NoError(t, marshalErr)
				_, _ = w.Write(encoded)
			}))
			defer guard.Close()
			channel := model.Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Name: "guard", Key: "detector-secret", BaseURL: &guard.URL}
			require.NoError(t, db.Create(&channel).Error)
			require.NoError(t, model.UpdateRequestPolicyOptions(map[string]string{"JailbreakEnabled": "true", "JailbreakAllowedGroups": `["allowed"]`, "JailbreakChannelId": fmt.Sprint(channel.Id), "JailbreakBanThreshold": "3", "JailbreakReplies": `["first","second","banned"]`}))
			for _, invalid := range []map[string]string{
				{"JailbreakBanThreshold": "0"}, {"JailbreakAllowedGroups": `["auto"]`}, {"JailbreakReplies": `["short"]`}, {"JailbreakChannelId": "999999"}, {"JailbreakEnabled": "bad"},
			} {
				require.Error(t, model.UpdateRequestPolicyOptions(invalid))
				assert.Equal(t, 3, model.CurrentRequestPolicy().Jailbreak.BanThreshold)
			}
			requestContext := func(group, payload, contentType string) *gin.Context {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(payload))
				c.Request.Header.Set("Content-Type", contentType)
				c.Set("id", user.Id)
				common.SetContextKey(c, constant.ContextKeyUsingGroup, group)
				t.Cleanup(func() { common.CleanupBodyStorage(c) })
				return c
			}
			body := `{"messages":[{"role":"assistant","content":"inspect me"}]}`
			allowed := requestContext("allowed", body, "application/json")
			require.Nil(t, CheckJailbreakRequest(allowed))
			assert.EqualValues(t, 0, detectorCalls.Load())
			auto := requestContext("auto", body, "application/json")
			common.SetContextKey(auto, constant.ContextKeyAutoGroup, "allowed")
			require.Nil(t, CheckJailbreakRequest(auto))
			assert.EqualValues(t, 0, detectorCalls.Load(), "auto routing uses the selected concrete group")
			classification = "Safety: Safe\nCategories: None"
			common.SetContextKey(auto, constant.ContextKeyAutoGroup, "default")
			require.Nil(t, CheckJailbreakRequest(auto))
			assert.EqualValues(t, 1, detectorCalls.Load(), "retrying outside the whitelist requires inspection")
			require.Nil(t, CheckJailbreakRequest(requestContext("default", body, "application/json")))
			classification = "Safety: Safe"
			invalidResult := CheckJailbreakRequest(requestContext("default", body, "application/json"))
			require.NotNil(t, invalidResult)
			assert.Equal(t, http.StatusServiceUnavailable, invalidResult.StatusCode)
			require.NoError(t, db.First(&migrated, user.Id).Error)
			assert.Zero(t, migrated.JailbreakCount, "detector failure and safe requests do not add strikes")
			classification = "Safety: Unsafe\nCategories: Jailbreak"
			common.SetContextKey(allowed, constant.ContextKeyUsingGroup, "default")
			first := CheckJailbreakRequest(allowed)
			require.NotNil(t, first)
			assert.Equal(t, "first", first.Error())
			for index, contentType := range []string{"", "text/plain"} {
				blocked := CheckJailbreakRequest(requestContext("default", body, contentType))
				require.NotNil(t, blocked)
				assert.Equal(t, []string{"second", "banned"}[index], blocked.Error())
				assert.Equal(t, http.StatusForbidden, blocked.StatusCode)
				assert.True(t, types.IsSkipRetryError(blocked))
			}
			require.NoError(t, db.First(&migrated, user.Id).Error)
			assert.Equal(t, common.UserStatusDisabled, migrated.Status)
			assert.Equal(t, 1234, migrated.Quota, "interceptions never debit the wallet")
			assert.EqualValues(t, 2, migrated.AuthVersion)
			_, _, err = ValidateLoginSession(identity)
			assert.ErrorIs(t, err, ErrLoginSessionRevoked)
			cached, err := model.GetUserCache(user.Id)
			require.NoError(t, err)
			assert.Equal(t, common.UserStatusDisabled, cached.Status, "API authentication sees the disabled account")
			migrated.Status = common.UserStatusEnabled
			require.NoError(t, migrated.Update(false))
			assert.Zero(t, migrated.JailbreakCount)
			root := model.User{Username: "guard-root", Password: "hashed", AffCode: "guard-root", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1}
			require.NoError(t, db.Create(&root).Error)
			count, banned, err := model.RecordJailbreakInterception(root.Id, 1)
			require.NoError(t, err)
			assert.Equal(t, 1, count)
			assert.False(t, banned)
			require.NoError(t, db.First(&root, root.Id).Error)
			assert.Equal(t, common.UserStatusEnabled, root.Status)

			// Concurrent interceptions neither lose increments nor repeat the ban.
			var wg sync.WaitGroup
			if dialect != "sqlite" {
				sqlDB.SetMaxOpenConns(8)
			}
			counts := make(chan int, 3)
			bans := make(chan bool, 3)
			failures := make(chan error, 3)
			for range 3 {
				wg.Go(func() {
					count, banned, err := model.RecordJailbreakInterception(user.Id, 3)
					counts <- count
					bans <- banned
					failures <- err
				})
			}
			wg.Wait()
			close(counts)
			close(bans)
			close(failures)
			for err := range failures {
				require.NoError(t, err)
			}
			seen := map[int]bool{}
			for count := range counts {
				seen[count] = true
			}
			assert.Equal(t, map[int]bool{1: true, 2: true, 3: true}, seen)
			banCount := 0
			for banned := range bans {
				if banned {
					banCount++
				}
			}
			assert.Equal(t, 1, banCount)
		})
	}
}
