package service

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func rankingTime(year int, month time.Month, day, hour int) time.Time {
	return time.Date(year, month, day, hour, 0, 0, 0, operation_setting.UsageRankingLocation)
}

func TestUsageRankingBounds(t *testing.T) {
	at := rankingTime(2026, time.January, 1, 1)
	for _, tc := range []struct {
		period string
		start  time.Time
		end    time.Time
	}{
		{"today", rankingTime(2026, time.January, 1, 0), rankingTime(2026, time.January, 2, 0)},
		{"week", rankingTime(2025, time.December, 29, 0), rankingTime(2026, time.January, 5, 0)},
		{"month", rankingTime(2026, time.January, 1, 0), rankingTime(2026, time.February, 1, 0)},
		{"year", rankingTime(2026, time.January, 1, 0), rankingTime(2027, time.January, 1, 0)},
	} {
		t.Run(tc.period, func(t *testing.T) {
			start, end, err := usageRankingBounds(tc.period, at)
			require.NoError(t, err)
			assert.Equal(t, tc.start, start)
			assert.Equal(t, tc.end, end)
		})
	}
	_, _, err := usageRankingBounds("invalid", at)
	require.Error(t, err)
}

func TestUsageRankingCatchUpAndAwardIdempotency(t *testing.T) {
	db := model.DB
	require.NoError(t, db.AutoMigrate(&model.QuotaData{}, &model.UsageRankingAward{}, &model.UsageRankingSettlement{}))
	t.Cleanup(func() {
		db.Exec("DELETE FROM usage_ranking_settlements")
		db.Exec("DELETE FROM usage_ranking_awards")
		db.Exec("DELETE FROM quota_data")
		db.Exec("DELETE FROM users")
		db.Exec("DELETE FROM logs")
	})

	users := []model.User{
		{Username: "ranking-a", DisplayName: "A", Quota: 100},
		{Username: "ranking-b", DisplayName: "B", Quota: 100},
	}
	for i := range users {
		require.NoError(t, db.Create(&users[i]).Error)
	}
	for _, usage := range []model.QuotaData{
		{UserID: users[0].Id, CreatedAt: rankingTime(2026, time.October, 6, 12).Unix(), TokenUsed: 10},
		{UserID: users[1].Id, CreatedAt: rankingTime(2026, time.October, 6, 12).Unix(), TokenUsed: 10},
		{UserID: users[1].Id, CreatedAt: rankingTime(2026, time.October, 7, 12).Unix(), TokenUsed: 20},
	} {
		require.NoError(t, db.Create(&usage).Error)
	}
	first, err := GetUsageRanking("today", rankingTime(2026, time.October, 6, 13))
	require.NoError(t, err)
	assert.Equal(t, int64(20), first.TotalTokens)
	assert.Equal(t, users[0].Id, first.Users[0].UserID)
	assert.Equal(t, users[1].Id, first.Users[1].UserID)

	key := operation_setting.UsageRankingRewardsOptionKey
	encoded, err := common.Marshal(operation_setting.UsageRankingRewards{
		Enabled: true, EnabledAt: rankingTime(2026, time.October, 5, 12).Unix(),
		Daily: [3]string{"1.00", "0.50", ""},
	})
	require.NoError(t, err)
	common.OptionMapRWMutex.Lock()
	previousMap := common.OptionMap
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	previous, existed := common.OptionMap[key]
	common.OptionMap[key] = string(encoded)
	common.OptionMapRWMutex.Unlock()
	previousExport := common.DataExportEnabled
	common.DataExportEnabled = true
	t.Cleanup(func() {
		common.DataExportEnabled = previousExport
		common.OptionMapRWMutex.Lock()
		if previousMap == nil {
			common.OptionMap = nil
		} else if existed {
			common.OptionMap[key] = previous
		} else {
			delete(common.OptionMap, key)
		}
		common.OptionMapRWMutex.Unlock()
	})

	now := rankingTime(2026, time.October, 8, 1)
	require.NoError(t, settleUsageRankingRewards(now))
	require.NoError(t, settleUsageRankingRewards(now))
	var awards []model.UsageRankingAward
	require.NoError(t, db.Order("period_start, ranking_position").Find(&awards).Error)
	require.Len(t, awards, 3)
	assert.Equal(t, users[0].Id, awards[0].UserID)
	assert.Equal(t, users[1].Id, awards[1].UserID)
	assert.Equal(t, users[1].Id, awards[2].UserID)
	var settlements []model.UsageRankingSettlement
	require.NoError(t, db.Where("period = ?", "today").Find(&settlements).Error)
	assert.Len(t, settlements, 2)

	firstQuota, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromFloat(common.QuotaPerUnit))
	require.NoError(t, err)
	secondQuota, err := common.WalletQuotaFromDecimalStrict(decimal.NewFromFloat(common.QuotaPerUnit).Div(decimal.NewFromInt(2)))
	require.NoError(t, err)
	var updated model.User
	require.NoError(t, db.First(&updated, users[1].Id).Error)
	assert.Equal(t, 100+firstQuota+secondQuota, updated.Quota)

	entry := model.UsageRankingEntry{UserID: users[0].Id, TotalTokens: 10}
	awarded, err := model.AwardUsageRanking("today", rankingTime(2026, time.October, 6, 0).Unix(), 1, entry, firstQuota, now.Unix())
	require.NoError(t, err)
	assert.False(t, awarded)
	_, err = model.AwardUsageRanking("today", now.Unix(), 1, entry, common.MaxWalletQuota, now.Unix())
	require.ErrorIs(t, err, model.ErrWalletQuotaLimitExceeded)
}
