package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	"github.com/gin-gonic/gin"
)

func SetVideoRouter(router *gin.Engine) {
	// Video proxy: accepts either session auth (dashboard) or token auth (API clients)
	videoProxyRouter := router.Group("/v1")
	videoProxyRouter.Use(middleware.RouteTag("relay"))
	videoProxyRouter.Use(middleware.TokenOrUserAuth())
	{
		videoProxyRouter.GET("/videos/:task_id/content", controller.VideoProxy)
	}

	videoV1Router := router.Group("/v1")
	videoV1Router.Use(middleware.RouteTag("relay"))
	videoV1Router.Use(middleware.TokenAuth(), middleware.TokenHealthRecord(), middleware.Distribute())
	{
		videoV1Router.POST("/video/generations", controller.RelayTask)
		videoV1Router.GET("/video/generations/:task_id", controller.RelayTaskFetch)
		videoV1Router.POST("/videos/:video_id/remix", controller.RelayTask)
	}
	// openai compatible API video routes
	// docs: https://platform.openai.com/docs/api-reference/videos/create
	{
		videoV1Router.POST("/videos", controller.RelayTask)
		videoV1Router.GET("/videos/:task_id", controller.RelayTaskFetch)
	}

	// SD 素材代理（Seedance 素材库，对外表面对齐 sd_real_max）：
	// 上传走渠道分发（model 由 SdAssetRequestConvert 注入，Distribute 按 model 选 doubao-video 渠道）；
	// 查询按素材落库记录路由到创建时的渠道，不走 Distribute（GET 无 model 可选渠道）。
	sdAssetSubmitRouter := router.Group("/v1/sd")
	sdAssetSubmitRouter.Use(middleware.RouteTag("relay"))
	sdAssetSubmitRouter.Use(middleware.SdAssetRequestConvert(), middleware.TokenAuth(), middleware.TokenHealthRecord(), middleware.Distribute())
	{
		sdAssetSubmitRouter.POST("/assets", controller.RelaySdAssetCreate)
	}
	sdAssetFetchRouter := router.Group("/v1/sd")
	sdAssetFetchRouter.Use(middleware.RouteTag("relay"))
	sdAssetFetchRouter.Use(middleware.TokenAuth())
	{
		sdAssetFetchRouter.GET("/assets/:asset_id", controller.RelaySdAssetGet)
	}

	klingV1Router := router.Group("/kling/v1")
	klingV1Router.Use(middleware.RouteTag("relay"))
	klingV1Router.Use(middleware.KlingRequestConvert(), middleware.TokenAuth(), middleware.TokenHealthRecord(), middleware.Distribute())
	{
		klingV1Router.POST("/videos/text2video", controller.RelayTask)
		klingV1Router.POST("/videos/image2video", controller.RelayTask)
		klingV1Router.GET("/videos/text2video/:task_id", controller.RelayTaskFetch)
		klingV1Router.GET("/videos/image2video/:task_id", controller.RelayTaskFetch)
	}

	// Jimeng official API routes - direct mapping to official API format
	jimengOfficialGroup := router.Group("jimeng")
	jimengOfficialGroup.Use(middleware.RouteTag("relay"))
	jimengOfficialGroup.Use(middleware.JimengRequestConvert(), middleware.TokenAuth(), middleware.TokenHealthRecord(), middleware.Distribute())
	{
		// Maps to: /?Action=CVSync2AsyncSubmitTask&Version=2022-08-31 and /?Action=CVSync2AsyncGetResult&Version=2022-08-31
		jimengOfficialGroup.POST("/", controller.RelayTask)
	}
}
