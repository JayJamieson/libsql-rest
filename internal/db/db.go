// Package db opens a database/sql handle for the sqlite or libsql driver.
package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/JayJamieson/libsql-rest/internal/config"

	_ "github.com/tursodatabase/libsql-client-go/libsql"
	_ "modernc.org/sqlite"
)

func Open(cfg config.DBConfig) (*sql.DB, error) {
	driver, dsn, err := resolve(cfg)
	if err != nil {
		return nil, err
	}

	sqlDB, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}
	// A libsql server expires idle streams, and reusing one fails with
	// STREAM_EXPIRED. Close idle connections before the server does.
	sqlDB.SetConnMaxIdleTime(9 * time.Second)
	return sqlDB, nil
}

func resolve(cfg config.DBConfig) (driver, dsn string, err error) {
	switch cfg.Driver {
	case config.DriverSQLite:
		return "sqlite", sqliteDSN(cfg.URI), nil
	case config.DriverLibSQL:
		return "libsql", libsqlDSN(cfg.URI, cfg.Token), nil
	default:
		return "", "", fmt.Errorf("unsupported db driver %q", cfg.Driver)
	}
}

// sqliteDSN sets a busy timeout unless the URI sets one. Without it, a request
// that finds the database locked by another request's write fails at once
// with SQLITE_BUSY instead of waiting for the lock.
func sqliteDSN(uri string) string {
	dsn := strings.TrimPrefix(uri, "sqlite://")
	if strings.Contains(dsn, "busy_timeout") {
		return dsn
	}
	return withParam(dsn, "_pragma=busy_timeout(5000)")
}

func libsqlDSN(uri, token string) string {
	if token == "" {
		return uri
	}
	return withParam(uri, "authToken="+url.QueryEscape(token))
}

func withParam(dsn, param string) string {
	if strings.Contains(dsn, "?") {
		return dsn + "&" + param
	}
	return dsn + "?" + param
}
