/**
此文件为旧版支付设置文件，如需增加新的参数、变量等，请在 payment_setting.go 中添加
This file is the old version of the payment settings file. If you need to add new parameters, variables, etc., please add them in payment_setting.go
*/

package operation_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

var PayAddress = ""
var CustomCallbackAddress = ""
var EpayId = ""
var EpayKey = ""
var Price = 7.3
var MinTopUp = 1
var USDExchangeRate = 7.3

var PayMethods = []map[string]string{
	{
		"name":  "支付宝",
		"color": "rgba(var(--semi-blue-5), 1)",
		"type":  "alipay",
	},
	{
		"name":  "微信",
		"color": "rgba(var(--semi-green-5), 1)",
		"type":  "wxpay",
	},
	{
		"name":      "自定义1",
		"color":     "black",
		"type":      "custom1",
		"min_topup": "50",
	},
}

func UpdatePayMethodsByJsonString(jsonString string) error {
	PayMethods = make([]map[string]string, 0)
	return common.Unmarshal([]byte(jsonString), &PayMethods)
}

func PayMethods2JsonString() string {
	jsonBytes, err := common.Marshal(PayMethods)
	if err != nil {
		return "[]"
	}
	return string(jsonBytes)
}

func ContainsPayMethod(method string) bool {
	for _, payMethod := range PayMethods {
		if payMethod["type"] == method {
			return true
		}
	}
	return false
}

// IsGroupAllowed 判断用户分组是否在 ';' 分隔的白名单内。
// 白名单为空/缺省 = 对所有分组开放；否则按 ';' 分隔逐段精确匹配（trim 后比较）。
func IsGroupAllowed(allowedGroups string, userGroup string) bool {
	allowed := strings.TrimSpace(allowedGroups)
	if allowed == "" {
		return true
	}
	for _, g := range strings.Split(allowed, ";") {
		if g = strings.TrimSpace(g); g != "" && g == userGroup {
			return true
		}
	}
	return false
}

// IsPayMethodAllowedForGroup 判断支付方式（map 中的 allowed_groups）是否对指定用户分组开放。
func IsPayMethodAllowedForGroup(payMethod map[string]string, userGroup string) bool {
	return IsGroupAllowed(payMethod["allowed_groups"], userGroup)
}
