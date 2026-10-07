package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UsageRankingEntry struct {
	UserID      int    `json:"user_id"`
	Name        string `json:"name"`
	TotalTokens int64  `json:"total_tokens"`
}

type UsageRankingAward struct {
	ID          int64  `gorm:"primaryKey"`
	Period      string `gorm:"type:varchar(8);uniqueIndex:idx_usage_rank_award,priority:1"`
	PeriodStart int64  `gorm:"uniqueIndex:idx_usage_rank_award,priority:2"`
	Rank        int    `gorm:"column:ranking_position;uniqueIndex:idx_usage_rank_award,priority:3"`
	UserID      int
	TotalTokens int64
	Quota       int
	CreatedAt   int64
}

type UsageRankingSettlement struct {
	ID          int64  `gorm:"primaryKey"`
	Period      string `gorm:"type:varchar(8);uniqueIndex:idx_usage_rank_settlement,priority:1"`
	PeriodStart int64  `gorm:"uniqueIndex:idx_usage_rank_settlement,priority:2"`
	CreatedAt   int64
}

func LastUsageRankingSettlement(period string) (int64, error) {
	var row UsageRankingSettlement
	err := DB.Where("period = ?", period).Order("period_start DESC").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, nil
	}
	return row.PeriodStart, err
}

func CompleteUsageRankingSettlement(period string, start, now int64) error {
	return DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&UsageRankingSettlement{
		Period: period, PeriodStart: start, CreatedAt: now,
	}).Error
}

// QueryUsageRanking uses the same hourly usage facts as the existing model ranking.
func QueryUsageRanking(start, end int64, limit int) ([]UsageRankingEntry, int64, error) {
	base := DB.Table("quota_data").Where("created_at >= ? AND created_at < ? AND user_id > 0 AND token_used > 0", start, end)
	var total struct{ TotalTokens int64 }
	if err := base.Select("COALESCE(SUM(token_used), 0) AS total_tokens").Scan(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []struct {
		UserID      int
		TotalTokens int64
	}
	query := DB.Table("quota_data").Joins("JOIN users ON users.id = quota_data.user_id").
		Where("quota_data.created_at >= ? AND quota_data.created_at < ? AND quota_data.token_used > 0", start, end).
		Select("quota_data.user_id, SUM(quota_data.token_used) AS total_tokens").
		Group("quota_data.user_id").Order("total_tokens DESC, quota_data.user_id ASC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Scan(&rows).Error; err != nil {
		return nil, 0, err
	}
	ids := make([]int, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.UserID)
	}
	var users []User
	if len(ids) > 0 {
		if err := DB.Select("id", "display_name").Where("id IN ?", ids).Find(&users).Error; err != nil {
			return nil, 0, err
		}
	}
	userByID := make(map[int]User, len(users))
	for _, user := range users {
		userByID[user.Id] = user
	}
	entries := make([]UsageRankingEntry, 0, len(rows))
	for _, row := range rows {
		user, ok := userByID[row.UserID]
		if !ok {
			continue
		}
		name := user.DisplayName
		if name == "" {
			name = fmt.Sprintf("User #%d", user.Id)
		}
		entries = append(entries, UsageRankingEntry{UserID: row.UserID, Name: name, TotalTokens: row.TotalTokens})
	}
	return entries, total.TotalTokens, nil
}

// AwardUsageRanking atomically records and credits a single period and rank.
func AwardUsageRanking(period string, start int64, rank int, entry UsageRankingEntry, quota int, now int64) (bool, error) {
	if quota <= 0 || quota > common.MaxWalletQuota || rank < 1 || rank > 3 || entry.UserID <= 0 || entry.TotalTokens <= 0 {
		return false, ErrWalletQuotaLimitExceeded
	}
	awarded := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		record := UsageRankingAward{Period: period, PeriodStart: start, Rank: rank, UserID: entry.UserID, TotalTokens: entry.TotalTokens, Quota: quota, CreatedAt: now}
		if err := tx.Create(&record).Error; err != nil {
			return err
		}
		result := tx.Model(&User{}).Where("id = ? AND quota <= ?", entry.UserID, common.MaxWalletQuota-quota).
			Update("quota", gorm.Expr("quota + ?", quota))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrWalletQuotaLimitExceeded
		}
		awarded = true
		return nil
	})
	if err != nil {
		var existing UsageRankingAward
		lookupErr := DB.Where("period = ? AND period_start = ? AND ranking_position = ?", period, start, rank).First(&existing).Error
		if lookupErr == nil {
			return false, nil
		}
		return false, err
	}
	if !awarded {
		return false, nil
	}
	syncCreditUserQuotaCache(entry.UserID, quota, "usage ranking reward")
	return true, nil
}
