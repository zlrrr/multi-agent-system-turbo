package config

import (
	"strings"
	"testing"
)

func tenantedConfig() *Config {
	c := Default()
	c.Targets = []TargetConfig{
		{ID: "payments-redis", Kind: "redis", Tenant: "payments"},
		{ID: "search-kafka", Kind: "kafka", Tenant: "search"},
	}
	c.Server.Auth = ServerAuth{Tokens: []APIToken{
		{Name: "payments-oncall", Token: "a", Scopes: []string{"read", "diagnose"},
			Tenants: []string{"payments"}},
		{Name: "platform", Token: "b", Scopes: []string{"read"},
			Tenants: []string{"payments", "search"}},
	}}
	return c
}

// TestTargetsCarryATenant is FR-001, and the shape of the answer: tenancy is
// read off the configuration rather than declared separately.
func TestTargetsCarryATenant(t *testing.T) {
	c := tenantedConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("a consistent multi-tenant configuration was refused: %v", err)
	}
	if !c.MultiTenant() {
		t.Error("a configuration whose targets name tenants is not multi-tenant")
	}
	if got := c.Tenants(); len(got) != 2 || got[0] != "payments" || got[1] != "search" {
		t.Errorf("tenants %v, want [payments search] sorted", got)
	}

	tenant, known := c.TenantOf("search-kafka")
	if !known || tenant != "search" {
		t.Errorf("TenantOf(search-kafka) = %q, %v", tenant, known)
	}
	// An unknown target and a target with no tenant are different answers.
	// Conflating them would make an unconfigured id look like everyone's.
	if _, known := c.TenantOf("nothing-here"); known {
		t.Error("an unconfigured id was reported as a known target")
	}
}

// TestTenancyOffChangesNothing is FR-002 and NFR-003: most deployments are one
// team's, and they must never learn any of this exists.
func TestTenancyOffChangesNothing(t *testing.T) {
	c := Default()
	c.Targets = []TargetConfig{
		{ID: "redis-prod", Kind: "redis"},
		{ID: "kafka-prod", Kind: "kafka"},
	}
	c.Server.Auth = ServerAuth{Tokens: []APIToken{
		{Name: "oncall", Token: "a", Scopes: []string{"read", "diagnose"}},
	}}

	if err := c.Validate(); err != nil {
		t.Fatalf("a single-tenant configuration was refused: %v", err)
	}
	if c.MultiTenant() {
		t.Error("a configuration with no tenants reported itself multi-tenant")
	}
	if got := c.Tenants(); len(got) != 0 {
		t.Errorf("tenants %v, want none", got)
	}
	if tenant, known := c.TenantOf("redis-prod"); !known || tenant != "" {
		t.Errorf("TenantOf on an untenanted target = %q, %v", tenant, known)
	}
}

// TestPartialTenancyIsRefused is FR-003. A target belonging to nobody in a
// partitioned deployment is one everyone or no one can reach, and either answer
// is a guess the tool should not make.
func TestPartialTenancyIsRefused(t *testing.T) {
	c := tenantedConfig()
	c.Targets = append(c.Targets, TargetConfig{ID: "orphan-mongo", Kind: "mongodb"})

	err := c.Validate()
	if err == nil {
		t.Fatal("a half-tenanted configuration was accepted")
	}
	// Validate aggregates: it reports MAS-1003 with the specific code inside,
	// which is how every other coded configuration problem already surfaces.
	if !strings.Contains(err.Error(), "MAS-1013") {
		t.Errorf("the error does not carry MAS-1013: %v", err)
	}
	if !strings.Contains(err.Error(), "orphan-mongo") {
		t.Errorf("the error does not name the untenanted target: %v", err)
	}
}

// TestCredentialWithoutTenantsIsRefused is FR-004 and CON-001: an unrestricted
// credential in a partitioned deployment is a superuser nobody declared.
func TestCredentialWithoutTenantsIsRefused(t *testing.T) {
	c := tenantedConfig()
	c.Server.Auth.Tokens = append(c.Server.Auth.Tokens,
		APIToken{Name: "unscoped", Token: "c", Scopes: []string{"read"}})

	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "MAS-1013") {
		t.Fatalf("an unrestricted credential was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "unscoped") {
		t.Errorf("the error does not name the credential: %v", err)
	}
}

// TestCredentialNamingAnUnknownTenantIsRefused is FR-012. The second half
// matters as much: a `tenants:` list that has no effect because nothing is
// tenanted is exactly the shape of a control that looks applied and is not.
func TestCredentialNamingAnUnknownTenantIsRefused(t *testing.T) {
	c := tenantedConfig()
	c.Server.Auth.Tokens[0].Tenants = []string{"payments", "billing"}
	err := c.Validate()
	if err == nil || !strings.Contains(err.Error(), "MAS-1013") ||
		!strings.Contains(err.Error(), "billing") {
		t.Errorf("a credential naming an undeclared tenant was accepted: %v", err)
	}

	// Tenants declared on a credential when no target is tenanted would be
	// silently ignored, which is worse than being wrong loudly.
	off := Default()
	off.Targets = []TargetConfig{{ID: "redis-prod", Kind: "redis"}}
	off.Server.Auth = ServerAuth{Tokens: []APIToken{
		{Name: "oncall", Token: "a", Scopes: []string{"read"}, Tenants: []string{"payments"}},
	}}
	err = off.Validate()
	if err == nil || !strings.Contains(err.Error(), "MAS-1013") {
		t.Errorf("a restriction that would be ignored was accepted: %v", err)
	}
	if !strings.Contains(err.Error(), "silently ignored") {
		t.Errorf("the error does not say why it matters: %v", err)
	}
}
