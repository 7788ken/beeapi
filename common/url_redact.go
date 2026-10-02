package common

import (
	"net/url"
	"strings"
)

// RedactURL 去掉网址里的用户信息、查询串和片段再用于日志或报错（上游渠道密钥常以 ?key= 等形式放在网址里）；无法解析时只按第一个 ? 截掉查询串
func RedactURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		base, _, _ := strings.Cut(rawURL, "?")
		return base
	}
	u.User = nil
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	u.RawFragment = ""
	return u.String()
}

// RedactURLError 处理发请求或解析网址失败时的 *url.Error（Error() 会带完整网址），保留 Op 和原因，错误分类不受影响
func RedactURLError(err error) error {
	urlErr, ok := err.(*url.Error)
	if !ok {
		return err
	}
	return &url.Error{Op: urlErr.Op, URL: RedactURL(urlErr.URL), Err: urlErr.Err}
}
