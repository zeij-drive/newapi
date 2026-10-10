package model

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// JailbreakPolicy always uses a group whitelist: all other groups are inspected.
// The detector references an existing channel so credentials never enter options.
type JailbreakPolicy struct {
	Enabled       bool
	AllowedGroups []string
	ChannelID     int
	Model         string
	BanThreshold  int
	Replies       []string
}

func buildJailbreakPolicy(raw map[string]string) (JailbreakPolicy, error) {
	var policy JailbreakPolicy
	var err error
	policy.Enabled, err = strconv.ParseBool(raw["JailbreakEnabled"])
	if err != nil {
		return policy, fmt.Errorf("invalid jailbreak enabled value")
	}
	policy.ChannelID, err = strconv.Atoi(raw["JailbreakChannelId"])
	if err != nil || policy.ChannelID < 0 || (policy.Enabled && policy.ChannelID == 0) {
		return policy, fmt.Errorf("select a jailbreak detection channel")
	}
	policy.Model = strings.TrimSpace(raw["JailbreakModel"])
	if policy.Model == "" || len(policy.Model) > 200 {
		return policy, fmt.Errorf("jailbreak detection model is required (maximum 200 characters)")
	}
	policy.BanThreshold, err = strconv.Atoi(raw["JailbreakBanThreshold"])
	if err != nil || policy.BanThreshold < 1 || policy.BanThreshold > 100 {
		return policy, fmt.Errorf("jailbreak ban threshold must be an integer from 1 to 100")
	}
	if err := common.UnmarshalJsonStr(raw["JailbreakAllowedGroups"], &policy.AllowedGroups); err != nil || policy.AllowedGroups == nil || len(policy.AllowedGroups) > 100 {
		return policy, fmt.Errorf("jailbreak allowed groups must be a JSON array (maximum 100 groups)")
	}
	for _, group := range policy.AllowedGroups {
		if group == "" || strings.TrimSpace(group) != group || len(group) > 64 || group == "auto" {
			return policy, fmt.Errorf("invalid jailbreak whitelist group")
		}
	}
	if err := common.UnmarshalJsonStr(raw["JailbreakReplies"], &policy.Replies); err != nil || len(policy.Replies) != policy.BanThreshold {
		return policy, fmt.Errorf("provide one jailbreak reply per interception up to the ban threshold")
	}
	for _, reply := range policy.Replies {
		if strings.TrimSpace(reply) == "" || len([]rune(reply)) > 2000 {
			return policy, fmt.Errorf("each jailbreak reply must contain 1 to 2000 characters")
		}
	}
	return policy, nil
}

// RecordJailbreakInterception serializes increments on the user row. The first
// write also acquires SQLite's writer lock before any read, avoiding lock upgrades.
// Status, auth version and session revocation commit together at the threshold.
func RecordJailbreakInterception(userID, threshold int) (count int, banned bool, err error) {
	if userID <= 0 || threshold < 1 || threshold > 100 {
		return 0, false, fmt.Errorf("invalid jailbreak interception")
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&User{}).Where("id = ? AND status = ? AND jailbreak_count < ?", userID, common.UserStatusEnabled, threshold).
			UpdateColumn("jailbreak_count", gorm.Expr("jailbreak_count + ?", 1))
		if result.Error != nil {
			return result.Error
		}
		var user User
		if err := lockForUpdate(tx).First(&user, userID).Error; err != nil {
			return err
		}
		count = user.JailbreakCount
		if user.Status != common.UserStatusEnabled || count < threshold || user.Role >= common.RoleRootUser {
			return nil
		}
		if _, err := IncrementUserAuthVersionWithTx(tx, userID); err != nil {
			return err
		}
		if err := tx.Model(&user).Update("status", common.UserStatusDisabled).Error; err != nil {
			return err
		}
		if err := tx.Model(&UserSession{}).Where("user_id = ? AND status = ?", userID, UserSessionStatusActive).
			Updates(map[string]any{"status": UserSessionStatusRevoked, "revoked_at": common.GetTimestamp(), "revoked_reason": "jailbreak_threshold"}).Error; err != nil {
			return err
		}
		banned = true
		return nil
	})
	if err == nil && banned {
		err = PublishUserAuthCache(userID)
	}
	return count, banned, err
}
