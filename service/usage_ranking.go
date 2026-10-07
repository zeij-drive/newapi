package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
)

type UsageRankingSnapshot struct {
	Period      string                    `json:"period"`
	Start       int64                     `json:"start"`
	End         int64                     `json:"end"`
	TotalTokens int64                     `json:"total_tokens"`
	Users       []model.UsageRankingEntry `json:"users"`
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
	return &UsageRankingSnapshot{Period: period, Start: start, End: end, TotalTokens: total, Users: users}, nil
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
		start, end, err := usageRankingBounds(period, time.Unix(setting.EnabledAt, 0))
		if err != nil {
			return err
		}
		if start.Unix() < setting.EnabledAt {
			start, end, err = usageRankingBounds(period, end)
			if err != nil {
				return err
			}
		}
		if lastStart >= start.Unix() {
			_, lastEnd, boundsErr := usageRankingBounds(period, time.Unix(lastStart, 0))
			if boundsErr != nil {
				return boundsErr
			}
			start, end, err = usageRankingBounds(period, lastEnd)
			if err != nil {
				return err
			}
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
				amount, err := decimal.NewFromString(amounts[index])
				if err != nil || !amount.IsPositive() {
					continue
				}
				quota, err := common.WalletQuotaFromDecimalStrict(amount.Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
				if err != nil {
					return fmt.Errorf("invalid %s ranking reward: %w", period, err)
				}
				if quota <= 0 {
					return fmt.Errorf("invalid %s ranking reward: amount converts to zero quota", period)
				}
				awarded, err := model.AwardUsageRanking(period, start.Unix(), index+1, user, quota, now.Unix())
				if err != nil {
					return err
				}
				if awarded {
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
