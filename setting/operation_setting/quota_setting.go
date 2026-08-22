package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type QuotaSetting struct {
	EnableFreeModelPreConsume bool `json:"enable_free_model_pre_consume"` // 是否对免费模型启用预消耗
	// 流式上游零产出时自动免单：completion_tokens=0 且未收到任何首字节响应 (frt<0)
	BillingRefundWhenNoOutput bool `json:"billing_refund_when_no_output"`
	// client_gone（客户端中途断开）零产出免单的最小请求时长（秒）。
	// 仅当请求实际持续 >= 该值才免单，用于区分"被动久等慢上游的冤案"与"秒断刷 cache 的滥用"。
	// 其它结束原因（EOF/timeout 等）不受此限。默认 60。
	RefundNoOutputClientGoneMinSeconds int64 `json:"refund_no_output_client_gone_min_seconds"`
	// 钱包信任旁路阈值（美元）。>0 时，余额扣除本次预估后仍不低于该值、且令牌为
	// 无限额度的请求跳过预扣（不写 users 行、不落凭据），结算走批量聚合，消除
	// 高频用户的 users.quota 单行热点。0 = 关闭（默认，保持逐请求预扣）。
	WalletTrustQuotaUsd float64 `json:"wallet_trust_quota_usd"`
}

// 默认配置
var quotaSetting = QuotaSetting{
	EnableFreeModelPreConsume:          true,
	BillingRefundWhenNoOutput:          true,
	RefundNoOutputClientGoneMinSeconds: 60,
	WalletTrustQuotaUsd:                0,
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("quota_setting", &quotaSetting)
}

func GetQuotaSetting() *QuotaSetting {
	return &quotaSetting
}
