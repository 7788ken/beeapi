package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
)

// redactChannelMetrics 是纯函数（不碰 DB），直接断言字段剥离面。
func TestRedactChannelMetricsStripsSensitiveFields(t *testing.T) {
	score := 88
	prev := 90
	ch := &model.Channel{
		RatioDetail:      `{"n":2,"min":0.8,"max":1.2}`,
		RatioUpCount:     2,
		RatioDownCount:   1,
		RatioChangedAt:   1725200000,
		QualityScore:     &score,
		QualityUpdatedAt: 1725200001,
		QualityDetail:    `{"success_cnt":10}`,
		VerifyScore:      &score,
		VerifyGrade:      "A",
		VerifyTestedAt:   1725200002,
		VerifyPrevScore:  &prev,
	}
	redactChannelMetrics([]*model.Channel{ch, nil})

	assert.Empty(t, ch.RatioDetail)
	assert.Zero(t, ch.RatioUpCount)
	assert.Zero(t, ch.RatioDownCount)
	assert.Zero(t, ch.RatioChangedAt)
	assert.Nil(t, ch.QualityScore)
	assert.Zero(t, ch.QualityUpdatedAt)
	assert.Empty(t, ch.QualityDetail)
	assert.Nil(t, ch.VerifyScore)
	assert.Empty(t, ch.VerifyGrade)
	assert.Zero(t, ch.VerifyTestedAt)
	assert.Nil(t, ch.VerifyPrevScore)
}

// 备注剥离仅作用于列表接口：非 root 不看备注（root 恒见，单取接口不剥）。
func TestRedactChannelRemarksNilOutRemark(t *testing.T) {
	remark := "synced from sub_site #1 (example.com)"
	ch := &model.Channel{Remark: &remark}
	redactChannelRemarks([]*model.Channel{ch, nil})
	assert.Nil(t, ch.Remark)
}
