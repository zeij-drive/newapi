package model

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/logger"
)

const configEncryptionSecretOptionKey = "ConfigEncryptionSecret"

// InitConfigEncryption loads a database-scoped configuration encryption key.
// The unique options key makes concurrent replicas use the same secret; it is
// excluded from administrator options and cannot be changed through that API.
func InitConfigEncryption() error {
	// Keep key material out of GORM's SQL logs, including debug/error output.
	db := DB.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)})
	var stored Option
	err := db.Where(&Option{Key: configEncryptionSecretOptionKey}).First(&stored).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return fmt.Errorf("generate configuration encryption key: %w", err)
		}
		candidate := Option{Key: configEncryptionSecretOptionKey, Value: base64.RawStdEncoding.EncodeToString(key)}
		if err := db.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "key"}}, DoNothing: true,
		}).Create(&candidate).Error; err != nil {
			return errors.New("persist configuration encryption key failed")
		}
		err = db.Where(&Option{Key: configEncryptionSecretOptionKey}).First(&stored).Error
	}
	if err != nil {
		return errors.New("load configuration encryption key failed")
	}
	key, err := base64.RawStdEncoding.DecodeString(stored.Value)
	if err != nil || len(key) != 32 {
		return errors.New("persisted configuration encryption key is invalid")
	}
	common.ConfigEncryptionSecret = stored.Value
	return nil
}

// migrateEpayGatewayKeys upgrades readable v1 ciphertext without overwriting
// keys whose original encryption secret is no longer available.
func migrateEpayGatewayKeys(raw string) (string, error) {
	gateways, err := operation_setting.ParseEpayGateways(raw)
	if err != nil && gateways == nil {
		return raw, err
	}
	needsMigration := false
	for _, gateway := range gateways {
		if gateway.Key != "" && strings.HasPrefix(gateway.EncryptedKey, "v1.") {
			needsMigration = true
			break
		}
	}
	if !needsMigration {
		return raw, nil
	}
	encoded, err := operation_setting.EpayGateways2JsonString(gateways)
	if err != nil {
		return raw, err
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		var latest Option
		if err := lockForUpdate(tx).Where(&Option{Key: operation_setting.EpayGatewaysOptionKey}).First(&latest).Error; err != nil {
			return errors.New("reload Epay gateway keys failed")
		}
		if latest.Value != raw {
			encoded = latest.Value
			return nil
		}
		if err := tx.Model(&Option{}).Where(&Option{Key: operation_setting.EpayGatewaysOptionKey}).Update("value", encoded).Error; err != nil {
			return errors.New("persist upgraded Epay gateway keys failed")
		}
		return nil
	})
	if err != nil {
		return raw, err
	}
	return encoded, nil
}
