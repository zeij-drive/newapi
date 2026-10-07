package operation_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEpayGatewaysRoundTripEncryptsKeysAndHidesThemFromViews(t *testing.T) {
	previousSecret := common.CryptoSecret
	common.CryptoSecret = "epay-gateway-test-secret"
	t.Cleanup(func() { common.CryptoSecret = previousSecret })

	gateways := []EpayGateway{
		{
			ID:         "primary",
			Name:       "Primary",
			Address:    "https://primary.example.com/",
			MerchantID: "merchant-primary",
			Key:        "primary-secret",
			Enabled:    true,
		},
		{
			ID:         "backup",
			Name:       "Backup",
			Address:    "https://backup.example.com",
			MerchantID: "merchant-backup",
			Key:        "backup-secret",
			Enabled:    false,
		},
	}

	encoded, err := EpayGateways2JsonString(gateways)
	require.NoError(t, err)
	assert.NotContains(t, encoded, "primary-secret")
	assert.NotContains(t, encoded, "backup-secret")
	assert.Contains(t, encoded, "v1.")

	parsed, err := ParseEpayGateways(encoded)
	require.NoError(t, err)
	require.Len(t, parsed, 2)
	assert.Equal(t, "primary-secret", parsed[0].Key)
	assert.Equal(t, "backup-secret", parsed[1].Key)
	assert.Equal(t, "https://primary.example.com/", parsed[0].Address)

	views := EpayGatewayViews(parsed)
	viewJSON, err := common.Marshal(views)
	require.NoError(t, err)
	assert.NotContains(t, string(viewJSON), "primary-secret")
	assert.NotContains(t, string(viewJSON), "backup-secret")
	assert.True(t, views[0].KeySet)
	assert.True(t, views[1].KeySet)

	serializedParsed, err := common.Marshal(parsed)
	require.NoError(t, err)
	assert.NotContains(t, string(serializedParsed), "primary-secret")
	assert.NotContains(t, string(serializedParsed), "backup-secret")
}

func TestEpayGatewaysValidationPreservesExistingEncryptedKey(t *testing.T) {
	gateways := []EpayGateway{
		{
			ID:           "primary",
			Name:         " Primary ",
			Address:      " https://primary.example.com/// ",
			MerchantID:   " merchant ",
			EncryptedKey: "v1.persisted-ciphertext",
		},
	}

	require.NoError(t, ValidateEpayGateways(gateways))
	assert.Equal(t, "Primary", gateways[0].Name)
	assert.Equal(t, "https://primary.example.com", gateways[0].Address)
	assert.Equal(t, "merchant", gateways[0].MerchantID)
	assert.Equal(t, "v1.persisted-ciphertext", gateways[0].EncryptedKey)
}

func TestEpayGatewaysValidationRejectsDuplicateIDsAndMissingKeys(t *testing.T) {
	base := EpayGateway{ID: "same", Name: "Gateway", Address: "https://example.com", MerchantID: "merchant", Key: "secret"}

	err := ValidateEpayGateways([]EpayGateway{base, base})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ID")

	err = ValidateEpayGateways([]EpayGateway{{ID: "missing-key", Name: "Gateway", Address: "https://example.com", MerchantID: "merchant"}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "密钥")
}
