package db

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/JayJamieson/libsql-rest/internal/config"

	// Register the libsql driver under the name "libsql".
	_ "github.com/tursodatabase/libsql-client-go/libsql"
	// Register the pure-Go sqlite driver under the name "sqlite".
	_ "modernc.org/sqlite"
)

// Open opens a *sql.DB for the configured driver. The libsql and sqlite
// drivers are SQLite-compatible, so the rest of the application can treat the
// returned handle uniformly and swap backends via configuration alone.
func Open(cfg config.DBConfig) (*sql.DB, error) {
	driver, dsn, err := resolve(cfg)
	if err != nil {
		return nil, err
	}

	sqlDB, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, err
	}

	// libsql streams connections and will surface STREAM_EXPIRED errors when a
	// connection is held idle past the server timeout, so recycle idle
	// connections proactively. (Previously this was `9`, i.e. 9 nanoseconds.)
	sqlDB.SetConnMaxIdleTime(9 * time.Second)

	return sqlDB, nil
}

// resolve maps a DBConfig to the underlying database/sql driver name and DSN.
func resolve(cfg config.DBConfig) (driver string, dsn string, err error) {
	switch cfg.Driver {
	case config.DriverSQLite:
		return "sqlite", sqliteDSN(cfg.URI), nil
	case config.DriverLibSQL:
		return "libsql", libsqlDSN(cfg.URI, cfg.Token), nil
	default:
		return "", "", fmt.Errorf("unsupported db driver %q", cfg.Driver)
	}
}

// sqliteDSN normalizes a user-supplied URI for the modernc sqlite driver,
// which accepts a bare filename or a "file:" DSN.
func sqliteDSN(uri string) string {
	return strings.TrimPrefix(uri, "sqlite://")
}

// libsqlDSN appends the auth token to a libsql URI as a query parameter,
// preserving any parameters already present.
func libsqlDSN(uri, token string) string {
	if token == "" {
		return uri
	}
	sep := "?"
	if strings.Contains(uri, "?") {
		sep = "&"
	}
	return fmt.Sprintf("%s%sauthToken=%s", uri, sep, url.QueryEscape(token))
}
