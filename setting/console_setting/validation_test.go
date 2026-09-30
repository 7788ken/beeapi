package console_setting

import (
	"strings"
	"testing"
)

func TestValidateSupportWidgetCode(t *testing.T) {
	cases := []struct {
		name    string
		code    string
		wantErr bool
	}{
		{name: "empty disables widget", code: "", wantErr: false},
		{name: "external script", code: `<script src="https://desk.9manager.com/widget.js" data-site="Example" async></script>`, wantErr: false},
		{name: "inline loader", code: `<script>window.$crisp=[];window.CRISP_WEBSITE_ID="x";</script>`, wantErr: false},
		{name: "max length", code: strings.Repeat("a", SupportWidgetCodeMaxLen), wantErr: false},
		{name: "max length counts characters not bytes", code: strings.Repeat("客", SupportWidgetCodeMaxLen), wantErr: false},
		{name: "too long", code: strings.Repeat("a", SupportWidgetCodeMaxLen+1), wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateConsoleSettings(tc.code, "SupportWidgetCode")
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateConsoleSettings(len=%d) error = %v, wantErr %v", len(tc.code), err, tc.wantErr)
			}
		})
	}
}
