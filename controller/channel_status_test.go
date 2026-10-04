package controller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func channelStatusTestDB(t *testing.T, kind, dsn string) *gorm.DB {
	t.Helper()
	db, _ := newAuditTestDatabase(t, kind, dsn)
	previousDB, previousLogDB := model.DB, model.LOG_DB
	previousMain, previousLog := common.MainDatabaseType(), common.LogDatabaseType()
	previousMaster, previousRedis, previousMemory := common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled
	previousOptions := common.OptionMap
	previousModelRatio, previousModelPrice := ratio_setting.ModelRatio2JSONString(), ratio_setting.ModelPrice2JSONString()
	previousBillingConfig := config.GlobalConfig.ExportAllConfigs()
	model.DB, model.LOG_DB = db, db
	common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled = false, false, false
	common.OptionMap = map[string]string{}
	databaseType := common.DatabaseTypeSQLite
	if kind == "mysql" {
		databaseType = common.DatabaseTypeMySQL
	}
	if kind == "postgres" {
		databaseType = common.DatabaseTypePostgreSQL
	}
	common.SetDatabaseTypes(databaseType, databaseType)
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB())
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Option{}, &model.User{}, &model.SystemTask{}, &model.SystemTaskLock{}, &model.Log{}))
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Option{}, &model.User{}, &model.SystemTask{}, &model.SystemTaskLock{}, &model.Log{}))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o-mini":1}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{}`))
	config.UpdateConfigFromMap(config.GlobalConfig.Get("billing_setting"), map[string]string{"billing_mode": "{}", "billing_expr": "{}", "plugin_billing_expr": "{}"})
	var version string
	query := "SELECT version()"
	if kind == "sqlite" {
		query = "SELECT sqlite_version()"
	}
	require.NoError(t, db.Raw(query).Scan(&version).Error)
	t.Logf("database version: %s", version)
	t.Cleanup(func() {
		model.DB, model.LOG_DB = previousDB, previousLogDB
		common.SetDatabaseTypes(previousMain, previousLog)
		common.IsMasterNode, common.RedisEnabled, common.MemoryCacheEnabled = previousMaster, previousRedis, previousMemory
		if previousDB != nil {
			common.IsMasterNode = false
			require.NoError(t, model.InitLogDB())
			common.IsMasterNode = previousMaster
			model.LOG_DB = previousLogDB
		}
		common.OptionMap = previousOptions
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousModelRatio))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousModelPrice))
		config.UpdateConfigFromMap(config.GlobalConfig.Get("billing_setting"), map[string]string{"billing_mode": previousBillingConfig["billing_setting.billing_mode"], "billing_expr": previousBillingConfig["billing_setting.billing_expr"], "plugin_billing_expr": previousBillingConfig["billing_setting.plugin_billing_expr"]})
		connection, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, connection.Close())
	})
	return db
}

func TestChannelStatusDatabaseMatrix(t *testing.T) {
	for _, database := range []struct{ kind, dsn string }{
		{"sqlite", ""},
		{"mysql", os.Getenv("TEST_MYSQL_DSN")},
		{"postgres", os.Getenv("TEST_POSTGRES_DSN")},
	} {
		t.Run(database.kind, func(t *testing.T) {
			if database.kind != "sqlite" && database.dsn == "" {
				t.Skip("set TEST_MYSQL_DSN and TEST_POSTGRES_DSN to run the real database matrix")
			}
			db := channelStatusTestDB(t, database.kind, database.dsn)
			previousLogConsume := common.LogConsumeEnabled
			common.LogConsumeEnabled = false
			t.Cleanup(func() { common.LogConsumeEnabled = previousLogConsume })
			service.InitHttpClient()
			user := model.User{Username: "channel-status-root", Group: "default", Role: common.RoleRootUser, Status: common.UserStatusEnabled, AuthVersion: 1, Quota: 100000, AffCode: "channel-status-root"}
			require.NoError(t, db.Create(&user).Error)
			var calls atomic.Int32
			var fail atomic.Bool
			prompt := make(chan string, 4)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				var request dto.GeneralOpenAIRequest
				if err := common.DecodeJson(r.Body, &request); err != nil {
					http.Error(w, "bad request", http.StatusBadRequest)
					return
				}
				if len(request.Messages) != 0 {
					if text, ok := request.Messages[0].Content.(string); ok {
						prompt <- text
					}
				}
				w.Header().Set("Content-Type", "application/json")
				if fail.Load() {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = w.Write([]byte(`{"error":{"message":"offline"}}`))
					return
				}
				_, _ = w.Write([]byte(`{"id":"probe-success","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`))
			}))
			defer upstream.Close()
			testModel := "preserve-business-test-model"
			channel := model.Channel{Name: "private-channel-name", Key: "private-channel-key", Models: "gpt-4o-mini", Group: "default,vip", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, BaseURL: &upstream.URL, TestModel: &testModel}
			require.NoError(t, db.Create(&channel).Error)
			probe := channelStatusProbe{Name: "private-probe-name", ChannelID: channel.Id, Model: "gpt-4o-mini", Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5, Prompt: "Reply with OK"}
			config := channelStatusProbeConfig{Enabled: true, Probes: []channelStatusProbe{probe}}
			var response struct {
				Success bool `json:"success"`
				Data    struct {
					Config  channelStatusProbeConfig   `json:"config"`
					Results []channelStatusProbeResult `json:"results"`
				} `json:"data"`
			}
			saved := modelManagementRequest(t, UpdateChannelStatusProbes, http.MethodPut, "/api/channel/status/probes/", config, &response)
			require.Equal(t, http.StatusOK, saved.Code)
			require.True(t, response.Success, saved.Body.String())
			require.Len(t, response.Data.Config.Probes, 1)
			probe = response.Data.Config.Probes[0]
			require.NotEmpty(t, probe.ID)
			queued := modelManagementRequest(t, RunChannelStatusProbes, http.MethodPost, "/api/channel/status/probes/run/", nil, nil)
			require.Equal(t, http.StatusOK, queued.Code)
			assert.Contains(t, queued.Body.String(), `"status":"pending"`)
			duplicate := modelManagementRequest(t, RunChannelStatusProbes, http.MethodPost, "/api/channel/status/probes/run/", nil, nil)
			require.Equal(t, http.StatusConflict, duplicate.Code)
			require.NoError(t, db.Where("type = ?", model.SystemTaskTypeChannelStatusProbe).Delete(&model.SystemTask{}).Error)
			common.OptionMapRWMutex.RLock()
			cachedOptions := make(map[string]string, len(common.OptionMap))
			for key, value := range common.OptionMap {
				if key != channelStatusConfigOption && key != channelStatusResultsOption {
					cachedOptions[key] = value
				}
			}
			common.OptionMapRWMutex.RUnlock()
			common.OptionMapRWMutex.Lock()
			common.OptionMap = cachedOptions
			common.OptionMapRWMutex.Unlock()
			restored, _, err := readChannelStatusState()
			require.NoError(t, err)
			assert.Equal(t, response.Data.Config, restored, "persisted config must survive empty process cache")
			assert.True(t, (channelStatusProbeHandler{}).Enabled(), "new enabled probe must be due immediately")
			results, err := runChannelStatusProbeTask(context.Background(), channelStatusProbeTaskPayload{}, "test-task")
			require.NoError(t, err)
			require.Len(t, results, 1)
			assert.Equal(t, channelStatusOperational, results[0].Status)
			select {
			case value := <-prompt:
				assert.Equal(t, "Reply with OK", value)
			default:
				t.Fatal("probe prompt was not sent upstream")
			}
			assert.False(t, (channelStatusProbeHandler{}).Enabled(), "last persisted result must prevent duplicate scheduled requests")
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = runChannelStatusProbeTask(cancelled, channelStatusProbeTaskPayload{Force: true}, "test-task")
			require.ErrorIs(t, err, context.Canceled)
			assert.Equal(t, int32(1), calls.Load(), "cancelled lease must not send an upstream request")
			results, err = runChannelStatusProbeTask(context.Background(), channelStatusProbeTaskPayload{}, "test-task")
			require.NoError(t, err)
			assert.Empty(t, results)
			assert.Equal(t, int32(1), calls.Load())
			fail.Store(true)
			results, err = runChannelStatusProbeTask(context.Background(), channelStatusProbeTaskPayload{ProbeID: probe.ID, Force: true}, "test-task")
			require.NoError(t, err)
			require.Len(t, results, 1)
			assert.Equal(t, channelStatusOutage, results[0].Status)
			select {
			case value := <-prompt:
				assert.Equal(t, "Reply with OK", value)
			default:
				t.Fatal("probe prompt was not sent upstream")
			}
			stored, err := model.GetChannelById(channel.Id, true)
			require.NoError(t, err)
			assert.Equal(t, common.ChannelStatusEnabled, stored.Status, "failed status probes must not disable production channels")
			assert.Equal(t, &testModel, stored.TestModel)
			assert.Zero(t, stored.ResponseTime)
			unsupported := model.Channel{Name: "task-channel", Key: "task-key", Models: "gpt-4o-mini", Group: "tasks", Type: constant.ChannelTypeTaskPlugin, Status: common.ChannelStatusEnabled}
			require.NoError(t, db.Create(&unsupported).Error)
			unsupportedProbe := probe
			unsupportedProbe.ChannelID = unsupported.Id
			assert.Equal(t, channelStatusOutage, executeChannelStatusProbe(context.Background(), unsupportedProbe, user.Id).Status, "local test failures cannot be reported as healthy")
			assert.Equal(t, int32(2), calls.Load())
			public := modelManagementRequest(t, GetChannelGroupStatus, http.MethodGet, "/api/channel/status/", nil, nil)
			require.Equal(t, http.StatusOK, public.Code)
			for _, secret := range []string{channel.Key, channel.Name, probe.Name, probe.Model, upstream.URL, "probe_id", "channel_id", "prompt", "results", "config", "offline"} {
				assert.NotContains(t, public.Body.String(), secret)
			}
			type groupStatusResponse struct {
				Success bool `json:"success"`
				Data    struct {
					Groups []channelGroupStatus `json:"groups"`
				} `json:"data"`
			}
			var groups groupStatusResponse
			require.NoError(t, common.Unmarshal(public.Body.Bytes(), &groups))
			require.True(t, groups.Success)
			var ordinaryGroups groupStatusResponse
			ordinary := modelManagementRequest(t, func(c *gin.Context) {
				c.Set("role", common.RoleCommonUser)
				GetChannelGroupStatus(c)
			}, http.MethodGet, "/api/channel/status/", nil, &ordinaryGroups)
			require.Equal(t, http.StatusOK, ordinary.Code)
			assert.Equal(t, groups, ordinaryGroups, "all roles receive the same monitored group summary")
			found := false
			for _, group := range groups.Data.Groups {
				if group.Group == "default" {
					found = true
					assert.Equal(t, channelStatusOutage, group.Status)
					assert.Equal(t, 1, group.TotalChannels)
				}
			}
			assert.True(t, found)
			config.Probes = []channelStatusProbe{}
			deleted := modelManagementRequest(t, UpdateChannelStatusProbes, http.MethodPut, "/api/channel/status/probes/", config, nil)
			require.Equal(t, http.StatusOK, deleted.Code)
			_, observations, err := readChannelStatusState()
			require.NoError(t, err)
			assert.Empty(t, observations, "deleted probe history must be pruned")
			assert.False(t, (channelStatusProbeHandler{}).Enabled())
			emptyPublic := modelManagementRequest(t, GetChannelGroupStatus, http.MethodGet, "/api/channel/status/", nil, &groups)
			require.Equal(t, http.StatusOK, emptyPublic.Code)
			assert.Empty(t, groups.Data.Groups, "deleting the last probe removes its group from the summary")
			verifyChannelStatusIndependentProbes(t, db, user.Id)
		})
	}
}

func verifyChannelStatusIndependentProbes(t *testing.T, db *gorm.DB, userID int) {
	t.Helper()
	responseBody := `{"id":"independent-probe","object":"chat.completion","created":1,"model":"gpt-4o-mini","choices":[{"index":0,"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}`
	fastRequests := make(chan struct{}, 4)
	slowStarted := make(chan struct{}, 1)
	slowCancelled := make(chan struct{}, 1)
	releaseSlow := make(chan struct{})
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(responseBody))
		fastRequests <- struct{}{}
	}))
	defer fast.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slowStarted <- struct{}{}
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-releaseSlow:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(responseBody))
		case <-r.Context().Done():
			slowCancelled <- struct{}{}
		}
	}))
	defer slow.Close()
	fastChannel := model.Channel{Name: "fast", Key: "fast-key", Models: "gpt-4o-mini", Group: "fast", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, BaseURL: &fast.URL}
	slowChannel := model.Channel{Name: "slow", Key: "slow-key", Models: "gpt-4o-mini", Group: "slow", Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, BaseURL: &slow.URL}
	require.NoError(t, db.Create(&fastChannel).Error)
	require.NoError(t, db.Create(&slowChannel).Error)
	fastProbe := channelStatusProbe{ID: "fast", Name: "fast", ChannelID: fastChannel.Id, Model: "gpt-4o-mini", IntervalSeconds: 60, TimeoutSeconds: 5, Enabled: true}
	slowProbe := fastProbe
	slowProbe.ID, slowProbe.Name, slowProbe.ChannelID = "slow", "slow", slowChannel.Id
	slowProbe.IntervalSeconds, slowProbe.TimeoutSeconds = 60, 10
	warmProbe := fastProbe
	warmProbe.ID, warmProbe.Name = "warm", "warm"
	probeConfig := channelStatusProbeConfig{Enabled: true, Probes: []channelStatusProbe{fastProbe, slowProbe, warmProbe}}
	encoded, err := common.Marshal(probeConfig)
	require.NoError(t, err)
	warmObservation := channelStatusObservation{Probe: warmProbe, Result: channelStatusProbeResult{ProbeID: warmProbe.ID, Status: channelStatusOperational, CheckedAt: time.Now().Unix()}}
	seeded, err := common.Marshal(map[string]channelStatusObservation{warmProbe.ID: warmObservation})
	require.NoError(t, err)
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{channelStatusConfigOption: string(encoded), channelStatusResultsOption: string(seeded)}))
	active, err := model.CreateSystemTask(model.SystemTaskTypeChannelStatusProbe, channelStatusProbeTaskPayload{Force: true}, nil)
	require.NoError(t, err)
	require.NoError(t, db.Model(active).Update("status", model.SystemTaskStatusRunning).Error)
	t.Cleanup(func() { require.NoError(t, db.Where("task_id = ?", active.TaskID).Delete(&model.SystemTask{}).Error) })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := runChannelStatusProbeTask(ctx, channelStatusProbeTaskPayload{}, active.TaskID)
		finished <- err
	}()
	select {
	case <-slowStarted:
	case <-ctx.Done():
		t.Fatal("slow probe did not start")
	}
	select {
	case <-fastRequests:
	case <-ctx.Done():
		t.Fatal("fast probe did not start")
	}
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	var observations map[string]channelStatusObservation
	for {
		_, observations, err = readChannelStatusState()
		require.NoError(t, err)
		if observation, exists := observations[fastProbe.ID]; exists && !observation.Result.Running && observation.Result.Status == channelStatusOperational {
			break
		}
		select {
		case <-poll.C:
		case <-ctx.Done():
			t.Fatal("fast probe result was blocked by slow probe")
		}
	}
	select {
	case err := <-finished:
		t.Fatalf("task ended before slow probe was released: %v", err)
	default:
	}
	var adminResponse struct {
		Data struct {
			Results []channelStatusProbeResult `json:"results"`
		} `json:"data"`
	}
	admin := modelManagementRequest(t, GetChannelStatusProbes, http.MethodGet, "/api/channel/status/probes/", nil, &adminResponse)
	require.Equal(t, http.StatusOK, admin.Code)
	for _, result := range adminResponse.Data.Results {
		assert.Equal(t, result.ProbeID == slowProbe.ID, result.Running, "completed and not-yet-due probes must not inherit the active task's force flag")
	}
	observation := observations[fastProbe.ID]
	observation.Result.CheckedAt -= int64(fastProbe.IntervalSeconds + 1)
	require.NoError(t, persistChannelStatusObservation(observation))
	warmObservation.Result.CheckedAt -= int64(warmProbe.IntervalSeconds + 1)
	require.NoError(t, persistChannelStatusObservation(warmObservation))
	for range 2 {
		select {
		case <-fastRequests:
		case <-ctx.Done():
			t.Fatal("a due fast probe was blocked by slow probe")
		}
	}
	close(releaseSlow)
	select {
	case err := <-finished:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("probe task did not finish")
	}
	_, observations, err = readChannelStatusState()
	require.NoError(t, err)
	assert.False(t, observations[fastProbe.ID].Result.Running)
	assert.Equal(t, channelStatusOperational, observations[fastProbe.ID].Result.Status)
	assert.Equal(t, channelStatusOperational, observations[slowProbe.ID].Result.Status)
	assert.Greater(t, observations[warmProbe.ID].Result.CheckedAt, warmObservation.Result.CheckedAt, "a probe that becomes due during the batch must run")
	observation = observations[fastProbe.ID]
	observation.Result.Running = true
	observation.TaskID = "interrupted-task"
	require.NoError(t, persistChannelStatusObservation(observation))
	admin = modelManagementRequest(t, GetChannelStatusProbes, http.MethodGet, "/api/channel/status/probes/", nil, &adminResponse)
	require.Equal(t, http.StatusOK, admin.Code)
	for _, result := range adminResponse.Data.Results {
		assert.False(t, result.Running, "a stale running marker must not belong to a different active task")
	}
	require.NoError(t, db.Where("task_id = ?", active.TaskID).Delete(&model.SystemTask{}).Error)
	admin = modelManagementRequest(t, GetChannelStatusProbes, http.MethodGet, "/api/channel/status/probes/", nil, &adminResponse)
	require.Equal(t, http.StatusOK, admin.Code)
	for _, result := range adminResponse.Data.Results {
		assert.False(t, result.Running, "persisted running markers must not survive an inactive task")
	}

	// Test the configured deadline against an upstream that accepts the body
	// but never sends headers. Server-side cancellation proves the request
	// was stopped, rather than only marking a timed-out wrapper as failed.
	if db.Dialector.Name() != "sqlite" {
		return
	}
	timeoutStarted := make(chan struct{}, 1)
	timeoutCancelled := make(chan struct{}, 1)
	timeoutUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		timeoutStarted <- struct{}{}
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
		timeoutCancelled <- struct{}{}
	}))
	defer timeoutUpstream.Close()
	timeoutChannel := fastChannel
	timeoutChannel.Id, timeoutChannel.Name, timeoutChannel.BaseURL = 0, "timeout", &timeoutUpstream.URL
	require.NoError(t, db.Create(&timeoutChannel).Error)
	timeoutProbe := fastProbe
	timeoutProbe.ChannelID = timeoutChannel.Id
	timeoutResult := make(chan channelStatusProbeResult, 1)
	go func() { timeoutResult <- executeChannelStatusProbe(ctx, timeoutProbe, userID) }()
	select {
	case <-timeoutStarted:
	case <-ctx.Done():
		t.Fatal("timeout probe did not start")
	}
	select {
	case result := <-timeoutResult:
		assert.Equal(t, channelStatusOutage, result.Status)
		assert.Equal(t, "探针请求超时", result.Message)
	case <-ctx.Done():
		t.Fatal("configured probe timeout was not applied")
	}
	select {
	case <-timeoutCancelled:
	case <-ctx.Done():
		t.Fatal("upstream request was not cancelled at probe deadline")
	}
}

func TestChannelStatusAggregation(t *testing.T) {
	now := time.Now().Unix()
	healthyProbe := channelStatusProbe{ID: "healthy", ChannelID: 1, Enabled: true, IntervalSeconds: 60, TimeoutSeconds: 5}
	downProbe := healthyProbe
	downProbe.ID, downProbe.ChannelID = "down", 2
	unknownProbe := healthyProbe
	unknownProbe.ID, unknownProbe.ChannelID, unknownProbe.Enabled = "unknown", 3, false
	observations := map[string]channelStatusObservation{
		"healthy": {Probe: healthyProbe, Result: channelStatusProbeResult{ProbeID: "healthy", Status: channelStatusOperational, CheckedAt: now, ResponseTimeMS: 20}},
		"down":    {Probe: downProbe, Result: channelStatusProbeResult{ProbeID: "down", Status: channelStatusOutage, CheckedAt: now, ResponseTimeMS: 40}},
	}
	channels := []*model.Channel{
		{Id: 1, Group: "healthy,mixed", Status: common.ChannelStatusEnabled},
		{Id: 2, Group: "down,mixed", Status: common.ChannelStatusManuallyDisabled},
		{Id: 3, Group: "unknown", Status: common.ChannelStatusEnabled},
		{Id: 4, Group: "unmonitored,mixed", Status: common.ChannelStatusManuallyDisabled},
	}
	for _, test := range []struct {
		name     string
		probes   []channelStatusProbe
		expected []channelGroupStatus
	}{
		{
			name:   "monitored health states",
			probes: []channelStatusProbe{healthyProbe, downProbe, unknownProbe},
			expected: []channelGroupStatus{
				{Group: "down", Status: channelStatusOutage, TotalChannels: 1, LastCheckedAt: now, ResponseTimeMS: 40},
				{Group: "healthy", Status: channelStatusOperational, TotalChannels: 1, AvailableChannels: 1, LastCheckedAt: now, ResponseTimeMS: 20},
				{Group: "mixed", Status: channelStatusDegraded, TotalChannels: 2, AvailableChannels: 1, LastCheckedAt: now, ResponseTimeMS: 40},
				{Group: "unknown", Status: channelStatusUnknown, TotalChannels: 1},
			},
		},
		{
			name:   "only configured channels contribute",
			probes: []channelStatusProbe{healthyProbe},
			expected: []channelGroupStatus{
				{Group: "healthy", Status: channelStatusOperational, TotalChannels: 1, AvailableChannels: 1, LastCheckedAt: now, ResponseTimeMS: 20},
				{Group: "mixed", Status: channelStatusOperational, TotalChannels: 1, AvailableChannels: 1, LastCheckedAt: now, ResponseTimeMS: 20},
			},
		},
		{
			name:     "disabled configured probe stays visible",
			probes:   []channelStatusProbe{unknownProbe},
			expected: []channelGroupStatus{{Group: "unknown", Status: channelStatusUnknown, TotalChannels: 1}},
		},
		{
			name:     "no probes and deleted probe results show no groups",
			expected: []channelGroupStatus{},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual := aggregateChannelGroupStatus(channels, channelStatusProbeConfig{Probes: test.probes}, observations, now)
			assert.Equal(t, test.expected, actual)
		})
	}
	assert.Equal(t, channelStatusUnknown, channelStatusCurrentResult(healthyProbe, observations, now+126).Status, "expired checks cannot claim current health")
	healthyProbe.Model = "changed-target"
	assert.Equal(t, channelStatusUnknown, channelStatusCurrentResult(healthyProbe, observations, now).Status)
}

func TestChannelStatusRejectsInvalidProbeConfig(t *testing.T) {
	probe := channelStatusProbe{ID: "probe", Name: "health", ChannelID: 1, Model: "gpt-4o-mini", IntervalSeconds: 60, TimeoutSeconds: 5, Enabled: true}
	for _, test := range []struct {
		name   string
		change func(*channelStatusProbeConfig)
	}{
		{"interval too short", func(config *channelStatusProbeConfig) { config.Probes[0].IntervalSeconds = 59 }},
		{"timeout too long", func(config *channelStatusProbeConfig) { config.Probes[0].TimeoutSeconds = 61 }},
		{"missing channel", func(config *channelStatusProbeConfig) { config.Probes[0].ChannelID = 0 }},
		{"missing model", func(config *channelStatusProbeConfig) { config.Probes[0].Model = "" }},
		{"long prompt", func(config *channelStatusProbeConfig) { config.Probes[0].Prompt = strings.Repeat("a", 2001) }},
		{"duplicate id", func(config *channelStatusProbeConfig) { config.Probes = append(config.Probes, probe) }},
		{"invalid endpoint", func(config *channelStatusProbeConfig) { config.Probes[0].EndpointType = "not-a-supported-endpoint" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := channelStatusProbeConfig{Probes: []channelStatusProbe{probe}}
			test.change(&config)
			require.Error(t, validateChannelStatusProbeConfig(&config, true))
		})
	}
}

func TestChannelStatusHandlersRequireRoot(t *testing.T) {
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser} {
		for _, handler := range []gin.HandlerFunc{GetChannelStatusProbes, UpdateChannelStatusProbes, RunChannelStatusProbes} {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Set("role", role)
			handler(context)
			assert.Equal(t, http.StatusForbidden, recorder.Code)
		}
	}
}
