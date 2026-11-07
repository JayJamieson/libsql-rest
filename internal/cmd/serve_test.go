package cmd

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/JayJamieson/libsql-rest/internal/config"
)

func TestLoadConfigPrecedence(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.yaml")
	yaml := `server:
  port: 9000
  max_page_size: 50
db:
  uri: file:from-file.db
  token: from-file
allow_tables: [users, posts]
`
	if err := os.WriteFile(file, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LIBSQL_REST_SERVER_PORT", "9001")
	t.Setenv("LIBSQL_REST_DB_URI", "file:from-env.db")

	cmd := newServeCmd()
	if err := cmd.ParseFlags([]string{"--config", file, "--port", "9002"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(cmd.Flags())
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Server.Port != 9002 {
		t.Errorf("port = %d, want the flag value 9002", cfg.Server.Port)
	}
	if cfg.DB.URI != "file:from-env.db" {
		t.Errorf("uri = %q, want the env value", cfg.DB.URI)
	}
	if cfg.DB.Token != "from-file" || cfg.MaxPageSize != 50 {
		t.Errorf("token = %q, max page size = %d, want the file values", cfg.DB.Token, cfg.MaxPageSize)
	}
	if !slices.Equal(cfg.AllowTables, []string{"users", "posts"}) {
		t.Errorf("allow tables = %v", cfg.AllowTables)
	}
	if cfg.DB.Driver != config.DriverSQLite {
		t.Errorf("driver = %q, want the default", cfg.DB.Driver)
	}
}

func TestLoadConfigAllowTablesFromEnv(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LIBSQL_REST_ALLOW_TABLES", "users,posts comments")
	cfg, err := loadConfig(newServeCmd().Flags())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"users", "posts", "comments"}; !slices.Equal(cfg.AllowTables, want) {
		t.Errorf("allow tables = %q, want %q", cfg.AllowTables, want)
	}
}

func TestLoadConfigRejectsInvalid(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cmd := newServeCmd()
	if err := cmd.ParseFlags([]string{"--auth"}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(cmd.Flags()); err == nil {
		t.Error("expected an error for --auth without a secret")
	}
}

func TestLoadConfigMissingExplicitFile(t *testing.T) {
	cmd := newServeCmd()
	if err := cmd.ParseFlags([]string{"--config", filepath.Join(t.TempDir(), "missing.yaml")}); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(cmd.Flags()); err == nil {
		t.Error("expected an error for a config file that does not exist")
	}
}
