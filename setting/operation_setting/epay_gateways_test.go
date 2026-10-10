package operation_setting

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEpayGatewaysRoundTripEncryptsKeysAndHidesThemFromViews(t *testing.T) {
	previousSecret := common.ConfigEncryptionSecret
	common.ConfigEncryptionSecret = "epay-gateway-test-secret"
	t.Cleanup(func() { common.ConfigEncryptionSecret = previousSecret })

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
	assert.Contains(t, encoded, "v2.")

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

func TestEpayGatewaysRejectUnsafeCheckoutURLs(t *testing.T) {
	for _, address := range []string{"javascript:alert(1)", "//example.com", "https://user:secret@example.com", "https://example.com?key=secret", "https://example.com#fragment"} {
		t.Run(address, func(t *testing.T) {
			require.Error(t, ValidateEpayGateways([]EpayGateway{{ID: "primary", Name: "Gateway", Address: address, MerchantID: "merchant", Key: "secret"}}))
		})
	}
}

func TestEpayGatewaysPreserveUnavailableKeysAndLoadOtherGateways(t *testing.T) {
	previousSecret := common.ConfigEncryptionSecret
	common.ConfigEncryptionSecret = "epay-recovery-test-secret"
	t.Cleanup(func() { common.ConfigEncryptionSecret = previousSecret })
	valid, err := common.EncryptWithCryptoSecret("working-merchant-secret")
	require.NoError(t, err)
	var stored []map[string]any
	for index, key := range []string{"v1.unavailable", valid, "v3.unsupported", "v2.AA"} {
		stored = append(stored, map[string]any{"id": fmt.Sprintf("gateway-%d", index), "name": "Gateway", "address": "https://pay.example.com", "merchant_id": "merchant", "key": key, "enabled": true})
	}
	encoded, err := common.Marshal(stored)
	require.NoError(t, err)
	parsed, err := ParseEpayGateways(string(encoded))
	require.Error(t, err)
	require.Len(t, parsed, 4)
	assert.Equal(t, "working-merchant-secret", parsed[1].Key)
	for _, index := range []int{0, 2, 3} {
		assert.Empty(t, parsed[index].Key)
		assert.False(t, EpayGatewayViews(parsed)[index].KeySet)
		assert.Nil(t, FindEpayGateway(parsed, parsed[index].ID))
		assert.Equal(t, stored[index]["key"], parsed[index].EncryptedKey)
	}
	assert.NotNil(t, FindEpayGateway(parsed, parsed[1].ID))
	views, err := common.Marshal(EpayGatewayViews(parsed))
	require.NoError(t, err)
	assert.NotContains(t, string(views), "working-merchant-secret")
	assert.NotContains(t, string(views), `"key":`)
}

func TestEpayGatewaysRequireInitializedEncryptionBeforeSavingSecrets(t *testing.T) {
	previousSecret := common.ConfigEncryptionSecret
	common.ConfigEncryptionSecret = ""
	t.Cleanup(func() { common.ConfigEncryptionSecret = previousSecret })
	encoded, err := EpayGateways2JsonString([]EpayGateway{{Key: "merchant-secret"}})
	require.Error(t, err)
	assert.Empty(t, encoded)
}
