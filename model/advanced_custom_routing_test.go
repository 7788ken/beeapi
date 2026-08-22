package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestGetChannelForRequestFiltersAdvancedCustomAfterDatabaseQuery(t *testing.T) {
	originalDB := DB
	originalUsingSQLite := common.UsingSQLite
	originalMemoryCacheEnabled := common.MemoryCacheEnabled
	originalCommonGroupCol := commonGroupCol
	t.Cleanup(func() {
		DB = originalDB
		common.UsingSQLite = originalUsingSQLite
		common.MemoryCacheEnabled = originalMemoryCacheEnabled
		commonGroupCol = originalCommonGroupCol
	})

	db, err := gorm.Open(sqlite.Open("file:advanced-custom-routing?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	DB = db
	common.UsingSQLite = true
	common.MemoryCacheEnabled = false
	commonGroupCol = "`group`"
	if err := DB.AutoMigrate(&Channel{}, &Ability{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	channel := &Channel{
		Id:     91001,
		Name:   "advanced",
		Type:   constant.ChannelTypeAdvancedCustom,
		Status: common.ChannelStatusEnabled,
		Models: "gpt-4o",
		Group:  "default",
	}
	channel.SetOtherSettings(dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/chat/completions",
				UpstreamPath: "/chat",
				Converter:    "none",
				Models:       []string{"gpt-4o"},
			},
		}},
	})
	if err := DB.Create(channel).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	priority := int64(100)
	if err := DB.Create(&Ability{
		Group:     "default",
		Model:     "gpt-4o",
		ChannelId: channel.Id,
		Enabled:   true,
		Priority:  &priority,
		Weight:    100,
	}).Error; err != nil {
		t.Fatalf("create ability: %v", err)
	}

	responsesChannel := &Channel{
		Id:     91002,
		Name:   "advanced-responses",
		Type:   constant.ChannelTypeAdvancedCustom,
		Status: common.ChannelStatusEnabled,
		Models: "gpt-4o",
		Group:  "default",
	}
	responsesChannel.SetOtherSettings(dto.ChannelOtherSettings{
		AdvancedCustom: &dto.AdvancedCustomConfig{Routes: []dto.AdvancedCustomRoute{
			{
				IncomingPath: "/v1/responses",
				UpstreamPath: "/responses",
				Converter:    "none",
				Models:       []string{"gpt-4o"},
			},
		}},
	})
	if err := DB.Create(responsesChannel).Error; err != nil {
		t.Fatalf("create responses channel: %v", err)
	}
	lowerPriority := int64(50)
	if err := DB.Create(&Ability{
		Group:     "default",
		Model:     "gpt-4o",
		ChannelId: responsesChannel.Id,
		Enabled:   true,
		Priority:  &lowerPriority,
		Weight:    100,
	}).Error; err != nil {
		t.Fatalf("create responses ability: %v", err)
	}

	selected, err := GetChannelForRequest(
		"default",
		"gpt-4o",
		0,
		nil,
		"",
		"/v1/responses",
	)
	if err != nil {
		t.Fatalf("GetChannelForRequest lower-priority path: %v", err)
	}
	if selected == nil || selected.Id != responsesChannel.Id {
		t.Fatalf("lower-priority compatible path selected %#v", selected)
	}

	selected, err = GetChannelForRequest(
		"default",
		"gpt-4o",
		0,
		nil,
		"",
		"/v1/chat/completions",
	)
	if err != nil {
		t.Fatalf("GetChannelForRequest supported path: %v", err)
	}
	if selected == nil || selected.Id != channel.Id {
		t.Fatalf("supported path selected %#v", selected)
	}
}
