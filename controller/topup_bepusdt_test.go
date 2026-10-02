package controller

import "testing"

// 回调法币金额与订单金额必须在 2 位小数内一致，否则拒绝入账。
func TestBepusdtAmountMatches(t *testing.T) {
	if !bepusdtAmountMatches(10, 10.0) || !bepusdtAmountMatches(28.88, 28.88) || !bepusdtAmountMatches(0.1+0.2, 0.3) {
		t.Errorf("相等金额应匹配")
	}
	if bepusdtAmountMatches(10, 10.01) || bepusdtAmountMatches(1000, 10) || bepusdtAmountMatches(0, 10) {
		t.Errorf("不等金额不应匹配")
	}
}
