package controller

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

func isRegistrationInviteError(err error) bool {
	return errors.Is(err, model.ErrRegistrationInviteRequired) ||
		errors.Is(err, model.ErrRegistrationInviteInvalid) ||
		errors.Is(err, model.ErrRegistrationInviteExpired) ||
		errors.Is(err, model.ErrRegistrationInviteUsed) ||
		errors.Is(err, model.ErrRegistrationInviteDisabled)
}

func writeRegistrationInviteError(c *gin.Context, err error) {
	key := "user.registration_invite_invalid"
	switch {
	case errors.Is(err, model.ErrRegistrationInviteRequired):
		key = "user.registration_invite_required"
	case errors.Is(err, model.ErrRegistrationInviteExpired):
		key = "user.registration_invite_expired"
	case errors.Is(err, model.ErrRegistrationInviteUsed):
		key = "user.registration_invite_used"
	case errors.Is(err, model.ErrRegistrationInviteDisabled):
		key = "user.registration_invite_disabled"
	}
	common.ApiErrorI18n(c, key)
}

type registrationInviteCreateRequest struct {
	Count     int   `json:"count"`
	ExpiresAt int64 `json:"expires_at"`
}

func CreateRegistrationInvites(c *gin.Context) {
	var request registrationInviteCreateRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if request.Count == 0 {
		request.Count = 1
	}
	if request.Count < 1 || request.Count > 1000 || request.ExpiresAt < 0 {
		common.ApiErrorMsg(c, "邀请码数量或过期时间无效")
		return
	}
	codes, err := model.CreateRegistrationInvites(request.Count, c.GetInt("id"), request.ExpiresAt)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "registration_invite.create", map[string]any{"count": len(codes), "expires_at": request.ExpiresAt})
	common.ApiSuccess(c, codes)
}

func ListRegistrationInvites(c *gin.Context) {
	page, _ := strconv.Atoi(c.Query("p"))
	pageSize, _ := strconv.Atoi(c.Query("page_size"))
	invites, total, err := model.ListRegistrationInvites(page, pageSize)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo := common.GetPageQuery(c)
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(invites)
	common.ApiSuccess(c, pageInfo)
}

func UpdateRegistrationInvite(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的邀请码 ID")
		return
	}
	var request struct {
		Status int `json:"status"`
	}
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	if err := model.SetRegistrationInviteStatus(id, request.Status); err != nil {
		common.ApiError(c, err)
		return
	}
	recordManageAudit(c, "registration_invite.status_update", map[string]any{"id": id, "status": request.Status})
	common.ApiSuccess(c, nil)
}

type qqBotInviteRequest struct {
	OpenID string `json:"open_id"`
}

// ClaimRegistrationInviteForQQ is an internal endpoint for the standalone QQ
// bot. The bot secret is kept outside the database and compared in constant
// time by the middleware-style handler below.
func ClaimRegistrationInviteForQQ(c *gin.Context) {
	configured := strings.TrimSpace(common.GetEnvOrDefaultString("STARRY_API_BOT_SECRET", ""))
	provided := c.GetHeader("X-QQ-Bot-Secret")
	if len(configured) < 32 || provided == "" || len(provided) > 256 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "未授权"})
		return
	}
	configuredHash := sha256.Sum256([]byte(configured))
	providedHash := sha256.Sum256([]byte(provided))
	if subtle.ConstantTimeCompare(providedHash[:], configuredHash[:]) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"success": false, "message": "未授权"})
		return
	}
	var request qqBotInviteRequest
	if err := common.DecodeJson(c.Request.Body, &request); err != nil {
		common.ApiErrorMsg(c, "无效的参数")
		return
	}
	code, err := model.ClaimRegistrationInviteForQQ(request.OpenID)
	if err != nil {
		if errors.Is(err, model.ErrRegistrationInviteInvalid) {
			common.ApiErrorMsg(c, "暂无可用邀请码或该 QQ 用户已领取")
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"code": code})
}
