package types

// 错误归属分类：用于「分组可用性」指标判定一次失败该不该算在上游头上。
//
// 可用性回答的是"上游渠道还活着、能访问吗"，所以只有上游侧的故障才进分母：
//   - AttributionUpstream 上游故障 → 计入分母，记为失败
//   - AttributionClient   客户端自己的错（参数非法/余额不足/无权限/敏感词）→ 完全不计入
//   - AttributionGateway  网关自身故障（DB/序列化/计价配置）→ 完全不计入，避免自家 bug
//     污染"上游是否可用"的语义
//
// 刻意不复用 service.ShouldDisableChannel：那个判定依赖运行时可配的状态码区间和关键词表，
// 管理员改一次自动禁用配置就会让指标口径漂移、历史数据前后不可比。指标语义必须写死在代码里。
type ErrorAttribution int

const (
	AttributionUpstream ErrorAttribution = iota
	AttributionClient
	AttributionGateway
)

// upstreamAttributedStatusCodes 上游返回这些码时算它自己有问题。
// 401/403（key 失效/欠费/被封）与 429（限流）严格说是渠道配置或配额问题而非"进程挂了"，
// 但从"这个分组现在能不能用"的角度结果一致，故归入上游故障。
func isUpstreamAttributedStatusCode(code int) bool {
	if code >= 500 && code <= 599 {
		return true
	}
	switch code {
	case 401, 403, 404, 408, 429:
		// 404 出现在 ErrorCodeBadResponseStatusCode 时表示上游把该模型下线了；
		// 网关侧"本站没配这个模型"用的是 ErrorCodeModelNotFound，不走这里。
		return true
	}
	return false
}

// ClassifyErrorAttribution 判定一个 relay 错误该由谁负责。
func ClassifyErrorAttribution(err *NewAPIError) ErrorAttribution {
	if err == nil {
		return AttributionUpstream
	}

	// channel:* 一律是渠道侧问题（key 用尽、key 无效、响应超时、参数覆盖配错等）
	if IsChannelError(err) {
		return AttributionUpstream
	}

	switch err.GetErrorCode() {
	// ── 上游侧：连不上 / 没响应 / 超时 ──
	case ErrorCodeDoRequestFailed,
		ErrorCodeUpstreamNoResponse,
		ErrorCodeUpstreamTimeout,
		ErrorCodeRelayDeadlineExceeded:
		return AttributionUpstream

	// ── 上游侧：响应回来了但是坏的 ──
	case ErrorCodeBadResponse,
		ErrorCodeBadResponseBody,
		ErrorCodeEmptyResponse,
		ErrorCodeReadResponseBodyFailed,
		ErrorCodeAwsInvokeError:
		return AttributionUpstream

	// ── 上游侧：返回了非 2xx，按状态码归属 ──
	case ErrorCodeBadResponseStatusCode:
		if isUpstreamAttributedStatusCode(err.StatusCode) {
			return AttributionUpstream
		}
		// 上游用 400/422 拒了非法参数 → 客户端的锅
		return AttributionClient

	// ── 客户端侧 ──
	case ErrorCodeInvalidRequest,
		ErrorCodeBadRequestBody,
		ErrorCodeReadRequestBodyFailed,
		ErrorCodeConvertRequestFailed,
		ErrorCodeAccessDenied,
		ErrorCodeSensitiveWordsDetected,
		ErrorCodePromptBlocked,
		ErrorCodeViolationFeeGrokCSAM,
		ErrorCodeInsufficientUserQuota,
		ErrorCodePreConsumeTokenQuotaFailed,
		ErrorCodeTokenErrorRateLimited,
		ErrorCodeModelNotFound:
		return AttributionClient

	// ── 网关自身 ──
	case ErrorCodeCountTokenFailed,
		ErrorCodeModelPriceError,
		ErrorCodeInvalidApiType,
		ErrorCodeJsonMarshalFailed,
		ErrorCodeGetChannelFailed,
		ErrorCodeGenRelayInfoFailed,
		ErrorCodeQueryDataError,
		ErrorCodeUpdateDataError:
		return AttributionGateway
	}

	// 未登记的错误码按状态码兜底：5xx/401/403/429 算上游，其余 4xx 算客户端。
	if isUpstreamAttributedStatusCode(err.StatusCode) {
		return AttributionUpstream
	}
	if err.StatusCode >= 400 && err.StatusCode < 500 {
		return AttributionClient
	}
	return AttributionUpstream
}

// IsUpstreamAttributedError 该错误是否应计入「分组可用性」的失败数。
func IsUpstreamAttributedError(err *NewAPIError) bool {
	return ClassifyErrorAttribution(err) == AttributionUpstream
}
