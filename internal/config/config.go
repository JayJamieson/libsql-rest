// Package config holds the server settings, their defaults, and their validation.
package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

type Driver string

const (
	DriverLibSQL Driver = "libsql"
	DriverSQLite Driver = "sqlite"
)

const (
	DefaultMaxPageSize = 100
	DefaultAlgorithm   = "HS256"
	DefaultIssuer      = "libsql-rest"
	DefaultAudience    = "libsql-rest"
)

type Config struct {
	Server      ServerConfig
	DB          DBConfig
	Auth        AuthConfig
	MaxPageSize int
	AllowTables []string
}

type ServerConfig struct {
	Host string
	Port int
}

type DBConfig struct {
	Driver Driver
	URI    string
	Token  string
}

type AuthConfig struct {
	Enabled       bool
	Algorithm     string
	Secret        string
	PublicKeyPath string
	Issuer        string
	Audience      string
	Optional      bool
}

func (a AuthConfig) IsRSA() bool {
	return strings.HasPrefix(strings.ToUpper(a.Algorithm), "RS")
}

func Default() Config {
	return Config{
		Server:      ServerConfig{Port: 8080},
		DB:          DBConfig{Driver: DriverSQLite, URI: "file:libsql.db"},
		MaxPageSize: DefaultMaxPageSize,
		Auth: AuthConfig{
			Algorithm: DefaultAlgorithm,
			Issuer:    DefaultIssuer,
			Audience:  DefaultAudience,
		},
	}
}

// Validate reports the first setting that would stop the server from starting.
// Default supplies every default, so an empty required value here is an error.
func (c Config) Validate() error {
	switch c.DB.Driver {
	case DriverLibSQL, DriverSQLite:
	default:
		return fmt.Errorf("unsupported db driver %q (want %q or %q)", c.DB.Driver, DriverSQLite, DriverLibSQL)
	}
	if strings.TrimSpace(c.DB.URI) == "" {
		return errors.New("db uri is required")
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("invalid server port %d", c.Server.Port)
	}
	if c.MaxPageSize <= 0 {
		return fmt.Errorf("max page size must be at least 1, got %d", c.MaxPageSize)
	}
	if !c.Auth.Enabled {
		return nil
	}
	if c.Auth.Issuer == "" || c.Auth.Audience == "" {
		return errors.New("auth requires an issuer and an audience")
	}
	if c.Auth.IsRSA() {
		if strings.TrimSpace(c.Auth.PublicKeyPath) == "" {
			return fmt.Errorf("auth algorithm %s requires auth.public_key_path", c.Auth.Algorithm)
		}
	} else if strings.TrimSpace(c.Auth.Secret) == "" {
		return fmt.Errorf("auth algorithm %s requires auth.secret", c.Auth.Algorithm)
	}
	return nil
}

func (c Config) IsTableAllowed(name string) bool {
	if len(c.AllowTables) == 0 {
		return true
	}
	return slices.ContainsFunc(c.AllowTables, func(t string) bool { return strings.EqualFold(t, name) })
}
