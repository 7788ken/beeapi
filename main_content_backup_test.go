package main

import "testing"

func TestContentBackupModuleEnabled(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		enabled bool
		wantErr bool
	}{
		{name: "unset defaults to on", enabled: true},
		{name: "on", value: "on", enabled: true},
		{name: "off", value: "off"},
		{name: "trimmed", value: " off ", enabled: false},
		{name: "reject bool spelling", value: "false", wantErr: true},
		{name: "reject mixed case", value: "OFF", wantErr: true},
		{name: "reject ambiguous zero", value: "0", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CONTENT_BACKUP_MODULE", test.value)
			enabled, err := contentBackupModuleEnabled()
			if (err != nil) != test.wantErr {
				t.Fatalf("contentBackupModuleEnabled() error = %v, wantErr %v", err, test.wantErr)
			}
			if enabled != test.enabled {
				t.Fatalf("contentBackupModuleEnabled() = %v, want %v", enabled, test.enabled)
			}
		})
	}
}
