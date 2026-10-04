package router

import (
	"net/http"

	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/service/authz"
	"github.com/gin-gonic/gin"
)

func registerChannelStatusRoutes(apiRouter *gin.RouterGroup) {
	statusRoute := apiRouter.Group("/channel/status", middleware.DisableCache(), middleware.UserAuth())
	statusRoute.GET("/", controller.GetChannelGroupStatus)
	probes := statusRoute.Group("/probes", middleware.RootAuth())
	handlePermissionRoute(probes, http.MethodGet, "/", authz.ChannelRead, controller.GetChannelStatusProbes)
	handlePermissionRoute(probes, http.MethodPut, "/", authz.ChannelWrite, middleware.CriticalRateLimit(), controller.UpdateChannelStatusProbes)
	handlePermissionRoute(probes, http.MethodPost, "/run/", authz.ChannelOperate, middleware.CriticalRateLimit(), controller.RunChannelStatusProbes)
	handlePermissionRoute(probes, http.MethodPost, "/:id/run/", authz.ChannelOperate, middleware.CriticalRateLimit(), controller.RunChannelStatusProbes)
}
