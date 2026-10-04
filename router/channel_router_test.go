package router

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestChannelDefaultBaseURLsRequireReadPermission(t *testing.T) {
	assertChannelRoutePermission(t, http.MethodGet, "/default_base_urls", authz.ChannelRead, controller.GetChannelDefaultBaseURLs)

	gin.SetMode(gin.TestMode)
	engine := gin.New()
	registerChannelRoutes(engine.Group("/api"))
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/channel/default_base_urls", nil))
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestChannelStatusRoutesUseExpectedPermissions(t *testing.T) {
	assertChannelRoutePermission(t, http.MethodGet, "/:id/vllm/status", authz.ChannelRead, controller.GetVLLMChannelStatus)
	assertChannelRoutePermission(t, http.MethodGet, "/:id/sglang/status", authz.ChannelRead, controller.GetSGLangChannelStatus)
	assertChannelRoutePermission(t, http.MethodPost, "/:id/status", authz.ChannelOperate, controller.UpdateChannelStatus)
	assertChannelRoutePermission(t, http.MethodPost, "/status/batch", authz.ChannelOperate, controller.BatchUpdateChannelStatus)
	assertChannelRoutePermission(t, http.MethodPut, "/", authz.ChannelWrite, controller.UpdateChannel)
}

func TestChannelDeleteRoutesUseSensitiveWritePermission(t *testing.T) {
	assertChannelRoutePermission(t, http.MethodDelete, "/:id", authz.ChannelSensitiveWrite, controller.DeleteChannel)
	assertChannelRoutePermission(t, http.MethodPost, "/batch", authz.ChannelSensitiveWrite, controller.DeleteChannelBatch)
	assertChannelRoutePermission(t, http.MethodDelete, "/disabled", authz.ChannelSensitiveWrite, controller.DeleteDisabledChannel)
	assertChannelRoutePermission(t, http.MethodPut, "/", authz.ChannelWrite, controller.UpdateChannel)
	assertChannelRoutePermission(t, http.MethodPut, "/tag", authz.ChannelWrite, controller.EditTagChannels)
	assertChannelRoutePermission(t, http.MethodPost, "/batch/tag", authz.ChannelWrite, controller.BatchSetChannelTag)
}

func TestChannelStatusRoutesRegisterWithoutConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api")

	require.NotPanics(t, func() {
		registerChannelStatusRoutes(api)
		registerChannelRoutes(api)
	})
}

func TestChannelStatusAPIEnforcesUserAndRootAccess(t *testing.T) {
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousRedis, previousMaster := common.RedisEnabled, common.IsMasterNode
	previousSecret := common.SessionSecret
	previousOptions := common.OptionMap
	previousType := common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.Channel{}, &model.Option{}, &model.SystemTask{}, &model.AuditLog{}, &model.CasbinRule{}, &model.AuthzRole{}))
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled, common.IsMasterNode = false, true
	common.SessionSecret = "channel-status-route-test-secret"
	common.OptionMap = map[string]string{}
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	require.NoError(t, authz.Init(db))
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.RedisEnabled, common.IsMasterNode = previousRedis, previousMaster
		common.SessionSecret = previousSecret
		common.OptionMap = previousOptions
		common.SetMainDatabaseType(previousType)
		require.NoError(t, sqlDB.Close())
	})
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	api := engine.Group("/api")
	registerChannelStatusRoutes(api)
	registerChannelRoutes(api)
	for _, role := range []int{0, common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		t.Run(fmt.Sprintf("role_%d", role), func(t *testing.T) {
			accessToken := ""
			if role != 0 {
				user := model.User{Username: fmt.Sprintf("status-role-%d", role), Role: role, Status: common.UserStatusEnabled, Group: "default", AuthVersion: 1, AffCode: fmt.Sprintf("status-aff-%d", role)}
				require.NoError(t, db.Create(&user).Error)
				session := model.UserSession{SID: fmt.Sprintf("status-session-%d", role), UserID: user.Id, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: "test-refresh-hash", LoginMethod: "password", LastActiveAt: time.Now().Unix(), ExpiresAt: time.Now().Add(time.Hour).Unix()}
				require.NoError(t, model.CreateUserSession(&session))
				accessToken, _, err = service.IssueAccessToken(service.AuthIdentity{UserID: user.Id, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1})
				require.NoError(t, err)
			}
			for _, endpoint := range []struct {
				method     string
				path       string
				body       string
				rootStatus int
			}{
				{http.MethodGet, "/api/channel/status/", "", http.StatusOK},
				{http.MethodGet, "/api/channel/status/probes/", "", http.StatusOK},
				{http.MethodPut, "/api/channel/status/probes/", `{"enabled":false,"probes":[]}`, http.StatusOK},
				{http.MethodPost, "/api/channel/status/probes/run/", "", http.StatusBadRequest},
				{http.MethodPost, "/api/channel/status/probes/unknown/run/", "", http.StatusBadRequest},
			} {
				t.Run(endpoint.method+endpoint.path, func(t *testing.T) {
					request := httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(endpoint.body))
					request.Header.Set("Content-Type", "application/json")
					if accessToken != "" {
						request.Header.Set("Authorization", "Bearer "+accessToken)
					}
					response := httptest.NewRecorder()
					engine.ServeHTTP(response, request)
					want := endpoint.rootStatus
					if role == 0 {
						want = http.StatusUnauthorized
					} else if role < common.RoleRootUser && endpoint.path != "/api/channel/status/" {
						want = http.StatusForbidden
					}
					assert.Equal(t, want, response.Code, response.Body.String())
				})
			}
		})
	}
}

func assertChannelRoutePermission(t *testing.T, method string, path string, permission authz.Permission, handler any) {
	t.Helper()
	for _, route := range channelPermissionRoutes {
		if route.method == method && route.path == path {
			assert.Equal(t, permission, route.permission)
			assert.Equal(t, reflect.ValueOf(handler).Pointer(), reflect.ValueOf(route.handler).Pointer())
			return
		}
	}
	t.Fatalf("route %s %s not found", method, path)
}
