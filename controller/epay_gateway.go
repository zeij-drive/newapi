package controller

import (
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

type epayGatewayInput struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Address    string `json:"address"`
	MerchantID string `json:"merchant_id"`
	Key        string `json:"key"`
	Enabled    bool   `json:"enabled"`
}

func currentEpayGateways() []operation_setting.EpayGateway {
	gateways := operation_setting.GetEpayGateways()
	if len(gateways) == 0 && operation_setting.PayAddress != "" && operation_setting.EpayId != "" && operation_setting.EpayKey != "" {
		gateways = []operation_setting.EpayGateway{{ID: "default", Name: "Epay", Address: operation_setting.PayAddress, MerchantID: operation_setting.EpayId, Key: operation_setting.EpayKey, Enabled: true}}
	}
	return gateways
}

func isEpayPaymentMethod(method map[string]string) bool {
	if strings.TrimSpace(method["type"]) == "" {
		return false
	}
	switch method["type"] {
	case model.PaymentMethodStripe, model.PaymentMethodWaffo, model.PaymentMethodWaffoPancake:
		return false
	default:
		return true
	}
}

func GetEpayGatewayConfig(c *gin.Context) {
	common.ApiSuccess(c, operation_setting.EpayGatewayViews(currentEpayGateways()))
}

func UpdateEpayGatewayConfig(c *gin.Context) {
	var req struct {
		Gateways []epayGatewayInput `json:"gateways"`
	}
	if err := common.DecodeJson(c.Request.Body, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "参数错误"})
		return
	}
	previous := currentEpayGateways()
	byID := make(map[string]operation_setting.EpayGateway, len(previous))
	for _, gateway := range previous {
		byID[gateway.ID] = gateway
	}
	gateways := make([]operation_setting.EpayGateway, 0, len(req.Gateways))
	for _, input := range req.Gateways {
		gateway := operation_setting.EpayGateway{ID: strings.TrimSpace(input.ID), Name: input.Name, Address: input.Address, MerchantID: input.MerchantID, Key: strings.TrimSpace(input.Key), Enabled: input.Enabled}
		if gateway.Key == "" {
			gateway.Key = byID[gateway.ID].Key
			gateway.EncryptedKey = byID[gateway.ID].EncryptedKey
		}
		gateways = append(gateways, gateway)
	}
	if err := operation_setting.ValidateEpayGateways(gateways); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	encoded, err := operation_setting.EpayGateways2JsonString(gateways)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOptionsBulk(map[string]string{
		operation_setting.EpayGatewaysOptionKey: encoded,
		"PayAddress":                            "",
		"EpayId":                                "",
		"EpayKey":                               "",
	}); err != nil {
		common.ApiError(c, err)
		return
	}
	updatedGateways, err := operation_setting.ParseEpayGateways(encoded)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, operation_setting.EpayGatewayViews(updatedGateways))
}
