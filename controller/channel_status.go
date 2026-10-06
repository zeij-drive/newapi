package controller

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
)

const (
	channelStatusConfigOption  = "ChannelStatusProbeConfig"
	channelStatusResultsOption = "ChannelStatusProbeResults"
	channelStatusOperational   = "operational"
	channelStatusDegraded      = "degraded"
	channelStatusOutage        = "outage"
	channelStatusUnknown       = "unknown"
	channelStatusProbeInterval = 600
)

type channelStatusProbe struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ChannelID       int    `json:"channel_id"`
	Model           string `json:"model"`
	EndpointType    string `json:"endpoint_type"`
	IsStream        bool   `json:"is_stream"`
	IntervalSeconds int    `json:"interval_seconds"`
	TimeoutSeconds  int    `json:"timeout_seconds"`
	Prompt          string `json:"prompt"`
	Enabled         bool   `json:"enabled"`
}

type channelStatusProbeConfig struct {
	Enabled bool                 `json:"enabled"`
	Probes  []channelStatusProbe `json:"probes"`
}

type channelStatusProbeResult struct {
	ProbeID        string `json:"probe_id"`
	Status         string `json:"status"`
	CheckedAt      int64  `json:"checked_at"`
	StartedAt      int64  `json:"started_at"`
	ResponseTimeMS int64  `json:"response_time_ms"`
	Message        string `json:"message"`
	Running        bool   `json:"running"`
	UserRequests   int64  `json:"user_requests"`
	UserSuccesses  int64  `json:"user_successes"`
}

// Store the exact configuration with a result: editing a target, model or
// request invalidates its old observation even while another node is running it.
type channelStatusObservation struct {
	Probe  channelStatusProbe       `json:"probe"`
	Result channelStatusProbeResult `json:"result"`
	TaskID string                   `json:"task_id,omitempty"`
}

type channelGroupStatus struct {
	Group             string `json:"group"`
	Status            string `json:"status"`
	AvailableChannels int    `json:"available_channels"`
	TotalChannels     int    `json:"total_channels"`
	LastCheckedAt     int64  `json:"last_checked_at"`
	ResponseTimeMS    int64  `json:"response_time_ms"`
	UserRequests      int64  `json:"-"`
	UserSuccesses     int64  `json:"-"`
}

type channelStatusProbeTarget struct {
	ID     int      `json:"id"`
	Name   string   `json:"name"`
	Models []string `json:"models"`
	Group  string   `json:"group"`
}

type channelProbeTestOptions struct{ Prompt string }

var channelStatusResultsMu sync.Mutex

func applyChannelProbePrompt(request dto.Request, prompt string) error {
	switch request := request.(type) {
	case *dto.GeneralOpenAIRequest:
		request.Messages = []dto.Message{{Role: "user", Content: prompt}}
	case *dto.ClaudeRequest:
		request.Messages = []dto.ClaudeMessage{{Role: "user", Content: prompt}}
	case *dto.GeminiChatRequest:
		request.Contents = []dto.GeminiChatContent{{Role: "user", Parts: []dto.GeminiPart{{Text: prompt}}}}
	case *dto.OpenAIResponsesRequest:
		input, err := common.Marshal([]dto.Message{{Role: "user", Content: prompt}})
		if err != nil {
			return err
		}
		request.Input = input
	case *dto.OpenAIResponsesCompactionRequest:
		input, err := common.Marshal([]dto.Message{{Role: "user", Content: prompt}})
		if err != nil {
			return err
		}
		request.Input = input
	case *dto.EmbeddingRequest:
		request.Input = []any{prompt}
	case *dto.RerankRequest:
		request.Query = prompt
	case *dto.ImageRequest:
		request.Prompt = prompt
	}
	return nil
}

func readChannelStatusState() (channelStatusProbeConfig, map[string]channelStatusObservation, error) {
	config := channelStatusProbeConfig{Probes: []channelStatusProbe{}}
	observations := map[string]channelStatusObservation{}
	// The system task lease may move between masters before OptionMap syncs.
	// Read both persisted values together so a new lease observes the last run.
	var options []model.Option
	if err := model.DB.Where(map[string]any{"key": []string{channelStatusConfigOption, channelStatusResultsOption}}).Find(&options).Error; err != nil {
		return config, observations, err
	}
	configJSON, resultsJSON := "", ""
	for _, option := range options {
		if option.Key == channelStatusConfigOption {
			configJSON = option.Value
		}
		if option.Key == channelStatusResultsOption {
			resultsJSON = option.Value
		}
	}
	if configJSON != "" {
		if err := common.UnmarshalJsonStr(configJSON, &config); err != nil {
			return config, observations, errors.New("渠道状态探针配置无效")
		}
		if err := validateChannelStatusProbeConfig(&config, false); err != nil {
			return config, observations, err
		}
	}
	if resultsJSON != "" {
		if err := common.UnmarshalJsonStr(resultsJSON, &observations); err != nil {
			return config, observations, errors.New("渠道状态探针结果无效")
		}
	}
	if observations == nil {
		observations = map[string]channelStatusObservation{}
	}
	for id, observation := range observations {
		observation.Probe.IntervalSeconds = channelStatusProbeInterval
		observations[id] = observation
	}
	return config, observations, nil
}

func validateChannelStatusProbeConfig(config *channelStatusProbeConfig, assignIDs bool) error {
	if len(config.Probes) > 64 {
		return errors.New("最多配置 64 个探针")
	}
	if config.Probes == nil {
		config.Probes = []channelStatusProbe{}
	}
	ids := map[string]bool{}
	for i := range config.Probes {
		probe := &config.Probes[i]
		probe.ID = strings.TrimSpace(probe.ID)
		probe.Name = strings.TrimSpace(probe.Name)
		probe.Model = strings.TrimSpace(probe.Model)
		probe.EndpointType = strings.TrimSpace(probe.EndpointType)
		if probe.ID == "" && assignIDs {
			probe.ID = common.GetRandomString(24)
		}
		if probe.ID == "" || len(probe.ID) > 64 || strings.ContainsAny(probe.ID, "/\\?#") || ids[probe.ID] {
			return errors.New("探针 ID 无效或重复")
		}
		ids[probe.ID] = true
		if probe.ChannelID <= 0 || probe.Name == "" || utf8.RuneCountInString(probe.Name) > 80 || probe.Model == "" || utf8.RuneCountInString(probe.Model) > 200 {
			return errors.New("探针名称、渠道或模型无效")
		}
		if utf8.RuneCountInString(probe.Prompt) > 2000 {
			return errors.New("探针提示词最多 2000 个字符")
		}
		probe.IntervalSeconds = channelStatusProbeInterval
		if probe.TimeoutSeconds < 5 || probe.TimeoutSeconds > 300 {
			return errors.New("探针每 10 分钟检测一次，超时应为 5 至 300 秒")
		}
		if probe.EndpointType != "" {
			switch constant.EndpointType(probe.EndpointType) {
			case constant.EndpointTypeOpenAI, constant.EndpointTypeOpenAIResponse, constant.EndpointTypeOpenAIResponseCompact,
				constant.EndpointTypeAnthropic, constant.EndpointTypeGemini, constant.EndpointTypeEmbeddings,
				constant.EndpointTypeJinaRerank, constant.EndpointTypeImageGeneration:
			default:
				return errors.New("探针端点类型无效")
			}
		}
	}
	return nil
}

func channelStatusCurrentResult(probe channelStatusProbe, observations map[string]channelStatusObservation, now int64) channelStatusProbeResult {
	result := channelStatusProbeResult{ProbeID: probe.ID, Status: channelStatusUnknown}
	observation, ok := observations[probe.ID]
	if !ok || observation.Probe != probe {
		return result
	}
	result = observation.Result
	result.Running = false
	if now-result.CheckedAt > int64(channelStatusProbeInterval*2+probe.TimeoutSeconds) || !probe.Enabled {
		result.Status = channelStatusUnknown
	}
	return result
}

func channelStatusProbeDue(probe channelStatusProbe, observations map[string]channelStatusObservation, now int64) bool {
	observation, exists := observations[probe.ID]
	if !exists || observation.Probe != probe {
		return true
	}
	startedAt := observation.Result.StartedAt
	if startedAt == 0 {
		startedAt = observation.Result.CheckedAt
	}
	return now-startedAt >= channelStatusProbeInterval
}

func GetChannelGroupStatus(c *gin.Context) {
	config, observations, err := readChannelStatusState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channels, err := model.GetAllChannels(0, -1, false, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channelIDs := make([]int, 0, len(config.Probes))
	for _, probe := range config.Probes {
		channelIDs = append(channelIDs, probe.ChannelID)
	}
	now := time.Now()
	usage := perfmetrics.RecentChannelUsage(channelIDs, now)
	groups := aggregateChannelGroupStatus(channels, config, observations, now.Unix(), usage)
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"groups": groups, "updated_at": time.Now().Unix()}})
}

func aggregateChannelGroupStatus(channels []*model.Channel, config channelStatusProbeConfig, observations map[string]channelStatusObservation, now int64, usage map[int]perfmetrics.ChannelUsage) []channelGroupStatus {
	groups := map[string]*channelGroupStatus{}
	unknown := map[string]int{}
	degraded := map[string]int{}
	monitoredChannels := make(map[int]bool, len(config.Probes))
	for _, probe := range config.Probes {
		monitoredChannels[probe.ChannelID] = true
	}
	for _, channel := range channels {
		if !monitoredChannels[channel.Id] {
			continue
		}
		status, checkedAt, latency := channelStatusUnknown, int64(0), int64(0)
		successfulChecks, failedChecks, missingChecks := int64(0), int64(0), false
		traffic := usage[channel.Id]
		if channel.Status != common.ChannelStatusEnabled {
			status = channelStatusOutage
		}
		for _, probe := range config.Probes {
			if !probe.Enabled || probe.ChannelID != channel.Id {
				continue
			}
			result := channelStatusCurrentResult(probe, observations, now)
			checkedAt = max(checkedAt, result.CheckedAt)
			latency = max(latency, result.ResponseTimeMS)
			if channel.Status != common.ChannelStatusEnabled {
				continue
			}
			switch result.Status {
			case channelStatusOperational:
				successfulChecks++
			case channelStatusOutage:
				failedChecks++
			default:
				missingChecks = true
			}
		}
		if channel.Status == common.ChannelStatusEnabled {
			successfulChecks += traffic.Successes
			failedChecks += traffic.Requests - traffic.Successes
			checkedAt = max(checkedAt, traffic.LastRequestAt)
			if traffic.Requests > 0 {
				latency = max(latency, traffic.TotalLatencyMS/traffic.Requests)
			}
			switch {
			case successfulChecks > 0 && (failedChecks > 0 || missingChecks && traffic.Requests == 0):
				status = channelStatusDegraded
			case successfulChecks > 0:
				status = channelStatusOperational
			case failedChecks > 0:
				status = channelStatusOutage
			}
		}
		for _, group := range channel.GetGroups() {
			if group == "" {
				continue
			}
			entry := groups[group]
			if entry == nil {
				entry = &channelGroupStatus{Group: group}
				groups[group] = entry
			}
			entry.TotalChannels++
			entry.LastCheckedAt = max(entry.LastCheckedAt, checkedAt)
			entry.ResponseTimeMS = max(entry.ResponseTimeMS, latency)
			entry.UserRequests += traffic.Requests
			entry.UserSuccesses += traffic.Successes
			if status == channelStatusOperational {
				entry.AvailableChannels++
			}
			if status == channelStatusUnknown {
				unknown[group]++
			}
			if status == channelStatusDegraded {
				degraded[group]++
			}
		}
	}
	names := make([]string, 0, len(groups))
	for group := range groups {
		names = append(names, group)
	}
	slices.Sort(names)
	result := make([]channelGroupStatus, 0, len(names))
	for _, group := range names {
		entry := groups[group]
		switch {
		case entry.TotalChannels == 0, unknown[group] == entry.TotalChannels:
			entry.Status = channelStatusUnknown
		case entry.AvailableChannels == entry.TotalChannels && degraded[group] == 0:
			entry.Status = channelStatusOperational
		case entry.AvailableChannels > 0 || degraded[group] > 0:
			entry.Status = channelStatusDegraded
		case unknown[group] > 0:
			entry.Status = channelStatusUnknown
		default:
			entry.Status = channelStatusOutage
		}
		result = append(result, *entry)
	}
	return result
}

func GetChannelStatusProbes(c *gin.Context) {
	if c.GetInt("role") < common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "仅最高管理员可以管理探针"})
		return
	}
	config, observations, err := readChannelStatusState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	channels, err := model.GetAllChannels(0, -1, false, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	targets := make([]channelStatusProbeTarget, 0, len(channels))
	for _, channel := range channels {
		targets = append(targets, channelStatusProbeTarget{ID: channel.Id, Name: channel.Name, Models: channel.GetModels(), Group: channel.Group})
	}
	results := make([]channelStatusProbeResult, 0, len(config.Probes))
	channelIDs := make([]int, 0, len(config.Probes))
	for _, probe := range config.Probes {
		channelIDs = append(channelIDs, probe.ChannelID)
	}
	usage := perfmetrics.RecentChannelUsage(channelIDs, time.Now())
	active, err := model.GetActiveSystemTask(model.SystemTaskTypeChannelStatusProbe)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	payload := channelStatusProbeTaskPayload{}
	if active != nil {
		_ = active.DecodePayload(&payload)
	}
	for _, probe := range config.Probes {
		result := channelStatusCurrentResult(probe, observations, time.Now().Unix())
		result.UserRequests = usage[probe.ChannelID].Requests
		result.UserSuccesses = usage[probe.ChannelID].Successes
		// Running is a transient scheduler state. A persisted observation can
		// remain marked while a worker is interrupted, so never expose that bit
		// unless an active system task still owns the probe run.
		result.Running = false
		if active != nil && probe.Enabled && (payload.ProbeID == "" || payload.ProbeID == probe.ID) {
			observation, exists := observations[probe.ID]
			if active.Status == model.SystemTaskStatusPending {
				result.Running = payload.Force || channelStatusProbeDue(probe, observations, time.Now().Unix())
			} else {
				result.Running = exists && observation.Probe == probe && observation.TaskID == active.TaskID && observation.Result.Running
			}
		}
		results = append(results, result)
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"config": config, "results": results, "channels": targets}})
}

func UpdateChannelStatusProbes(c *gin.Context) {
	if c.GetInt("role") < common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "仅最高管理员可以管理探针"})
		return
	}
	var config channelStatusProbeConfig
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	if err := common.DecodeJson(c.Request.Body, &config); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "探针配置无效"})
		return
	}
	if err := validateChannelStatusProbeConfig(&config, true); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	channelStatusResultsMu.Lock()
	defer channelStatusResultsMu.Unlock()
	for _, probe := range config.Probes {
		channel, err := model.GetChannelById(probe.ChannelID, false)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "探针渠道不存在"})
			return
		}
		if !slices.Contains(channel.GetModels(), probe.Model) {
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "探针模型不属于所选渠道"})
			return
		}
	}
	_, observations, err := readChannelStatusState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	retained := map[string]channelStatusObservation{}
	for _, probe := range config.Probes {
		if observation, exists := observations[probe.ID]; exists && observation.Probe == probe {
			retained[probe.ID] = observation
		}
	}
	data, err := common.Marshal(config)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	resultData, err := common.Marshal(retained)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOptionsBulk(map[string]string{channelStatusConfigOption: string(data), channelStatusResultsOption: string(resultData)}); err != nil {
		common.ApiError(c, err)
		return
	}
	GetChannelStatusProbes(c)
}

type channelStatusProbeTaskPayload struct {
	ProbeID string `json:"probe_id,omitempty"`
	Force   bool   `json:"force,omitempty"`
}

func RunChannelStatusProbes(c *gin.Context) {
	if c.GetInt("role") < common.RoleRootUser {
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "仅最高管理员可以运行探针"})
		return
	}
	config, _, err := readChannelStatusState()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	probeID := c.Param("id")
	found := false
	for _, probe := range config.Probes {
		if probe.Enabled && (probeID == "" || probe.ID == probeID) {
			found = true
		}
	}
	if !found {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "没有可运行的已启用探针"})
		return
	}
	task, created, err := service.EnqueueSystemTask(model.SystemTaskTypeChannelStatusProbe, channelStatusProbeTaskPayload{ProbeID: probeID, Force: true})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if !created {
		c.JSON(http.StatusConflict, gin.H{"success": false, "message": "已有渠道状态探针任务正在运行或等待中"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"success": true, "message": "", "data": gin.H{"task_id": task.TaskID, "status": task.Status}})
}

type channelStatusProbeHandler struct{}

func (channelStatusProbeHandler) Type() string            { return model.SystemTaskTypeChannelStatusProbe }
func (channelStatusProbeHandler) Interval() time.Duration { return 15 * time.Second }
func (channelStatusProbeHandler) NewPayload() any         { return nil }
func (channelStatusProbeHandler) Enabled() bool {
	config, observations, err := readChannelStatusState()
	if err != nil || !config.Enabled {
		return false
	}
	for _, probe := range config.Probes {
		if probe.Enabled && channelStatusProbeDue(probe, observations, time.Now().Unix()) {
			return true
		}
	}
	return false
}

func (channelStatusProbeHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	payload := channelStatusProbeTaskPayload{}
	if err := task.DecodePayload(&payload); err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	results, err := runChannelStatusProbeTask(ctx, payload, task.TaskID)
	if err != nil {
		finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusFailed, nil, err)
		return
	}
	finishSystemTaskHandler(task, runnerID, model.SystemTaskStatusSucceeded, results, nil)
}

func runChannelStatusProbeTask(ctx context.Context, payload channelStatusProbeTaskPayload, taskID string) ([]channelStatusProbeResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	config, observations, err := readChannelStatusState()
	if err != nil {
		return nil, err
	}
	if !payload.Force && !config.Enabled {
		return []channelStatusProbeResult{}, nil
	}
	if !payload.Force {
		due := false
		for _, probe := range config.Probes {
			if probe.Enabled && channelStatusProbeDue(probe, observations, time.Now().Unix()) {
				due = true
				break
			}
		}
		if !due {
			return []channelStatusProbeResult{}, nil
		}
	}
	testUserID, err := resolveChannelTestUserID(nil)
	if err != nil {
		return nil, err
	}
	selected := make([]channelStatusProbe, 0, len(config.Probes))
	for _, probe := range config.Probes {
		if !probe.Enabled || payload.ProbeID != "" && payload.ProbeID != probe.ID {
			continue
		}
		if !payload.Force && !channelStatusProbeDue(probe, observations, time.Now().Unix()) {
			continue
		}
		selected = append(selected, probe)
	}
	runCtx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	completed := make(chan channelStatusObservation, 64)
	running := map[string]bool{}
	initialPending := map[string]bool{}
	selectedIDs := map[string]bool{}
	latestResults := map[string]channelStatusProbeResult{}
	startProbes := func(probes []channelStatusProbe) error {
		if len(probes) == 0 {
			return nil
		}
		updates := make([]channelStatusObservation, 0, len(probes))
		for _, probe := range probes {
			observation := channelStatusObservation{Probe: probe, Result: channelStatusCurrentResult(probe, observations, time.Now().Unix()), TaskID: taskID}
			observation.Result.StartedAt = time.Now().Unix()
			observation.Result.Running = true
			updates = append(updates, observation)
		}
		if err := persistChannelStatusObservation(updates...); err != nil {
			return err
		}
		for _, probe := range probes {
			running[probe.ID] = true
			workers.Go(func() {
				result := executeChannelStatusProbe(runCtx, probe, testUserID)
				completed <- channelStatusObservation{Probe: probe, Result: result, TaskID: taskID}
			})
		}
		return nil
	}
	for _, probe := range selected {
		initialPending[probe.ID], selectedIDs[probe.ID] = true, true
	}
	if err := startProbes(selected); err != nil {
		return nil, err
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for len(running) > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case observation := <-completed:
			delete(running, observation.Probe.ID)
			delete(initialPending, observation.Probe.ID)
			if err := persistChannelStatusObservation(observation); err != nil {
				return nil, err
			}
			observations[observation.Probe.ID] = observation
			latestResults[observation.Probe.ID] = observation.Result
		case <-ticker.C:
			if len(initialPending) == 0 {
				continue
			}
			currentConfig, currentObservations, err := readChannelStatusState()
			if err != nil {
				return nil, err
			}
			if !currentConfig.Enabled {
				continue
			}
			observations = currentObservations
			due := make([]channelStatusProbe, 0, len(currentConfig.Probes))
			for _, probe := range currentConfig.Probes {
				if !probe.Enabled || running[probe.ID] || payload.ProbeID != "" && payload.ProbeID != probe.ID || !channelStatusProbeDue(probe, observations, time.Now().Unix()) {
					continue
				}
				due = append(due, probe)
				if !selectedIDs[probe.ID] {
					selected = append(selected, probe)
					selectedIDs[probe.ID] = true
				}
			}
			if err := startProbes(due); err != nil {
				return nil, err
			}
		}
	}
	results := make([]channelStatusProbeResult, 0, len(selected))
	for _, probe := range selected {
		if result, exists := latestResults[probe.ID]; exists {
			results = append(results, result)
		}
	}
	return results, nil
}

func persistChannelStatusObservation(updates ...channelStatusObservation) error {
	channelStatusResultsMu.Lock()
	defer channelStatusResultsMu.Unlock()
	config, observations, err := readChannelStatusState()
	if err != nil {
		return err
	}
	for _, observation := range updates {
		observations[observation.Probe.ID] = observation
	}
	retained := map[string]channelStatusObservation{}
	for _, probe := range config.Probes {
		if existing, ok := observations[probe.ID]; ok && existing.Probe == probe {
			retained[probe.ID] = existing
		}
	}
	data, err := common.Marshal(retained)
	if err != nil {
		return err
	}
	return model.UpdateOptionsBulk(map[string]string{channelStatusResultsOption: string(data)})
}

func executeChannelStatusProbe(parent context.Context, probe channelStatusProbe, userID int) (result channelStatusProbeResult) {
	start := time.Now()
	result = channelStatusProbeResult{ProbeID: probe.ID, Status: channelStatusOutage, StartedAt: start.Unix()}
	defer func() {
		result.CheckedAt = time.Now().Unix()
		result.ResponseTimeMS = time.Since(start).Milliseconds()
		if recovered := recover(); recovered != nil {
			result.Message = "探针执行失败"
			common.SysError(fmt.Sprintf("channel status probe %s panicked", probe.ID))
		}
	}()
	channel, err := model.GetChannelById(probe.ChannelID, true)
	if err != nil {
		result.Message = "探针渠道不存在"
		return result
	}
	if !slices.Contains(channel.GetModels(), probe.Model) {
		result.Message = "探针模型不属于所选渠道"
		return result
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(probe.TimeoutSeconds)*time.Second)
	defer cancel()
	test := testChannel(ctx, channel, userID, probe.Model, probe.EndpointType, probe.IsStream, channelProbeTestOptions{Prompt: probe.Prompt})
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		result.Message = "探针请求超时"
	case test.localErr != nil:
		result.Message = "探针请求失败"
	case test.newAPIError != nil:
		result.Message = "上游返回错误"
	default:
		result.Status = channelStatusOperational
	}
	return result
}
