package config

import "testing"

func TestMCPConfigRedactDefault(t *testing.T) {
	// Zero value (nil pointer) → redaction on by default.
	if !(MCPConfig{}).RedactConfigSecretsEnabled() {
		t.Error("zero-value MCPConfig should default to redaction enabled")
	}
	f := false
	if (MCPConfig{RedactConfigSecrets: &f}).RedactConfigSecretsEnabled() {
		t.Error("explicit false should disable redaction")
	}
	tr := true
	if !(MCPConfig{RedactConfigSecrets: &tr}).RedactConfigSecretsEnabled() {
		t.Error("explicit true should enable redaction")
	}
}
