package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
)

type UsageRankingSnapshot struct {
	Period      string                     `json:"period"`
	Start       int64                      `json:"start"`
	End         int64                      `json:"end"`
	TotalTokens int64                      `json:"total_tokens"`
	Users       []model.UsageRankingEntry  `json:"users"`
	Reward      *UsageRankingRewardPreview `json:"reward,omitempty"`
}

type UsageRankingRewardRecipient struct {
	UserID int    `json:"user_id"`
	Name   string `json:"name"`
	Rank   int    `json:"rank"`
	Quota  int    `json:"quota"`
}

type UsageRankingRewardPreview struct {
	Configured   bool                          `json:"configured"`
	Eligible     bool                          `json:"eligible"`
	SettlementAt int64                         `json:"settlement_at"`
	PeriodStart  int64                         `json:"period_start"`
	PeriodEnd    int64                         `json:"period_end"`
	Recipients   []UsageRankingRewardRecipient `json:"recipients"`
}

func usageRankingBounds(period string, at time.Time) (time.Time, time.Time, error) {
	local := at.In(operation_setting.UsageRankingLocation)
	y, m, d := local.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, local.Location())
	switch period {
	case "today":
		return day, day.AddDate(0, 0, 1), nil
	case "week":
		start := day.AddDate(0, 0, -(int(local.Weekday())+6)%7)
		return start, start.AddDate(0, 0, 7), nil
	case "month":
		start := time.Date(y, m, 1, 0, 0, 0, 0, local.Location())
		return start, start.AddDate(0, 1, 0), nil
	case "year":
		start := time.Date(y, 1, 1, 0, 0, 0, 0, local.Location())
		return start, start.AddDate(1, 0, 0), nil
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("invalid ranking period: %s", period)
	}
}

func GetUsageRanking(period string, now time.Time) (*UsageRankingSnapshot, error) {
	start, end := int64(0), now.Unix()+1
	if period != "all" {
		from, to, err := usageRankingBounds(period, now)
		if err != nil {
			return nil, err
		}
		start, end = from.Unix(), to.Unix()
	}
	users, total, err := model.QueryUsageRanking(start, end, 20)
	if err != nil {
		return nil, err
	}
	reward, err := getUsageRankingRewardPreview(period, start, end)
	if err != nil {
		logger.LogWarn(context.Background(), fmt.Sprintf("usage ranking reward preview failed: %v", err))
	}
	return &UsageRankingSnapshot{Period: period, Start: start, End: end, TotalTokens: total, Users: users, Reward: reward}, nil
}

func firstUsageRankingRewardWindow(period string, setting operation_setting.UsageRankingRewards) (time.Time, time.Time, error) {
	// 首个奖励周期从启用日零点开始，结束时间仍沿用日、周、月、年的自然边界。
	enabledAt := time.Unix(setting.EnabledAt, 0).In(operation_setting.UsageRankingLocation)
	start, end, err := usageRankingBounds(period, enabledAt)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	activationDay := time.Date(enabledAt.Year(), enabledAt.Month(), enabledAt.Day(), 0, 0, 0, 0, enabledAt.Location())
	if activationDay.After(start) {
		start = activationDay
	}
	return start, end, nil
}

func nextUsageRankingRewardWindow(period string, setting operation_setting.UsageRankingRewards, lastStart int64) (time.Time, time.Time, error) {
	start, end, err := firstUsageRankingRewardWindow(period, setting)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	if lastStart >= start.Unix() {
		_, lastEnd, boundsErr := usageRankingBounds(period, time.Unix(lastStart, 0))
		if boundsErr != nil {
			return time.Time{}, time.Time{}, boundsErr
		}
		start, end, err = usageRankingBounds(period, lastEnd)
		if err != nil {
			return time.Time{}, time.Time{}, err
		}
	}
	return start, end, nil
}

func usageRankingRewardQuota(amount string) (int, bool, error) {
	trimmed := strings.TrimSpace(amount)
	if trimmed == "" {
		return 0, false, nil
	}
	value, err := decimal.NewFromString(trimmed)
	if err != nil {
		return 0, false, err
	}
	if !value.IsPositive() {
		return 0, false, nil
	}
	quota, err := common.WalletQuotaFromDecimalStrict(value.Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
	if err != nil {
		return 0, false, err
	}
	if quota <= 0 {
		return 0, false, fmt.Errorf("amount converts to zero quota")
	}
	return quota, true, nil
}

func buildUsageRankingRewardPreview(period string, setting operation_setting.UsageRankingRewards, start, end time.Time, eligible bool, users []model.UsageRankingEntry) (*UsageRankingRewardPreview, error) {
	amounts := setting.Amounts(period)
	quotas := [3]int{}
	configured := false
	for index, amount := range amounts {
		quota, shouldReward, err := usageRankingRewardQuota(amount)
		if err != nil {
			return nil, fmt.Errorf("invalid %s ranking reward: %w", period, err)
		}
		if shouldReward {
			configured = true
			quotas[index] = quota
		}
	}
	recipients := make([]UsageRankingRewardRecipient, 0, min(len(users), 3))
	for index, user := range users {
		if index >= 3 {
			break
		}
		if quotas[index] == 0 {
			continue
		}
		recipients = append(recipients, UsageRankingRewardRecipient{
			UserID: user.UserID, Name: user.Name, Rank: index + 1, Quota: quotas[index],
		})
	}
	settlementAt := end.Add(time.Duration(max(common.DataExportInterval, 10)) * time.Minute)
	return &UsageRankingRewardPreview{
		Configured:   configured,
		Eligible:     eligible,
		SettlementAt: settlementAt.Unix(),
		PeriodStart:  start.Unix(),
		PeriodEnd:    end.Unix(),
		Recipients:   recipients,
	}, nil
}

func getUsageRankingRewardPreview(period string, start, end int64) (*UsageRankingRewardPreview, error) {
	// 即使当前周期尚未配置金额，也返回状态供前端显示“暂未开启该周期的奖励”。
	if period == "all" {
		return nil, nil
	}
	setting, err := operation_setting.GetUsageRankingRewards()
	if err != nil {
		return nil, err
	}
	if !setting.Enabled || !common.DataExportEnabled {
		return &UsageRankingRewardPreview{Recipients: []UsageRankingRewardRecipient{}}, nil
	}
	firstStart, firstEnd, err := firstUsageRankingRewardWindow(period, setting)
	if err != nil {
		return nil, err
	}
	previewStart, previewEnd := time.Unix(start, 0), time.Unix(end, 0)
	eligible := end > firstStart.Unix()
	if !eligible {
		previewStart, previewEnd = firstStart, firstEnd
	} else if previewStart.Before(firstStart) {
		previewStart = firstStart
	}
	users := make([]model.UsageRankingEntry, 0)
	if eligible {
		users, _, err = model.QueryUsageRanking(previewStart.Unix(), previewEnd.Unix(), 3)
		if err != nil {
			return nil, err
		}
	}
	return buildUsageRankingRewardPreview(period, setting, previewStart, previewEnd, eligible, users)
}

var usageRankingRewardOnce sync.Once

func StartUsageRankingRewardTask() {
	usageRankingRewardOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		go func() {
			ticker := time.NewTicker(time.Minute)
			defer ticker.Stop()
			for {
				if err := settleUsageRankingRewards(time.Now()); err != nil {
					logger.LogWarn(context.Background(), fmt.Sprintf("usage ranking reward settlement failed: %v", err))
				}
				<-ticker.C
			}
		}()
	})
}

func settleUsageRankingRewards(now time.Time) error {
	setting, err := operation_setting.GetUsageRankingRewards()
	if err != nil || !setting.Enabled || !common.DataExportEnabled {
		return err
	}
	for _, period := range []string{"today", "week", "month", "year"} {
		lastStart, err := model.LastUsageRankingSettlement(period)
		if err != nil {
			return err
		}
		start, end, err := nextUsageRankingRewardWindow(period, setting, lastStart)
		if err != nil {
			return err
		}
		amounts := setting.Amounts(period)
		// Usage facts are flushed periodically and grouped by their starting hour.
		settlementDelay := time.Duration(max(common.DataExportInterval, 10)) * time.Minute
		for !now.Before(end.Add(settlementDelay)) {
			users, _, err := model.QueryUsageRanking(start.Unix(), end.Unix(), 3)
			if err != nil {
				return err
			}
			for index, user := range users {
				quota, shouldReward, err := usageRankingRewardQuota(amounts[index])
				if err != nil {
					return fmt.Errorf("invalid %s ranking reward: %w", period, err)
				}
				if !shouldReward {
					continue
				}
				awarded, err := model.AwardUsageRanking(period, start.Unix(), index+1, user, quota, now.Unix())
				if err != nil {
					return err
				}
				if awarded {
					amount, _ := decimal.NewFromString(strings.TrimSpace(amounts[index]))
					model.RecordLog(user.UserID, model.LogTypeSystem, fmt.Sprintf("%s token usage ranking #%d reward: %s USD", period, index+1, amount.StringFixed(2)))
				}
			}
			if err := model.CompleteUsageRankingSettlement(period, start.Unix(), now.Unix()); err != nil {
				return err
			}
			start, end, err = usageRankingBounds(period, end)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
