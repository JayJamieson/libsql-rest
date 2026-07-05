package config

import (
	"fmt"
	"strings"
)

// Driver identifies which database/sql driver backs the server.
type Driver string

const (
	// DriverLibSQL targets a libsql/Turso database (local `turso dev` or remote).
	DriverLibSQL Driver = "libsql"
	// DriverSQLite targets a plain SQLite database via the pure-Go modernc driver.
	// Useful for local development and tests without a running libsql server.
	DriverSQLite Driver = "sqlite"
)

// DefaultMaxPageSize bounds how many rows a list endpoint returns when the
// caller does not specify a limit, and caps explicit limits.
const DefaultMaxPageSize = 100

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	Host string
	Port int
}

// DBConfig holds database connection settings.
type DBConfig struct {
	// Driver selects the backing database. Defaults to sqlite when empty.
	Driver Driver
	// URI is the connection string. For sqlite this is a file path or
	// "file:...:memory:" DSN; for libsql it is an http(s)/libsql URL.
	URI string
	// Token is the libsql auth token (ignored by the sqlite driver).
	Token string
}

// AuthConfig holds JWT authentication settings.
type AuthConfig struct {
	// Enabled turns on JWT validation for the API. When false the API is open
	// and every request is treated as anonymous.
	Enabled bool
	// Algorithm is the expected signing algorithm (default HS256). HS* uses
	// Secret; RS* uses PublicKeyPath.
	Algorithm string
	// Secret is the HMAC signing key used to verify HS* tokens.
	Secret string
	// PublicKeyPath is the path to a PEM RSA public key used to verify RS*
	// tokens. This is the recommended production mode: the server can verify
	// tokens without holding a key capable of minting them.
	PublicKeyPath string
	// Issuer and Audience are validated against the token's `iss`/`aud` claims.
	Issuer   string
	Audience string
	// Optional, when true, lets requests without a token through as anonymous
	// rather than rejecting them. This is the seam row-level security will use.
	Optional bool
}

// IsRSA reports whether the configured algorithm is an RSA family algorithm.
func (a AuthConfig) IsRSA() bool {
	return strings.HasPrefix(strings.ToUpper(a.Algorithm), "RS")
}

// Config is the fully-resolved application configuration.
type Config struct {
	Server ServerConfig
	DB     DBConfig
	Auth   AuthConfig
	// MaxPageSize caps rows returned by list endpoints.
	MaxPageSize int
	// AllowTables, when non-empty, restricts the API to these tables/views.
	// An empty list exposes every table and view in the database.
	AllowTables []string
}

// Default issuer/audience used when auth is enabled but none is configured, so
// the server works out of the box for self-hosted single-tenant setups.
const (
	DefaultIssuer    = "libsql-rest"
	DefaultAudience  = "libsql-rest"
	DefaultAlgorithm = "HS256"
)

// Default returns a Config populated with sensible defaults.
func Default() Config {
	return Config{
		Server:      ServerConfig{Host: "", Port: 8080},
		DB:          DBConfig{Driver: DriverSQLite, URI: "file:libsql.db"},
		MaxPageSize: DefaultMaxPageSize,
	}
}

// Validate normalizes defaults and returns an error if the config is unusable.
func (c *Config) Validate() error {
	if c.DB.Driver == "" {
		c.DB.Driver = DriverSQLite
	}
	switch c.DB.Driver {
	case DriverLibSQL, DriverSQLite:
	default:
		return fmt.Errorf("unsupported db driver %q (want %q or %q)", c.DB.Driver, DriverSQLite, DriverLibSQL)
	}
	if strings.TrimSpace(c.DB.URI) == "" {
		return fmt.Errorf("db uri is required")
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port %d", c.Server.Port)
	}
	if c.MaxPageSize <= 0 {
		c.MaxPageSize = DefaultMaxPageSize
	}
	if c.Auth.Enabled {
		if c.Auth.Algorithm == "" {
			c.Auth.Algorithm = DefaultAlgorithm
		}
		if c.Auth.IsRSA() {
			if strings.TrimSpace(c.Auth.PublicKeyPath) == "" {
				return fmt.Errorf("auth algorithm %s requires auth.public_key_path", c.Auth.Algorithm)
			}
		} else if strings.TrimSpace(c.Auth.Secret) == "" {
			return fmt.Errorf("auth algorithm %s requires auth.secret", c.Auth.Algorithm)
		}
		if c.Auth.Issuer == "" {
			c.Auth.Issuer = DefaultIssuer
		}
		if c.Auth.Audience == "" {
			c.Auth.Audience = DefaultAudience
		}
	}
	return nil
}

// IsTableAllowed reports whether the given table/view may be exposed.
func (c *Config) IsTableAllowed(name string) bool {
	if len(c.AllowTables) == 0 {
		return true
	}
	for _, t := range c.AllowTables {
		if strings.EqualFold(t, name) {
			return true
		}
	}
	return false
}
