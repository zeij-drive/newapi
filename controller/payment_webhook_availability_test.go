package controller

import (
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEpayCallbackRequiresOrderedAmountAndMerchant(t *testing.T) {
	client, err := epay.NewClient(&epay.Config{PartnerID: "merchant", Key: "secret"}, "https://pay.example.com")
	require.NoError(t, err)
	for _, tc := range []struct {
		amount, merchant string
		valid            bool
	}{
		{"12.30", "merchant", true}, {"12.3", "merchant", true}, {"12.300", "merchant", true},
		{"0.01", "merchant", false}, {"12.301", "merchant", false}, {"", "merchant", false},
		{"12.30", "other", false}, {"12.30", "", false}, {"1.23e1", "merchant", false},
		{"-12.30", "merchant", false}, {"NaN", "merchant", false}, {"12.30 ", "merchant", false},
	} {
		t.Run(tc.amount+"/"+tc.merchant, func(t *testing.T) {
			assert.Equal(t, tc.valid, epayCallbackMatchesOrder(client, map[string]string{"money": tc.amount, "pid": tc.merchant}, 12.30))
		})
	}
	assert.False(t, epayCallbackMatchesOrder(client, map[string]string{"money": "12.30", "pid": "merchant"}, math.NaN()))
}

func TestEpaySignedUnderpaymentCannotCompleteTopupOrSubscription(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	oldDB, oldLogDB := model.DB, model.LOG_DB
	oldGateways := operation_setting.GetEpayGateways()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB, model.LOG_DB = db, db
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLogDB
		operation_setting.SetEpayGateways(oldGateways)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.SubscriptionOrder{}))
	operation_setting.SetEpayGateways([]operation_setting.EpayGateway{{ID: "primary", Name: "Gateway", Address: "https://pay.example.com", MerchantID: "merchant", Key: "secret", Enabled: true}})
	topup := model.TopUp{TradeNo: "topup-order", Money: 12.30, Amount: 100, PaymentGateway: "primary", PaymentMethod: "alipay", PaymentProvider: model.PaymentProviderEpay, Status: common.TopUpStatusPending}
	order := model.SubscriptionOrder{TradeNo: "subscription-order", Money: 12.30, PaymentGateway: "primary", PaymentMethod: "alipay", PaymentProvider: model.PaymentProviderEpay, Status: common.TopUpStatusPending}
	require.NoError(t, db.Create(&topup).Error)
	require.NoError(t, db.Create(&order).Error)
	for _, tc := range []struct {
		name, tradeNo string
		handler       gin.HandlerFunc
		browser       bool
	}{
		{"topup", topup.TradeNo, EpayNotify, false}, {"subscription notify", order.TradeNo, SubscriptionEpayNotify, false}, {"subscription return", order.TradeNo, SubscriptionEpayReturn, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, signed := range []map[string]string{{"money": "0.01", "pid": "merchant"}, {"money": "12.30", "pid": "other"}} {
				signed["out_trade_no"], signed["type"], signed["trade_status"] = tc.tradeNo, "alipay", epay.StatusTradeSuccess
				params := epay.GenerateParams(signed, "secret")
				query := url.Values{}
				for key, value := range params {
					query.Set(key, value)
				}
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				c.Request = httptest.NewRequest(http.MethodGet, "/callback?"+query.Encode(), nil)
				tc.handler(c)
				if tc.browser {
					assert.Contains(t, recorder.Header().Get("Location"), "pay=fail")
				} else {
					assert.Equal(t, "fail", recorder.Body.String())
				}
			}
		})
	}
	require.NoError(t, db.First(&topup, topup.Id).Error)
	require.NoError(t, db.First(&order, order.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, topup.Status)
	assert.Equal(t, common.TopUpStatusPending, order.Status)
}

func confirmPaymentComplianceForTest(t *testing.T) {
	t.Helper()
	paymentSetting := operation_setting.GetPaymentSetting()
	originalConfirmed := paymentSetting.ComplianceConfirmed
	originalTermsVersion := paymentSetting.ComplianceTermsVersion
	t.Cleanup(func() {
		paymentSetting.ComplianceConfirmed = originalConfirmed
		paymentSetting.ComplianceTermsVersion = originalTermsVersion
	})
	paymentSetting.ComplianceConfirmed = true
	paymentSetting.ComplianceTermsVersion = operation_setting.CurrentComplianceTermsVersion
}

func TestStripeWebhookEnabledRequiresTopUpAndWebhookConfig(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	originalAPISecret := setting.StripeApiSecret
	originalWebhookSecret := setting.StripeWebhookSecret
	originalPriceID := setting.StripePriceId
	t.Cleanup(func() {
		setting.StripeApiSecret = originalAPISecret
		setting.StripeWebhookSecret = originalWebhookSecret
		setting.StripePriceId = originalPriceID
	})

	setting.StripeWebhookSecret = ""
	setting.StripeApiSecret = "sk_test_123"
	setting.StripePriceId = "price_123"
	require.False(t, isStripeWebhookEnabled())

	setting.StripeWebhookSecret = "whsec_test"
	require.True(t, isStripeWebhookEnabled())

	setting.StripePriceId = ""
	require.False(t, isStripeWebhookEnabled())
}

func TestCreemWebhookEnabledRequiresTopUpAndWebhookConfig(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	originalAPIKey := setting.CreemApiKey
	originalProducts := setting.CreemProducts
	originalWebhookSecret := setting.CreemWebhookSecret
	t.Cleanup(func() {
		setting.CreemApiKey = originalAPIKey
		setting.CreemProducts = originalProducts
		setting.CreemWebhookSecret = originalWebhookSecret
	})

	setting.CreemWebhookSecret = ""
	setting.CreemApiKey = "creem_api_key"
	setting.CreemProducts = `[{"productId":"prod_123"}]`
	require.False(t, isCreemWebhookEnabled())

	setting.CreemWebhookSecret = "creem_secret"
	require.True(t, isCreemWebhookEnabled())

	setting.CreemProducts = "[]"
	require.False(t, isCreemWebhookEnabled())
}

func TestWaffoWebhookEnabledRequiresTopUpAndWebhookConfig(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	originalEnabled := setting.WaffoEnabled
	originalSandbox := setting.WaffoSandbox
	originalAPIKey := setting.WaffoApiKey
	originalPrivateKey := setting.WaffoPrivateKey
	originalPublicCert := setting.WaffoPublicCert
	originalSandboxAPIKey := setting.WaffoSandboxApiKey
	originalSandboxPrivateKey := setting.WaffoSandboxPrivateKey
	originalSandboxPublicCert := setting.WaffoSandboxPublicCert
	t.Cleanup(func() {
		setting.WaffoEnabled = originalEnabled
		setting.WaffoSandbox = originalSandbox
		setting.WaffoApiKey = originalAPIKey
		setting.WaffoPrivateKey = originalPrivateKey
		setting.WaffoPublicCert = originalPublicCert
		setting.WaffoSandboxApiKey = originalSandboxAPIKey
		setting.WaffoSandboxPrivateKey = originalSandboxPrivateKey
		setting.WaffoSandboxPublicCert = originalSandboxPublicCert
	})

	setting.WaffoEnabled = true
	setting.WaffoSandbox = false
	setting.WaffoApiKey = ""
	setting.WaffoPrivateKey = "private"
	setting.WaffoPublicCert = "public"
	require.False(t, isWaffoWebhookEnabled())

	setting.WaffoApiKey = "api"
	require.True(t, isWaffoWebhookEnabled())

	setting.WaffoEnabled = false
	require.False(t, isWaffoWebhookEnabled())

	setting.WaffoEnabled = true
	setting.WaffoSandbox = true
	setting.WaffoSandboxApiKey = ""
	setting.WaffoSandboxPrivateKey = "sandbox_private"
	setting.WaffoSandboxPublicCert = "sandbox_public"
	require.False(t, isWaffoWebhookEnabled())

	setting.WaffoSandboxApiKey = "sandbox_api"
	require.True(t, isWaffoWebhookEnabled())
}

func TestWaffoPancakeWebhookEnabledRequiresTopUpAndWebhookConfig(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	originalMerchantID := setting.WaffoPancakeMerchantID
	originalPrivateKey := setting.WaffoPancakePrivateKey
	originalProductID := setting.WaffoPancakeProductID
	t.Cleanup(func() {
		setting.WaffoPancakeMerchantID = originalMerchantID
		setting.WaffoPancakePrivateKey = originalPrivateKey
		setting.WaffoPancakeProductID = originalProductID
	})

	// Presence of all three credentials enables the gateway. Webhook public
	// keys are bundled in the SDK and there is no separate Enabled toggle —
	// clear any of the three fields to disable.
	setting.WaffoPancakeMerchantID = ""
	setting.WaffoPancakePrivateKey = "private"
	setting.WaffoPancakeProductID = "product"
	require.False(t, isWaffoPancakeWebhookEnabled())

	setting.WaffoPancakeMerchantID = "merchant"
	require.True(t, isWaffoPancakeWebhookEnabled())

	setting.WaffoPancakeProductID = ""
	require.False(t, isWaffoPancakeWebhookEnabled())

	setting.WaffoPancakeProductID = "product"
	setting.WaffoPancakePrivateKey = ""
	require.False(t, isWaffoPancakeWebhookEnabled())
}

func TestEpayWebhookRemainsEnabledForPendingOrders(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	originalPayAddress := operation_setting.PayAddress
	originalEpayID := operation_setting.EpayId
	originalEpayKey := operation_setting.EpayKey
	originalPayMethods := operation_setting.PayMethods
	originalGateways := operation_setting.GetEpayGateways()
	t.Cleanup(func() {
		operation_setting.PayAddress = originalPayAddress
		operation_setting.EpayId = originalEpayID
		operation_setting.EpayKey = originalEpayKey
		operation_setting.PayMethods = originalPayMethods
		operation_setting.SetEpayGateways(originalGateways)
	})
	operation_setting.SetEpayGateways(nil)

	operation_setting.PayAddress = "https://pay.example.com"
	operation_setting.EpayId = "epay_id"
	operation_setting.EpayKey = ""
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}}
	require.False(t, isEpayWebhookEnabled())

	operation_setting.EpayKey = "epay_key"
	require.True(t, isEpayWebhookEnabled())

	operation_setting.PayMethods = nil
	require.False(t, isEpayTopUpEnabled())
	require.True(t, isEpayWebhookEnabled())

	operation_setting.PayAddress = ""
	operation_setting.EpayId = ""
	operation_setting.EpayKey = ""
	operation_setting.SetEpayGateways([]operation_setting.EpayGateway{{
		ID: "primary", Name: "Primary", Address: "https://pay.example.com",
		MerchantID: "merchant", Key: "secret", Enabled: false,
	}})
	require.False(t, isEpayTopUpEnabled())
	require.True(t, isEpayWebhookEnabled())
}

func TestEpayLegacyOrderUsesDefaultGatewayAfterMigration(t *testing.T) {
	previousAddress := operation_setting.PayAddress
	previousID := operation_setting.EpayId
	previousKey := operation_setting.EpayKey
	previousGateways := operation_setting.GetEpayGateways()
	t.Cleanup(func() {
		operation_setting.PayAddress = previousAddress
		operation_setting.EpayId = previousID
		operation_setting.EpayKey = previousKey
		operation_setting.SetEpayGateways(previousGateways)
	})

	operation_setting.PayAddress = ""
	operation_setting.EpayId = ""
	operation_setting.EpayKey = ""
	operation_setting.SetEpayGateways([]operation_setting.EpayGateway{{
		ID: "default", Name: "Epay", Address: "https://pay.example.com",
		MerchantID: "merchant", Key: "old-secret", Enabled: false,
	}})

	require.NotNil(t, getEpayClientForStoredGateway(""))
	require.NotNil(t, getEpayClientForStoredGateway("default"))
	require.Nil(t, getEpayClientForStoredGateway("unknown"))
}

func TestEpayGatewayOptionRequiresDedicatedEndpoint(t *testing.T) {
	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(`{"key":"EpayGateways","value":"[]"}`))

	UpdateOption(context)

	var payload struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	require.False(t, payload.Success)
}

func TestTopUpInfoHidesEpayMethodsWithoutEnabledGateway(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	previousAddress := operation_setting.PayAddress
	previousID := operation_setting.EpayId
	previousKey := operation_setting.EpayKey
	previousMethods := operation_setting.PayMethods
	previousGateways := operation_setting.GetEpayGateways()
	t.Cleanup(func() {
		operation_setting.PayAddress = previousAddress
		operation_setting.EpayId = previousID
		operation_setting.EpayKey = previousKey
		operation_setting.PayMethods = previousMethods
		operation_setting.SetEpayGateways(previousGateways)
	})
	operation_setting.PayAddress = ""
	operation_setting.EpayId = ""
	operation_setting.EpayKey = ""
	operation_setting.PayMethods = []map[string]string{{"name": "Alipay", "type": "alipay"}}
	operation_setting.SetEpayGateways([]operation_setting.EpayGateway{{
		ID: "disabled", Name: "Disabled", Address: "https://pay.example.com",
		MerchantID: "merchant", Key: "secret", Enabled: false,
	}})

	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/user/topup/info", nil)
	GetTopUpInfo(context)

	var payload struct {
		Data struct {
			PayMethods []map[string]string `json:"pay_methods"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	for _, method := range payload.Data.PayMethods {
		require.NotEqual(t, "alipay", method["type"])
	}
}

func TestEpayMethodsUseConfiguredGateways(t *testing.T) {
	confirmPaymentComplianceForTest(t)
	previousMethods := operation_setting.PayMethods
	previousGateways := operation_setting.GetEpayGateways()
	t.Cleanup(func() {
		operation_setting.PayMethods = previousMethods
		operation_setting.SetEpayGateways(previousGateways)
	})
	operation_setting.PayMethods = []map[string]string{
		{"name": "WeChat", "type": "wxpay", "gateway_id": "primary"},
		{"name": "WeChat backup", "type": "wxpay", "gateway_id": "backup"},
	}
	operation_setting.SetEpayGateways([]operation_setting.EpayGateway{
		{ID: "primary", Name: "Primary", Address: "https://primary.example.com", MerchantID: "merchant-primary", Key: "secret-primary", Enabled: true},
		{ID: "backup", Name: "Backup", Address: "https://backup.example.com", MerchantID: "merchant-backup", Key: "secret-backup", Enabled: true},
	})

	require.True(t, operation_setting.IsPayMethodAvailableForGateway("wxpay", "primary"))
	require.True(t, operation_setting.IsPayMethodAvailableForGateway("wxpay", "backup"))
	require.False(t, operation_setting.IsPayMethodAvailableForGateway("wxpay", "other"))

	response := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/user/topup/info", nil)
	GetTopUpInfo(context)
	var payload struct {
		Data struct {
			PayMethods []map[string]string `json:"pay_methods"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	var epayMethods []map[string]string
	for _, method := range payload.Data.PayMethods {
		if method["type"] == "wxpay" {
			epayMethods = append(epayMethods, method)
		}
	}
	require.Equal(t, []map[string]string{
		{"name": "WeChat", "type": "wxpay", "gateway_id": "primary"},
		{"name": "WeChat backup", "type": "wxpay", "gateway_id": "backup"},
	}, epayMethods)

	gateways := operation_setting.GetEpayGateways()
	gateways[1].Enabled = false
	operation_setting.SetEpayGateways(gateways)
	require.True(t, isEpayTopUpEnabled())
	response = httptest.NewRecorder()
	context, _ = gin.CreateTestContext(response)
	context.Request = httptest.NewRequest(http.MethodGet, "/api/user/topup/info", nil)
	GetTopUpInfo(context)
	require.NoError(t, common.Unmarshal(response.Body.Bytes(), &payload))
	for _, method := range payload.Data.PayMethods {
		require.NotEqual(t, "backup", method["gateway_id"])
	}
}
