package config

import "testing"

func TestValidateDefaults(t *testing.T) {
	c := Default()
	if err := c.Validate(); err != nil {
		t.Fatalf("default config invalid: %v", err)
	}
	if c.DB.Driver != DriverSQLite {
		t.Errorf("driver = %q", c.DB.Driver)
	}
	if c.MaxPageSize != DefaultMaxPageSize {
		t.Errorf("max page size = %d", c.MaxPageSize)
	}
}

func TestValidateErrors(t *testing.T) {
	cases := map[string]func(*Config){
		"bad driver":          func(c *Config) { c.DB.Driver = "postgres" },
		"empty uri":           func(c *Config) { c.DB.URI = "" },
		"bad port":            func(c *Config) { c.Server.Port = 0 },
		"empty driver":        func(c *Config) { c.DB.Driver = "" },
		"zero page size":      func(c *Config) { c.MaxPageSize = 0 },
		"auth without secret": func(c *Config) { c.Auth.Enabled = true },
		"rsa without key": func(c *Config) {
			c.Auth.Enabled, c.Auth.Algorithm = true, "RS256"
		},
		"auth without issuer": func(c *Config) {
			c.Auth.Enabled, c.Auth.Secret, c.Auth.Issuer = true, "s", ""
		},
	}
	for name, mutate := range cases {
		c := Default()
		mutate(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestIsTableAllowed(t *testing.T) {
	empty := Default()
	if !empty.IsTableAllowed("anything") {
		t.Error("empty allow list should allow everything")
	}

	restricted := Default()
	restricted.AllowTables = []string{"users", "Posts"}
	if !restricted.IsTableAllowed("users") || !restricted.IsTableAllowed("posts") {
		t.Error("allow list should match case-insensitively")
	}
	if restricted.IsTableAllowed("secret") {
		t.Error("secret should not be allowed")
	}
}
