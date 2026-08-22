package controller

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/backgroundtask"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// GetRuntimeMetrics 返回运行时大盘快照。
// 完全从 Redis/内存读，不会直接查 logs 表。
// 首次启动时如果快照还没算完，返回 success + 空快照（不 inline 触发重算，
// 避免多 admin 并发触发"惊群"。后台 60s tick 会自然填充）。
func GetRuntimeMetrics(c *gin.Context) {
	snap, err := service.GetRuntimeSnapshot()
	if err != nil {
		// 返回一个空快照让前端正常渲染（显示 "No data" 占位），不报错
		common.ApiSuccess(c, service.EmptyRuntimeSnapshot())
		return
	}
	common.ApiSuccess(c, snap)
}

// RecomputeRuntimeMetrics 手动触发重算（admin 调试用，CriticalRateLimit 防刷）。
// 异步执行，立即返回；前端可以隔几秒再 GET /runtime 拿最新数据。
func RecomputeRuntimeMetrics(c *gin.Context) {
	err := backgroundtask.Submit("manual-runtime-metrics", func(context.Context) {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		_ = service.RecomputeRuntimeMetricsOnce(ctx)
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"triggered": true, "started_at": time.Now().Unix()})
}
