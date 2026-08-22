package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func setAllowIntranet(t *testing.T, v string) {
	t.Helper()
	prev := common.OptionMap["SubSiteAllowIntranet"]
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMap["SubSiteAllowIntranet"] = v
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap["SubSiteAllowIntranet"] = prev
		common.OptionMapRWMutex.Unlock()
	})
}

func TestValidateSubSiteBaseURL(t *testing.T) {
	setAllowIntranet(t, "false")
	cases := []struct {
		name   string
		raw    string
		expect error
	}{
		{"empty", "", ErrSubSiteMalformedURL},
		{"bad scheme ftp", "ftp://example.com", ErrSubSiteBadScheme},
		{"bad scheme file", "file:///etc/passwd", ErrSubSiteBadScheme},
		{"loopback v4", "http://127.0.0.1:3000", ErrSubSitePrivateAddr},
		{"private v4 10.x", "http://10.0.0.5", ErrSubSitePrivateAddr},
		{"private v4 192.168", "http://192.168.1.1", ErrSubSitePrivateAddr},
		{"private v4 172.16", "http://172.16.5.5", ErrSubSitePrivateAddr},
		{"link local 169.254", "http://169.254.169.254/", ErrSubSitePrivateAddr},
		{"loopback v6", "http://[::1]/", ErrSubSitePrivateAddr},
		{"v4 mapped v6", "http://[::ffff:127.0.0.1]/", ErrSubSitePrivateAddr},
		{"unique local v6", "http://[fc00::1]/", ErrSubSitePrivateAddr},
		{"link local v6", "http://[fe80::1]/", ErrSubSitePrivateAddr},
		{"public dns ok", "https://api.openai.com/", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSubSiteBaseURL(tc.raw)
			if tc.expect == nil {
				if err != nil {
					t.Fatalf("want nil err, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want %v, got nil", tc.expect)
			}
			if !errors.Is(err, tc.expect) && !strings.Contains(err.Error(), tc.expect.Error()) {
				t.Fatalf("want err %v, got %v", tc.expect, err)
			}
		})
	}
}

func TestValidateSubSiteBaseURL_AllowIntranet(t *testing.T) {
	setAllowIntranet(t, "true")
	if err := ValidateSubSiteBaseURL("http://127.0.0.1:3000"); err != nil {
		t.Fatalf("with SubSiteAllowIntranet=true loopback should pass, got %v", err)
	}
}
