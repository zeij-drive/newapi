package model

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupRegistrationInviteTest(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&RegistrationInvite{}))
	require.NoError(t, DB.Where("1 = 1").Delete(&RegistrationInvite{}).Error)
	oldSecret := common.ConfigEncryptionSecret
	oldInviteRequired := common.RegistrationInviteRequired
	common.ConfigEncryptionSecret = "registration-invite-test-secret"
	t.Cleanup(func() {
		common.ConfigEncryptionSecret = oldSecret
		common.RegistrationInviteRequired = oldInviteRequired
		_ = DB.Where("1 = 1").Delete(&RegistrationInvite{}).Error
	})
}

func TestRegistrationInviteClaimIsIdempotentAndUnique(t *testing.T) {
	setupRegistrationInviteTest(t)
	codes, err := CreateRegistrationInvites(2, 1, 0)
	require.NoError(t, err)
	require.Len(t, codes, 2)
	assert.NotEqual(t, codes[0], codes[1])
	assert.NoError(t, VerifyRegistrationInvite(strings.ToLower(codes[0])))

	firstClaim, err := ClaimRegistrationInviteForQQ("qq-openid-a")
	require.NoError(t, err)
	assert.Contains(t, codes, firstClaim)
	duplicateClaim, err := ClaimRegistrationInviteForQQ("qq-openid-a")
	require.NoError(t, err)
	assert.Equal(t, firstClaim, duplicateClaim)

	secondClaim, err := ClaimRegistrationInviteForQQ("qq-openid-b")
	require.NoError(t, err)
	assert.NotEqual(t, firstClaim, secondClaim)
	autoClaim, err := ClaimRegistrationInviteForQQ("qq-openid-c")
	require.NoError(t, err)
	assert.NotEqual(t, firstClaim, autoClaim)
	assert.NotEqual(t, secondClaim, autoClaim)
	require.NoError(t, VerifyRegistrationInvite(autoClaim))
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return ConsumeRegistrationInviteWithTx(tx, strings.ToLower(autoClaim), 9)
	}))
	require.ErrorIs(t, VerifyRegistrationInvite(autoClaim), ErrRegistrationInviteUsed)
}

func TestRegistrationInviteConsumeIsSingleUseAndRollsBackWithTransaction(t *testing.T) {
	setupRegistrationInviteTest(t)
	codes, err := CreateRegistrationInvites(1, 1, 0)
	require.NoError(t, err)

	err = DB.Transaction(func(tx *gorm.DB) error {
		return ConsumeRegistrationInviteWithTx(tx, codes[0], 7)
	})
	require.NoError(t, err)
	require.ErrorIs(t, VerifyRegistrationInvite(codes[0]), ErrRegistrationInviteUsed)

	err = DB.Transaction(func(tx *gorm.DB) error {
		if createErr := tx.Create(&RegistrationInvite{
			CodeHash:      "transaction-rollback-check",
			EncryptedCode: "ciphertext",
			CodePrefix:    "SAPI-ROLLBA",
			Status:        RegistrationInviteStatusEnabled,
		}).Error; createErr != nil {
			return createErr
		}
		return ConsumeRegistrationInviteWithTx(tx, codes[0], 8)
	})
	require.ErrorIs(t, err, ErrRegistrationInviteUsed)
	var rollbackCount int64
	require.NoError(t, DB.Model(&RegistrationInvite{}).Where("code_hash = ?", "transaction-rollback-check").Count(&rollbackCount).Error)
	assert.Zero(t, rollbackCount)
}

func TestRegistrationInviteCodeIsNotSerializedInList(t *testing.T) {
	setupRegistrationInviteTest(t)
	codes, err := CreateRegistrationInvites(1, 1, 0)
	require.NoError(t, err)
	invites, _, err := ListRegistrationInvites(1, 10)
	require.NoError(t, err)
	require.Len(t, invites, 1)
	serialized, err := common.Marshal(invites[0])
	require.NoError(t, err)
	assert.NotContains(t, string(serialized), codes[0])
	assert.NotContains(t, string(serialized), invites[0].EncryptedCode)
	assert.NotContains(t, string(serialized), invites[0].CodeHash)
}

func TestOAuthRegistrationConsumesInviteInsideUserTransaction(t *testing.T) {
	setupRegistrationInviteTest(t)
	common.RegistrationInviteRequired = true
	codes, err := CreateRegistrationInvites(1, 1, 0)
	require.NoError(t, err)

	user := &User{
		Username:    "registration-invite-oauth-user",
		DisplayName: "OAuth user",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		return user.InsertWithTxAndInviteHash(tx, 0, HashRegistrationInviteCode(codes[0]))
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = DB.Unscoped().Delete(&User{}, user.Id).Error })
	require.ErrorIs(t, VerifyRegistrationInvite(codes[0]), ErrRegistrationInviteUsed)

	failedUser := &User{
		Username:    "registration-invite-oauth-no-code",
		DisplayName: "OAuth user",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		return failedUser.InsertWithTxAndInviteHash(tx, 0, "")
	})
	require.ErrorIs(t, err, ErrRegistrationInviteRequired)
	var userCount int64
	require.NoError(t, DB.Model(&User{}).Where("username = ?", failedUser.Username).Count(&userCount).Error)
	assert.Zero(t, userCount)
}
