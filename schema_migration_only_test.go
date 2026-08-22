package main

import "testing"

func TestSchemaMigrationOnlyEnabled(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		enabled bool
		wantErr bool
	}{
		{name: "unset"},
		{name: "false", value: "false"},
		{name: "true", value: "true", enabled: true},
		{name: "trimmed", value: " true ", enabled: true},
		{name: "reject ambiguous one", value: "1", wantErr: true},
		{name: "reject mixed case", value: "TRUE", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SCHEMA_MIGRATION_ONLY", test.value)
			enabled, err := schemaMigrationOnlyEnabled()
			if (err != nil) != test.wantErr {
				t.Fatalf("schemaMigrationOnlyEnabled() error = %v, wantErr %v", err, test.wantErr)
			}
			if enabled != test.enabled {
				t.Fatalf("schemaMigrationOnlyEnabled() = %v, want %v", enabled, test.enabled)
			}
		})
	}
}
