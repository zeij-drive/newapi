package controller

import (
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

func GetUsageRanking(c *gin.Context) {
	snapshot, err := service.GetUsageRanking(c.DefaultQuery("period", "week"), time.Now())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	common.ApiSuccess(c, snapshot)
}

func GetUsageRankingRewards(c *gin.Context) {
	setting, err := operation_setting.GetUsageRankingRewards()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, setting)
}

func UpdateUsageRankingRewards(c *gin.Context) {
	var request operation_setting.UsageRankingRewards
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "Invalid reward settings")
		return
	}
	previous, err := operation_setting.GetUsageRankingRewards()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	request.EnabledAt = previous.EnabledAt
	if request.Enabled && !previous.Enabled {
		request.EnabledAt = time.Now().Unix()
	}
	if err := request.Validate(); err != nil {
		common.ApiError(c, err)
		return
	}
	if request.Enabled && !common.DataExportEnabled {
		common.ApiErrorMsg(c, "Enable data export before enabling ranking rewards")
		return
	}
	encoded, err := common.Marshal(request)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.UpdateOptionsBulk(map[string]string{operation_setting.UsageRankingRewardsOptionKey: string(encoded)}); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, request)
}
