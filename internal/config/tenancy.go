package config

import "sort"

// Tenancy is decided by the configuration rather than by a flag.
//
// A flag has two failure modes and no upside: off with tenants configured is a
// deployment that looks partitioned and is not, and on with no tenants is a
// configuration error that surfaces as an empty list nobody can explain. So a
// configuration is multi-tenant the moment any target names a tenant, and the
// rules that follow are enforced at load
// (specs/011-tenant-registry/design-hld.md §2).

// MultiTenant reports whether any target names a tenant.
func (c *Config) MultiTenant() bool {
	for _, t := range c.Targets {
		if t.Tenant != "" {
			return true
		}
	}
	return false
}

// Tenants lists the declared tenants, sorted and deduplicated.
func (c *Config) Tenants() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range c.Targets {
		if t.Tenant != "" && !seen[t.Tenant] {
			seen[t.Tenant] = true
			out = append(out, t.Tenant)
		}
	}
	sort.Strings(out)
	return out
}

// TenantOf returns the tenant a target belongs to, and whether the target
// exists at all. The two are separate answers: an unknown target and a target
// with no tenant are different situations, and conflating them would make an
// unconfigured id look like it belonged to everyone.
func (c *Config) TenantOf(targetID string) (string, bool) {
	for _, t := range c.Targets {
		if t.ID == targetID {
			return t.Tenant, true
		}
	}
	return "", false
}
