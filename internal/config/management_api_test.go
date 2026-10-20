package config

import (
	"strings"
	"testing"
)

func TestManagementAPIConfig(t *testing.T) {
	for _, tc := range []struct {
		name, token string
		valid       bool
	}{
		{"disabled", "", true},
		{"minimum", strings.Repeat("a", 32), true},
		{"random hex format", strings.Repeat("ab12", 16), true},
		{"short", strings.Repeat("a", 31), false},
		{"whitespace only", strings.Repeat(" ", 32), false},
		{"space", strings.Repeat("a", 32) + " ", false},
		{"newline", strings.Repeat("a", 32) + "\n", false},
		{"non ASCII", strings.Repeat("é", 32), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{ManagementAPIToken: tc.token}
			err := c.ValidateManagementAPI()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, error=%v", tc.valid, err)
			}
			if err != nil && tc.token != "" && strings.Contains(err.Error(), tc.token) {
				t.Fatal("validation error contains secret")
			}
			cAdmin := Config{AdminAPIToken: tc.token}
			errAdmin := cAdmin.ValidateManagementAPI()
			if (errAdmin == nil) != tc.valid {
				t.Fatalf("admin valid=%v, error=%v", tc.valid, errAdmin)
			}
			if errAdmin != nil && tc.token != "" && strings.Contains(errAdmin.Error(), tc.token) {
				t.Fatal("admin validation error contains secret")
			}
		})
	}
	dup := strings.Repeat("x", 32)
	if err := (Config{ManagementAPIToken: dup, AdminAPIToken: dup}).ValidateManagementAPI(); err == nil {
		t.Fatal("identical management and admin tokens accepted")
	}
	if err := (Config{ManagementAPIToken: strings.Repeat("x", 32), AdminAPIToken: strings.Repeat("y", 32)}).ValidateManagementAPI(); err != nil {
		t.Fatalf("distinct tokens rejected: %v", err)
	}
}

func TestManagementAPITokenLoad(t *testing.T) {
	// Package tests run outside the root .env; explicitly isolate relevant inputs.
	t.Setenv("MANAGEMENT_API_TOKEN", strings.Repeat("a", 64))
	t.Setenv("ADMIN_API_TOKEN", strings.Repeat("b", 64))
	t.Setenv("SSO_ENABLED", "false")
	c, err := Load()
	if err != nil || c.ManagementAPIToken != strings.Repeat("a", 64) || c.AdminAPIToken != strings.Repeat("b", 64) {
		t.Fatalf("load failed: %v", err)
	}
	t.Setenv("MANAGEMENT_API_TOKEN", "short")
	if _, err := Load(); err == nil {
		t.Fatal("short configured token accepted")
	}
	t.Setenv("MANAGEMENT_API_TOKEN", strings.Repeat("a", 64))
	t.Setenv("ADMIN_API_TOKEN", "short")
	if _, err := Load(); err == nil {
		t.Fatal("short configured admin token accepted")
	}
}
