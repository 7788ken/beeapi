package service

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"github.com/QuantumNous/new-api/model"
)

// channel_quality_history.go — 渠道质量历史报表（列表质量分 hover → 查看更多弹窗）。
// 汇总单渠道任意时间范围内的：按桶趋势（请求量/错误率/平均耗时）、错误码分布、
// 错误模型分布、首包延迟均值。SQL 只做标量聚合，错误码与 frt 在 Go 端解析
// （复用 channel_metrics.go 的 parseStatusCode / containsTimeout / parseFrtFromOther）。

const (
	qualityHistoryMaxRangeSec   = 92 * 86400 // 最长 92 天（覆盖"按月+自定义"）
	qualityHistoryErrSampleCap  = 5000       // 错误码解析取样上限
	qualityHistoryFrtSampleCap  = 2000       // frt 取样上限
	qualityHistoryErrModelLimit = 20
)

type QualityHistoryTotals struct {
	SuccessCnt   int64 `json:"success_cnt"`
	ErrorCnt     int64 `json:"error_cnt"`
	AvgUseTimeMs int64 `json:"avg_use_time_ms"` // 成功请求加权平均
	AvgFrtMs     int64 `json:"avg_frt_ms"`      // 0=无流式样本
	FrtSamples   int   `json:"frt_samples"`
}

type QualityHistoryResult struct {
	BucketSeconds     int64                        `json:"bucket_seconds"`
	Buckets           []model.QualityHistoryBucket `json:"buckets"`
	ErrorCodes        map[string]int               `json:"error_codes"` // 键为真实状态码；0+timeout 归 "timeout"，其余归 "unknown"
	ErrorCodesSampled bool                         `json:"error_codes_sampled"`
	ErrorModels       []model.QualityErrorModelRow `json:"error_models"`
	Totals            QualityHistoryTotals         `json:"totals"`
}

// qualityHistoryBucketSec 根据范围选桶宽：≤72h 按小时，≤14d 按 6h，更长按天。
func qualityHistoryBucketSec(rangeSec int64) int64 {
	switch {
	case rangeSec <= 72*3600:
		return 3600
	case rangeSec <= 14*86400:
		return 6 * 3600
	default:
		return 86400
	}
}

// GetChannelQualityHistory 组装单渠道质量历史报表。
func GetChannelQualityHistory(ctx context.Context, channelId int, startTs, endTs, tzOffsetSec int64) (*QualityHistoryResult, error) {
	if endTs <= startTs {
		return nil, fmt.Errorf("invalid range: end must be greater than start")
	}
	if endTs-startTs > qualityHistoryMaxRangeSec {
		return nil, fmt.Errorf("range too large: max %d days", qualityHistoryMaxRangeSec/86400)
	}

	bucketSec := qualityHistoryBucketSec(endTs - startTs)
	buckets, err := model.GetChannelQualityBuckets(ctx, channelId, startTs, endTs, bucketSec, tzOffsetSec)
	if err != nil {
		return nil, fmt.Errorf("bucket query failed: %w", err)
	}

	errModels, err := model.GetChannelErrorModelDist(ctx, channelId, startTs, endTs, qualityHistoryErrModelLimit)
	if err != nil {
		return nil, fmt.Errorf("error model query failed: %w", err)
	}

	errContents, err := model.GetChannelErrorContents(ctx, channelId, startTs, endTs, qualityHistoryErrSampleCap)
	if err != nil {
		return nil, fmt.Errorf("error content query failed: %w", err)
	}
	errorCodes := make(map[string]int)
	for _, content := range errContents {
		code := parseStatusCode(content)
		switch {
		case code > 0:
			errorCodes[strconv.Itoa(code)]++
		case containsTimeout(content):
			errorCodes["timeout"]++
		default:
			errorCodes["unknown"]++
		}
	}

	frtRows, err := model.GetChannelFrtSamples(ctx, channelId, startTs, endTs, qualityHistoryFrtSampleCap)
	if err != nil {
		return nil, fmt.Errorf("frt sample query failed: %w", err)
	}
	frtSum, frtCnt := 0.0, 0
	for _, other := range frtRows {
		if frt, ok := parseFrtFromOther(other); ok && frt > 0 {
			frtSum += frt
			frtCnt++
		}
	}

	totals := QualityHistoryTotals{FrtSamples: frtCnt}
	useTimeWeighted := 0.0
	for _, b := range buckets {
		totals.SuccessCnt += b.SuccessCnt
		totals.ErrorCnt += b.ErrorCnt
		useTimeWeighted += b.AvgUseTime * float64(b.SuccessCnt)
	}
	if totals.SuccessCnt > 0 {
		totals.AvgUseTimeMs = int64(math.Round(useTimeWeighted / float64(totals.SuccessCnt) * 1000))
	}
	if frtCnt > 0 {
		totals.AvgFrtMs = int64(math.Round(frtSum / float64(frtCnt)))
	}

	return &QualityHistoryResult{
		BucketSeconds:     bucketSec,
		Buckets:           buckets,
		ErrorCodes:        errorCodes,
		ErrorCodesSampled: len(errContents) >= qualityHistoryErrSampleCap,
		ErrorModels:       errModels,
		Totals:            totals,
	}, nil
}
