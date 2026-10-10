package model

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func legacyEpayCiphertext(t *testing.T, secret, plaintext string) string {
	t.Helper()
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	nonce := make([]byte, gcm.NonceSize())
	payload := gcm.Seal(nonce, nonce, []byte(plaintext), []byte("new-api-config-v1"))
	return "v1." + base64.RawStdEncoding.EncodeToString(payload)
}

func TestEpayConfigEncryptionDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(filepath.Join(t.TempDir(), "epay.db"))
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN is not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN is not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "epay_crypto_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			oldDB, oldSecret, oldCrypto, oldOptions := DB, common.ConfigEncryptionSecret, common.CryptoSecret, common.OptionMap
			oldDatabaseType := common.MainDatabaseType()
			oldGateways := operation_setting.GetEpayGateways()
			DB, common.OptionMap = db, map[string]string{}
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			t.Cleanup(func() {
				require.NoError(t, db.Migrator().DropTable(&Option{}))
				require.NoError(t, sqlDB.Close())
				DB, common.ConfigEncryptionSecret, common.CryptoSecret, common.OptionMap = oldDB, oldSecret, oldCrypto, oldOptions
				common.SetMainDatabaseType(oldDatabaseType)
				operation_setting.SetEpayGateways(oldGateways)
			})
			require.NoError(t, db.AutoMigrate(&Option{}))
			var version string
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database version: %s", version)

			common.ConfigEncryptionSecret = ""
			require.NoError(t, InitConfigEncryption())
			persistedSecret := common.ConfigEncryptionSecret
			require.NotEmpty(t, persistedSecret)
			gateways := []operation_setting.EpayGateway{{ID: "primary", Name: "Primary", Address: "https://pay.example.com", MerchantID: "merchant", Key: "merchant-secret", Enabled: true}}
			encoded, err := operation_setting.EpayGateways2JsonString(gateways)
			require.NoError(t, err)
			require.NoError(t, db.Create(&Option{Key: operation_setting.EpayGatewaysOptionKey, Value: encoded}).Error)
			assert.NotContains(t, encoded, "merchant-secret")
			assert.Contains(t, encoded, "v2.")

			// A restarted replica has new process secrets but the same database.
			common.ConfigEncryptionSecret, common.CryptoSecret = "", "new-process-secret"
			require.NoError(t, db.AutoMigrate(&Option{}))
			require.NoError(t, InitConfigEncryption())
			assert.Equal(t, persistedSecret, common.ConfigEncryptionSecret)
			parsed, err := operation_setting.ParseEpayGateways(encoded)
			require.NoError(t, err)
			assert.Equal(t, "merchant-secret", parsed[0].Key)
			require.NoError(t, InitConfigEncryption())
			assert.Equal(t, persistedSecret, common.ConfigEncryptionSecret)

			// The internal secret cannot be listed or modified by option APIs.
			require.NoError(t, updateOptionMap(configEncryptionSecretOptionKey, persistedSecret))
			assert.NotContains(t, common.OptionMap, configEncryptionSecretOptionKey)
			require.Error(t, UpdateOption(configEncryptionSecretOptionKey, "replacement"))
			require.Error(t, UpdateOptionsBulk(map[string]string{configEncryptionSecretOptionKey: "replacement"}))
			var stored Option
			require.NoError(t, db.Where(&Option{Key: configEncryptionSecretOptionKey}).First(&stored).Error)
			assert.Equal(t, persistedSecret, stored.Value)

			// Upgrade old ciphertext with the original environment secret available.
			common.CryptoSecret = "legacy-process-secret"
			legacyKey := legacyEpayCiphertext(t, common.CryptoSecret, "legacy-merchant-secret")
			legacyJSON, err := common.Marshal([]map[string]any{{"id": "legacy", "name": "Legacy", "address": "https://legacy.example.com", "merchant_id": "legacy-merchant", "key": legacyKey, "enabled": true}})
			require.NoError(t, err)
			require.NoError(t, db.Model(&Option{}).Where(&Option{Key: operation_setting.EpayGatewaysOptionKey}).Update("value", string(legacyJSON)).Error)
			migrated, err := migrateEpayGatewayKeys(string(legacyJSON))
			require.NoError(t, err)
			assert.Contains(t, migrated, "v2.")
			assert.NotContains(t, migrated, legacyKey)
			assert.NotContains(t, migrated, "legacy-merchant-secret")
			// A replica holding an older snapshot must keep a newer saved value.
			latest, err := migrateEpayGatewayKeys(string(legacyJSON))
			require.NoError(t, err)
			assert.Equal(t, migrated, latest)
			common.CryptoSecret = "another-process-secret"
			parsed, err = operation_setting.ParseEpayGateways(migrated)
			require.NoError(t, err)
			assert.Equal(t, "legacy-merchant-secret", parsed[0].Key)

			unchanged, err := migrateEpayGatewayKeys(migrated)
			require.NoError(t, err)
			assert.Equal(t, migrated, unchanged)

			// Losing the old secret preserves gateway metadata and ciphertext.
			unchanged, err = migrateEpayGatewayKeys(string(legacyJSON))
			require.NoError(t, err)
			assert.Equal(t, string(legacyJSON), unchanged)
			require.NoError(t, updateOptionMap(operation_setting.EpayGatewaysOptionKey, unchanged))
			unavailable := operation_setting.GetEpayGateways()
			require.Len(t, unavailable, 1)
			assert.Empty(t, unavailable[0].Key)
			assert.Equal(t, legacyKey, unavailable[0].EncryptedKey)
			assert.False(t, operation_setting.EpayGatewayViews(unavailable)[0].KeySet)
			require.Error(t, operation_setting.ValidateEpayGateways(unavailable))
			unavailable[0].Enabled = false
			require.NoError(t, operation_setting.ValidateEpayGateways(unavailable))
			unavailable[0].Key, unavailable[0].Enabled = "restored-merchant-secret", true
			require.NoError(t, operation_setting.ValidateEpayGateways(unavailable))
			restored, err := operation_setting.EpayGateways2JsonString(unavailable)
			require.NoError(t, err)
			parsed, err = operation_setting.ParseEpayGateways(restored)
			require.NoError(t, err)
			assert.Equal(t, "restored-merchant-secret", parsed[0].Key)

			// Corrupt persisted key material must never be silently regenerated.
			require.NoError(t, db.Model(&Option{}).Where(&Option{Key: configEncryptionSecretOptionKey}).Update("value", "invalid").Error)
			require.Error(t, InitConfigEncryption())
			require.NoError(t, db.Where(&Option{Key: configEncryptionSecretOptionKey}).First(&stored).Error)
			assert.Equal(t, "invalid", stored.Value)
		})
	}
}
