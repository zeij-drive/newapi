package operation_setting

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

const EpayGatewaysOptionKey = "EpayGateways"

var epayGateways []EpayGateway
var epayGatewaysMu sync.RWMutex

type EpayGateway struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Address      string `json:"address"`
	MerchantID   string `json:"merchant_id"`
	EncryptedKey string `json:"-"`
	// Key is only used while constructing an upstream client. It must never be
	// serialized into an API response or a persisted option value.
	Key     string `json:"-"`
	Enabled bool   `json:"enabled"`
}

type EpayGatewayView struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Address    string `json:"address"`
	MerchantID string `json:"merchant_id"`
	Enabled    bool   `json:"enabled"`
	KeySet     bool   `json:"key_set"`
}

func ParseEpayGateways(raw string) ([]EpayGateway, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var stored []struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Address    string `json:"address"`
		MerchantID string `json:"merchant_id"`
		Key        string `json:"key"`
		Enabled    bool   `json:"enabled"`
	}
	if err := common.UnmarshalJsonStr(raw, &stored); err != nil {
		return nil, err
	}
	gateways := make([]EpayGateway, 0, len(stored))
	for _, item := range stored {
		gateways = append(gateways, EpayGateway{ID: item.ID, Name: item.Name, Address: item.Address, MerchantID: item.MerchantID, EncryptedKey: item.Key, Enabled: item.Enabled})
	}
	for i := range gateways {
		if gateways[i].EncryptedKey == "" {
			continue
		}
		key, err := common.DecryptWithCryptoSecret(gateways[i].EncryptedKey)
		if err != nil {
			return nil, fmt.Errorf("解密 Epay 网关 %q 密钥失败: %w", gateways[i].Name, err)
		}
		gateways[i].Key = key
	}
	return gateways, nil
}

func SetEpayGateways(gateways []EpayGateway) {
	copyOfGateways := make([]EpayGateway, len(gateways))
	copy(copyOfGateways, gateways)
	epayGatewaysMu.Lock()
	epayGateways = copyOfGateways
	epayGatewaysMu.Unlock()
}

func GetEpayGateways() []EpayGateway {
	epayGatewaysMu.RLock()
	defer epayGatewaysMu.RUnlock()
	gateways := make([]EpayGateway, len(epayGateways))
	copy(gateways, epayGateways)
	return gateways
}

func EpayGateways2JsonString(gateways []EpayGateway) (string, error) {
	stored := make([]map[string]any, len(gateways))
	for i, gateway := range gateways {
		stored[i] = map[string]any{"id": gateway.ID, "name": gateway.Name, "address": gateway.Address, "merchant_id": gateway.MerchantID, "enabled": gateway.Enabled}
		if gateway.Key != "" {
			encrypted, err := common.EncryptWithCryptoSecret(gateway.Key)
			if err != nil {
				return "", err
			}
			stored[i]["key"] = encrypted
		} else if gateway.EncryptedKey != "" {
			stored[i]["key"] = gateway.EncryptedKey
		}
	}
	data, err := common.Marshal(stored)
	return string(data), err
}

func ValidateEpayGateways(gateways []EpayGateway) error {
	seen := make(map[string]struct{}, len(gateways))
	for i := range gateways {
		gateway := &gateways[i]
		gateway.ID = strings.TrimSpace(gateway.ID)
		gateway.Name = strings.TrimSpace(gateway.Name)
		gateway.Address = strings.TrimRight(strings.TrimSpace(gateway.Address), "/")
		gateway.MerchantID = strings.TrimSpace(gateway.MerchantID)
		if gateway.ID == "" || gateway.Name == "" || gateway.Address == "" || gateway.MerchantID == "" {
			return fmt.Errorf("Epay 网关 #%d 缺少必填字段", i+1)
		}
		if _, ok := seen[gateway.ID]; ok {
			return errors.New("Epay 网关 ID 不能重复")
		}
		seen[gateway.ID] = struct{}{}
		if gateway.Key == "" && gateway.EncryptedKey == "" {
			return fmt.Errorf("Epay 网关 %q 缺少密钥", gateway.Name)
		}
	}
	return nil
}

func EpayGatewayViews(gateways []EpayGateway) []EpayGatewayView {
	views := make([]EpayGatewayView, 0, len(gateways))
	for _, gateway := range gateways {
		views = append(views, EpayGatewayView{
			ID: gateway.ID, Name: gateway.Name, Address: gateway.Address,
			MerchantID: gateway.MerchantID, Enabled: gateway.Enabled,
			KeySet: gateway.EncryptedKey != "" || gateway.Key != "",
		})
	}
	return views
}

func FindEpayGateway(gateways []EpayGateway, id string) *EpayGateway {
	for i := range gateways {
		if gateways[i].ID == id && gateways[i].Enabled {
			return &gateways[i]
		}
	}
	return nil
}
