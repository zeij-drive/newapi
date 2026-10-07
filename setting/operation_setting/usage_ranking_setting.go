package operation_setting

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/shopspring/decimal"
)

const UsageRankingRewardsOptionKey = "UsageRankingRewards"

var UsageRankingLocation = time.FixedZone("UTC+8", 8*60*60)

type UsageRankingRewards struct {
	Enabled   bool      `json:"enabled"`
	EnabledAt int64     `json:"enabled_at"`
	Daily     [3]string `json:"daily"`
	Weekly    [3]string `json:"weekly"`
	Monthly   [3]string `json:"monthly"`
	Yearly    [3]string `json:"yearly"`
}

func (s UsageRankingRewards) Amounts(period string) [3]string {
	switch period {
	case "today":
		return s.Daily
	case "week":
		return s.Weekly
	case "month":
		return s.Monthly
	case "year":
		return s.Yearly
	default:
		return [3]string{}
	}
}

func (s UsageRankingRewards) Validate() error {
	if s.Enabled && s.EnabledAt <= 0 {
		return fmt.Errorf("ranking rewards require an activation time")
	}
	for _, period := range []string{"today", "week", "month", "year"} {
		for rank, amount := range s.Amounts(period) {
			if strings.TrimSpace(amount) == "" {
				continue
			}
			value, err := decimal.NewFromString(strings.TrimSpace(amount))
			if err != nil || value.IsNegative() || value.Exponent() < -2 {
				return fmt.Errorf("invalid %s reward for rank %d", period, rank+1)
			}
			quota, err := common.WalletQuotaFromDecimalStrict(value.Mul(decimal.NewFromFloat(common.QuotaPerUnit)))
			if err != nil || quota < 0 {
				return fmt.Errorf("reward exceeds wallet limit for %s rank %d", period, rank+1)
			}
		}
	}
	return nil
}

func GetUsageRankingRewards() (UsageRankingRewards, error) {
	var setting UsageRankingRewards
	common.OptionMapRWMutex.RLock()
	raw := common.OptionMap[UsageRankingRewardsOptionKey]
	common.OptionMapRWMutex.RUnlock()
	if raw == "" {
		return setting, nil
	}
	if err := common.UnmarshalJsonStr(raw, &setting); err != nil {
		return UsageRankingRewards{}, err
	}
	return setting, setting.Validate()
}
