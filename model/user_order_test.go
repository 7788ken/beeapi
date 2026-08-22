package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// buildUserOrderClause 白名单纯逻辑测试。
// 关键：任何不在白名单的输入都必须回退到 "id desc"，防 SQL 注入。

func TestBuildUserOrderClause_Allowlist(t *testing.T) {
	cases := []struct {
		name    string
		orderBy string
		order   string
		want    string
	}{
		{"default", "", "", "id desc"},
		{"unknown column rejected", "foo", "desc", "id desc"},
		{"sql injection rejected", "id; DROP TABLE users--", "desc", "id desc"},
		{"valid rpm desc", "rpm_24h", "desc", "rpm_24h desc, id desc"},
		{"valid rpm asc", "rpm_24h", "asc", "rpm_24h asc, id desc"},
		{"order empty falls back to desc", "rpm_24h", "", "rpm_24h desc, id desc"},
		{"unknown order falls back to desc", "rpm_24h", "random", "rpm_24h desc, id desc"},
		{"created_at allowed", "created_at", "desc", "created_at desc, id desc"},
		{"last_login_at allowed", "last_login_at", "asc", "last_login_at asc, id desc"},
		{"used_quota allowed", "used_quota", "desc", "used_quota desc, id desc"},
		{"request_count allowed", "request_count", "desc", "request_count desc, id desc"},
		{"quota allowed", "quota", "desc", "quota desc, id desc"},
		{"password column rejected (sensitive)", "password", "desc", "id desc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildUserOrderClause(tc.orderBy, tc.order)
			assert.Equal(t, tc.want, got)
		})
	}
}
